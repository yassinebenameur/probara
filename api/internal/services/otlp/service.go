// Package otlp ingests OpenTelemetry metrics pushed by host collectors
// (agent monitors). One accepted export request writes raw samples into the
// generic metric store (shared/metricstore) and records exactly one
// availability heartbeat via monitorstate.Record — server clock, so agent
// clock skew can never distort state or history ordering (agent timestamps
// live only on the samples themselves).
//
// All pdata usage is deliberately confined to this package: pdata is pre-1.0
// and nothing outside ingest should couple to it.
package otlp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"golang.org/x/time/rate"

	"github.com/yassinebenameur/probara/shared/metricstore"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/monitorstate"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

var (
	// ErrUnknownAgent maps to 404: the collector's agent id matches no live
	// agent monitor in this tenant. Non-retryable by design — a deleted
	// monitor must not keep a collector retrying forever.
	ErrUnknownAgent = errors.New("agent monitor not found")
	// ErrMonitorDisabled maps to 403.
	ErrMonitorDisabled = errors.New("agent monitor disabled")
	// ErrRateLimited maps to 429 + Retry-After; the otlphttp exporter backs
	// off and retries, so limited data is delayed, not lost.
	ErrRateLimited = errors.New("otlp ingest rate limited")
)

// ResourceAgentIDAttribute is the resource attribute that can carry the
// agent identity instead of the X-Probara-Agent-Id header (gateway
// collectors multiplexing several hosts set it per resource).
const ResourceAgentIDAttribute = "probara.agent.id"

// futureSampleSlack is how far ahead of the server clock a data point may be
// before it is rejected (protects partition routing from a broken clock).
const futureSampleSlack = 5 * time.Minute

// maxDataPointsPerRequest caps decoded work per request; the handler already
// caps body bytes.
const maxDataPointsPerRequest = 10000

// jobIDNamespace makes heartbeat job ids deterministic per (body, monitor):
// an otlphttp retry after a dropped response dedupes on (job_id,
// result_source) instead of double-heartbeating.
var jobIDNamespace = uuid.NewSHA1(uuid.NameSpaceOID, []byte("probara.otlp.ingest"))

// IngestResult summarizes one export request for the OTLP response.
type IngestResult struct {
	AcceptedPoints int
	RejectedPoints int
	RejectMessage  string
}

// seriesPoint is one decoded data point plus its series description.
type seriesPoint struct {
	MetricName  string
	Attributes  map[string]string
	Unit        string
	MetricType  string // "gauge" | "sum"
	IsMonotonic bool
	Temporality string // "cumulative" | "delta" | "unspecified"
	TS          time.Time
	Value       float64
}

// Service ingests OTLP export requests.
type Service struct {
	db        *sql.DB
	publisher *statusupdates.Publisher
	cache     *seriesCache

	maxSeriesPerMonitor int
	ratePerMin          int

	limiterMu sync.Mutex
	limiters  map[uuid.UUID]*rate.Limiter

	countMu     sync.Mutex
	seriesCount map[uuid.UUID]countEntry
}

type countEntry struct {
	count int
	at    time.Time
}

// NewService creates the ingest service. maxSeriesPerMonitor and ratePerMin
// come from APIConfig (OTLP_MAX_SERIES_PER_MONITOR / OTLP_MONITOR_RATE_PER_MIN).
func NewService(db *sql.DB, publisher *statusupdates.Publisher, maxSeriesPerMonitor, ratePerMin int) *Service {
	if maxSeriesPerMonitor <= 0 {
		maxSeriesPerMonitor = 2000
	}
	if ratePerMin <= 0 {
		ratePerMin = 60
	}
	return &Service{
		db:                  db,
		publisher:           publisher,
		cache:               newSeriesCache(0),
		maxSeriesPerMonitor: maxSeriesPerMonitor,
		ratePerMin:          ratePerMin,
		limiters:            make(map[uuid.UUID]*rate.Limiter),
		seriesCount:         make(map[uuid.UUID]countEntry),
	}
}

