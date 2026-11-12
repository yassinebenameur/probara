// Latest host metrics for agent monitors, read from the metric store.

package statuspage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/metricstore"
	"github.com/yassinebenameur/probara/shared/monitorstate"
)

// agentMetricNames are the curated OpenTelemetry host-metric series the
// agent collector emits that the status-page summary cards consume.
var agentMetricNames = []string{
	"system.cpu.utilization",
	"system.memory.usage",
	"system.memory.utilization",
	"system.filesystem.usage",
	"system.network.io",
	"system.cpu.load_average.1m",
	"system.cpu.load_average.5m",
	"system.cpu.load_average.15m",
	"system.processes.count",
}

// metricstoreQuerier adapts the service's read-only db.Querier to
// metricstore.DBTX, which also declares ExecContext for write paths this
// service never takes.
type metricstoreQuerier struct{ db.Querier }

func (metricstoreQuerier) ExecContext(context.Context, string, ...interface{}) (sql.Result, error) {
	return nil, errors.New("statuspage: metric store access is read-only")
}

// nonNegBytes converts an aggregated float sample to the uint64 the display
// struct carries, clamping pathological negatives instead of wrapping.
func nonNegBytes(v float64) uint64 {
	if v <= 0 {
		return 0
	}
	return uint64(v)
}

// GetLatestAgentMetrics retrieves the most recent agent metrics for a monitor
// from the generic metric store (the newest fresh sample per curated host
// series), mapped onto the same summary shape the templates always rendered.
func (s *Service) GetLatestAgentMetrics(ctx context.Context, monitorID, tenantID uuid.UUID) (*AgentMetricsData, error) {
	// Freshness bound: interval × 3 (floor 90s), the same horizon state
	// evaluation uses — a dead agent's last readings must not render
	// forever. Fall back to a fixed window if the interval can't be read.
	freshness := 15 * time.Minute
	var intervalSeconds int
	if err := s.db.QueryRowContext(ctx,
		`SELECT interval_seconds FROM monitors WHERE id = $1 AND tenant_id = $2`,
		monitorID, tenantID,
	).Scan(&intervalSeconds); err == nil && intervalSeconds > 0 {
		freshness = time.Duration(monitorstate.FreshnessHorizonSeconds(intervalSeconds)) * time.Second
	}

	samples, err := metricstore.LatestSamples(ctx, metricstoreQuerier{s.db}, tenantID, monitorID, agentMetricNames, freshness)
	if err != nil {
		return nil, fmt.Errorf("failed to get agent metrics: %w", err)
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("no agent metrics found")
	}

	metrics := &AgentMetricsData{}
	var (
		memUsed, memTotal   float64 // bytes, from system.memory.usage per state
		memUtil             float64 // ratio 0-1, from system.memory.utilization{state=used}
		memUtilSeen         bool
		diskUsed, diskTotal float64 // bytes, system.filesystem.usage summed across mountpoints
		netIn, netOut       float64 // cumulative counter bytes summed across devices
	)

	for _, smp := range samples {
		if smp.TS.After(metrics.Timestamp) {
			metrics.Timestamp = smp.TS
		}
		state := smp.Attributes["state"]
		switch smp.MetricName {
		case "system.cpu.utilization":
			if state == "used" {
				metrics.CPUPercent = smp.Value * 100
			}
		case "system.memory.utilization":
			if state == "used" {
				memUtil = smp.Value
				memUtilSeen = true
			}
		case "system.memory.usage":
			memTotal += smp.Value
			if state == "used" {
				memUsed += smp.Value
			}
		case "system.filesystem.usage":
			diskTotal += smp.Value
			if state == "used" {
				diskUsed += smp.Value
			}
		case "system.network.io":
			switch smp.Attributes["direction"] {
			case "receive":
				netIn += smp.Value
			case "transmit":
				netOut += smp.Value
			}
		case "system.cpu.load_average.1m":
			metrics.LoadAvg1 = smp.Value
		case "system.cpu.load_average.5m":
			metrics.LoadAvg5 = smp.Value
		case "system.cpu.load_average.15m":
			metrics.LoadAvg15 = smp.Value
		case "system.processes.count":
			metrics.ProcessCount += int(smp.Value)
		}
	}

	// Memory: used/total from per-state usage bytes; if only the utilization
	// ratio was reported alongside used bytes, derive the total from it.
	if memTotal == 0 && memUtilSeen && memUtil > 0 && memUsed > 0 {
		memTotal = memUsed / memUtil
	}
	metrics.MemoryUsed = nonNegBytes(memUsed)
	metrics.MemoryTotal = nonNegBytes(memTotal)
	metrics.DiskUsed = nonNegBytes(diskUsed)
	metrics.DiskTotal = nonNegBytes(diskTotal)
	metrics.NetworkBytesIn = nonNegBytes(netIn)
	metrics.NetworkBytesOut = nonNegBytes(netOut)

	// Calculate percentages
	if metrics.MemoryTotal > 0 {
		metrics.MemoryPercent = float64(metrics.MemoryUsed) / float64(metrics.MemoryTotal) * 100
	} else if memUtilSeen {
		metrics.MemoryPercent = memUtil * 100
	}
	if metrics.DiskTotal > 0 {
		metrics.DiskPercent = float64(metrics.DiskUsed) / float64(metrics.DiskTotal) * 100
	}

	return metrics, nil
}