// Ingest processes one decoded export request. bodyDigest is the sha256 of
// the decoded request body (heartbeat idempotence). agentIDHeader is the
// X-Probara-Agent-Id value; a ResourceMetrics block carrying
// probara.agent.id overrides it for that block.
//
// The typed errors (ErrUnknownAgent, ErrMonitorDisabled, ErrRateLimited) are
// returned when the request resolves to a single agent identity and that
// identity fails as a whole; in multi-resource exports a failing block is
// counted into RejectedPoints instead so the healthy blocks still land.
func (s *Service) Ingest(ctx context.Context, tenantID uuid.UUID, agentIDHeader string, bodyDigest [32]byte, md pmetric.Metrics) (IngestResult, error) {
	var res IngestResult

	if md.DataPointCount() > maxDataPointsPerRequest {
		return res, fmt.Errorf("too many data points: %d > %d", md.DataPointCount(), maxDataPointsPerRequest)
	}

	// Decode into per-agent batches.
	batches := map[string][]seriesPoint{}
	now := time.Now()
	rms := md.ResourceMetrics()
	distinctAgents := map[string]bool{}
	for i := 0; i < rms.Len(); i++ {
		rm := rms.At(i)
		agentID := agentIDHeader
		if v, ok := rm.Resource().Attributes().Get(ResourceAgentIDAttribute); ok {
			agentID = v.AsString()
		}
		if agentID == "" {
			n := resourcePointCount(rm)
			res.RejectedPoints += n
			res.RejectMessage = "no agent identity: set the X-Probara-Agent-Id header or the probara.agent.id resource attribute"
			continue
		}
		distinctAgents[agentID] = true
		if _, ok := batches[agentID]; !ok {
			// The batch entry exists even if every point is rejected: the
			// export request itself still heartbeats the monitor.
			batches[agentID] = nil
		}
		s.decodeResource(rm, now, agentID, batches, &res)
	}

	singleAgent := len(distinctAgents) == 1

	for agentID, points := range batches {
		monitorID, enabled, err := s.lookupMonitor(ctx, agentID, tenantID)
		if err != nil {
			if singleAgent {
				return res, err
			}
			res.RejectedPoints += len(points)
			res.RejectMessage = err.Error()
			continue
		}
		if !enabled {
			if singleAgent {
				return res, fmt.Errorf("%w: %s", ErrMonitorDisabled, agentID)
			}
			res.RejectedPoints += len(points)
			res.RejectMessage = "agent monitor disabled"
			continue
		}
		if !s.allow(monitorID) {
			if singleAgent {
				return res, ErrRateLimited
			}
			res.RejectedPoints += len(points)
			res.RejectMessage = "rate limited"
			continue
		}

		accepted, rejected, rejectMsg, maxTS, err := s.writeBatch(ctx, tenantID, monitorID, points)
		if err != nil {
			return res, err
		}
		res.AcceptedPoints += accepted
		res.RejectedPoints += rejected
		if rejectMsg != "" {
			res.RejectMessage = rejectMsg
		}

		// Heartbeat: the export request itself is the liveness evidence —
		// even a fully capped batch proves the host's collector is up.
		if err := s.heartbeat(ctx, tenantID, monitorID, bodyDigest, points, maxTS, now); err != nil {
			return res, err
		}
		s.publishStatusUpdate(monitorID, tenantID)
	}

	return res, nil
}

func (s *Service) lookupMonitor(ctx context.Context, agentID string, tenantID uuid.UUID) (uuid.UUID, bool, error) {
	var monitorID uuid.UUID
	var enabled bool
	err := s.db.QueryRowContext(ctx,
		`SELECT id, enabled FROM monitors
		 WHERE agent_id = $1 AND tenant_id = $2 AND type = 'agent' AND deleted_at IS NULL`,
		agentID, tenantID,
	).Scan(&monitorID, &enabled)
	if err == sql.ErrNoRows {
		return uuid.Nil, false, fmt.Errorf("%w: %s", ErrUnknownAgent, agentID)
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("lookup agent monitor: %w", err)
	}
	return monitorID, enabled, nil
}

func (s *Service) allow(monitorID uuid.UUID) bool {
	s.limiterMu.Lock()
	defer s.limiterMu.Unlock()
	lim, ok := s.limiters[monitorID]
	if !ok {
		// Refill at the per-minute rate; burst covers a collector flushing a
		// short outage backlog without tripping the limit.
		lim = rate.NewLimiter(rate.Limit(float64(s.ratePerMin)/60.0), s.ratePerMin)
		s.limiters[monitorID] = lim
	}
	return lim.Allow()
}

// decodeResource flattens one ResourceMetrics block into the agent's batch,
// counting unsupported or invalid points into the result.
func (s *Service) decodeResource(rm pmetric.ResourceMetrics, now time.Time, agentID string, batches map[string][]seriesPoint, res *IngestResult) {
	sms := rm.ScopeMetrics()
	for i := 0; i < sms.Len(); i++ {
		ms := sms.At(i).Metrics()
		for j := 0; j < ms.Len(); j++ {
			metric := ms.At(j)
			switch metric.Type() {
			case pmetric.MetricTypeGauge:
				s.decodeNumberPoints(metric, metric.Gauge().DataPoints(), "gauge", false, "unspecified", now, agentID, batches, res)
			case pmetric.MetricTypeSum:
				sum := metric.Sum()
				temporality := "unspecified"
				switch sum.AggregationTemporality() {
				case pmetric.AggregationTemporalityCumulative:
					temporality = "cumulative"
				case pmetric.AggregationTemporalityDelta:
					temporality = "delta"
				}
				s.decodeNumberPoints(metric, sum.DataPoints(), "sum", sum.IsMonotonic(), temporality, now, agentID, batches, res)
			default:
				n := dataPointCount(metric)
				res.RejectedPoints += n
				res.RejectMessage = fmt.Sprintf("unsupported metric type %s (%s): only gauges and sums are stored", metric.Type(), metric.Name())
			}
		}
	}
}

func (s *Service) decodeNumberPoints(metric pmetric.Metric, dps pmetric.NumberDataPointSlice, metricType string, isMonotonic bool, temporality string, now time.Time, agentID string, batches map[string][]seriesPoint, res *IngestResult) {
	for i := 0; i < dps.Len(); i++ {
		dp := dps.At(i)
		if dp.Flags().NoRecordedValue() {
			continue
		}
		var value float64
		switch dp.ValueType() {
		case pmetric.NumberDataPointValueTypeDouble:
			value = dp.DoubleValue()
		case pmetric.NumberDataPointValueTypeInt:
			value = float64(dp.IntValue())
		default:
			continue
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			res.RejectedPoints++
			res.RejectMessage = fmt.Sprintf("non-finite value for %s", metric.Name())
			continue
		}
		ts := dp.Timestamp().AsTime()
		if ts.IsZero() || ts.Unix() == 0 {
			ts = now
		}
		if ts.After(now.Add(futureSampleSlack)) {
			res.RejectedPoints++
			res.RejectMessage = fmt.Sprintf("data point timestamp too far in the future for %s", metric.Name())
			continue
		}
		attrs := map[string]string{}
		dp.Attributes().Range(func(k string, v pcommon.Value) bool {
			attrs[k] = v.AsString()
			return true
		})
		batches[agentID] = append(batches[agentID], seriesPoint{
			MetricName:  metric.Name(),
			Attributes:  attrs,
			Unit:        metric.Unit(),
			MetricType:  metricType,
			IsMonotonic: isMonotonic,
			Temporality: temporality,
			TS:          ts,
			Value:       value,
		})
	}
}

// writeBatch persists one monitor's points: resolve series (cache-first,
// cardinality-capped), insert samples, refresh series freshness, and mark
// rollup buckets dirty — one transaction, retried once after creating a
// missing partition (partition DDL cannot run inside the failed tx).
func (s *Service) writeBatch(ctx context.Context, tenantID, monitorID uuid.UUID, points []seriesPoint) (accepted, rejected int, rejectMsg string, maxTS time.Time, err error) {
	for attempt := 0; ; attempt++ {
		accepted, rejected, rejectMsg, maxTS, err = s.writeBatchOnce(ctx, tenantID, monitorID, points)
		if err == nil || attempt > 0 || !metricstore.IsMissingPartitionErr(err) {
			return
		}
		minTS, maxSeen := timeBounds(points)
		if ensureErr := metricstore.EnsurePartitions(ctx, s.db, minTS, maxSeen); ensureErr != nil {
			err = fmt.Errorf("ensure metric partitions: %w", ensureErr)
			return
		}
	}
}

func (s *Service) writeBatchOnce(ctx context.Context, tenantID, monitorID uuid.UUID, points []seriesPoint) (int, int, string, time.Time, error) {
	var maxTS time.Time
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, "", maxTS, fmt.Errorf("begin otlp ingest transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rejected := 0
	rejectMsg := ""
	samples := make([]metricstore.Sample, 0, len(points))
	touched := map[int64]bool{}
	hours := map[time.Time]bool{}
	newSeries := 0

	for i := range points {
		p := &points[i]
		hash := metricstore.AttrHash(p.MetricName, p.Attributes)
		id, ok := s.cache.get(monitorID, hash)
		if !ok {
			// Cardinality guardrail on the slow path only: a series already
			// registered keeps flowing even when the monitor is at cap.
			count, cErr := s.cachedSeriesCount(ctx, monitorID)
			if cErr != nil {
				return 0, 0, "", maxTS, cErr
			}
			if count+newSeries >= s.maxSeriesPerMonitor {
				rejected++
				rejectMsg = fmt.Sprintf("series cardinality cap reached (%d per monitor): dropped new series for %s", s.maxSeriesPerMonitor, p.MetricName)
				continue
			}
			id, err = metricstore.ResolveSeries(ctx, tx, metricstore.SeriesKey{
				TenantID:    tenantID,
				MonitorID:   monitorID,
				MetricName:  p.MetricName,
				Unit:        p.Unit,
				MetricType:  p.MetricType,
				IsMonotonic: p.IsMonotonic,
				Temporality: p.Temporality,
				Attributes:  p.Attributes,
			})
			if err != nil {
				return 0, 0, "", maxTS, err
			}
			s.cache.put(monitorID, hash, id)
			newSeries++
		}
		samples = append(samples, metricstore.Sample{SeriesID: id, TS: p.TS, Value: p.Value})
		touched[id] = true
		hours[p.TS.UTC().Truncate(time.Hour)] = true
		if p.TS.After(maxTS) {
			maxTS = p.TS
		}
	}

	if _, err := metricstore.InsertSamples(ctx, tx, samples); err != nil {
		return 0, 0, "", maxTS, err
	}
	ids := make([]int64, 0, len(touched))
	for id := range touched {
		ids = append(ids, id)
	}
	if err := metricstore.TouchSeries(ctx, tx, ids); err != nil {
		return 0, 0, "", maxTS, err
	}
	hourList := make([]time.Time, 0, len(hours))
	for h := range hours {
		hourList = append(hourList, h)
	}
	if err := metricstore.MarkDirty(ctx, tx, monitorID, hourList); err != nil {
		return 0, 0, "", maxTS, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, "", maxTS, fmt.Errorf("commit otlp ingest transaction: %w", err)
	}
	if newSeries > 0 {
		s.invalidateSeriesCount(monitorID)
	}
	return len(samples), rejected, rejectMsg, maxTS, nil
}

// heartbeat records the availability evidence for one accepted export
// request. StartedAt is the SERVER clock (deliberate: agent clocks are
// untrusted for state; see docs/state-semantics.md S-F1/S-O2). LatencyMs is
// informational: receipt delay vs the newest data point.
func (s *Service) heartbeat(ctx context.Context, tenantID, monitorID uuid.UUID, bodyDigest [32]byte, points []seriesPoint, maxTS, receivedAt time.Time) error {
	jobID := uuid.NewSHA1(jobIDNamespace, append(bodyDigest[:], monitorID[:]...))
	var latency int64
	if !maxTS.IsZero() {
		latency = receivedAt.Sub(maxTS).Milliseconds()
		if latency < 0 {
			latency = 0
		}
	}
	if _, err := monitorstate.Record(ctx, s.db, monitorstate.Result{
		MonitorID:    monitorID,
		TenantID:     tenantID,
		JobID:        jobID,
		Status:       "success",
		ResultSource: string(models.ResultSourceMonitor),
		LatencyMs:    &latency,
		MetricsData:  buildLegacySnapshot(points, receivedAt),
		StartedAt:    receivedAt,
		CompletedAt:  receivedAt,
	}); err != nil {
		return fmt.Errorf("record otlp heartbeat: %w", err)
	}
	return nil
}

func (s *Service) cachedSeriesCount(ctx context.Context, monitorID uuid.UUID) (int, error) {
	s.countMu.Lock()
	entry, ok := s.seriesCount[monitorID]
	s.countMu.Unlock()
	if ok && time.Since(entry.at) < time.Minute {
		return entry.count, nil
	}
	count, err := metricstore.CountSeries(ctx, s.db, monitorID)
	if err != nil {
		return 0, err
	}
	s.countMu.Lock()
	s.seriesCount[monitorID] = countEntry{count: count, at: time.Now()}
	s.countMu.Unlock()
	return count, nil
}

func (s *Service) invalidateSeriesCount(monitorID uuid.UUID) {
	s.countMu.Lock()
	delete(s.seriesCount, monitorID)
	s.countMu.Unlock()
}

func (s *Service) publishStatusUpdate(monitorID, tenantID uuid.UUID) {
	if s.publisher == nil {
		return
	}
	_ = s.publisher.Publish(statusupdates.Event{
		Type:      "check_result",
		MonitorID: monitorID.String(),
		TenantID:  tenantID.String(),
		Timestamp: time.Now().UTC(),
	})
}

func timeBounds(points []seriesPoint) (time.Time, time.Time) {
	if len(points) == 0 {
		now := time.Now()
		return now, now
	}
	min, max := points[0].TS, points[0].TS
	for _, p := range points[1:] {
		if p.TS.Before(min) {
			min = p.TS
		}
		if p.TS.After(max) {
			max = p.TS
		}
	}
	return min, max
}

func resourcePointCount(rm pmetric.ResourceMetrics) int {
	n := 0
	sms := rm.ScopeMetrics()
	for i := 0; i < sms.Len(); i++ {
		ms := sms.At(i).Metrics()
		for j := 0; j < ms.Len(); j++ {
			n += dataPointCount(ms.At(j))
		}
	}
	return n
}

func dataPointCount(m pmetric.Metric) int {
	switch m.Type() {
	case pmetric.MetricTypeGauge:
		return m.Gauge().DataPoints().Len()
	case pmetric.MetricTypeSum:
		return m.Sum().DataPoints().Len()
	case pmetric.MetricTypeHistogram:
		return m.Histogram().DataPoints().Len()
	case pmetric.MetricTypeExponentialHistogram:
		return m.ExponentialHistogram().DataPoints().Len()
	case pmetric.MetricTypeSummary:
		return m.Summary().DataPoints().Len()
	}
	return 0
}
