// Package depsuggest produces AI-suggested monitor dependency edges from
// co-firing alert history. It's a live advisory: suggestions are not
// persisted (accepting one goes through the normal add-dependency path);
// operator verdicts are stored as AI labels (ailabels), whose evidence
// PairDecision recomputes here.
package depsuggest

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/apperr"
	"github.com/yassinebenameur/probara/shared/ai"
	shareddb "github.com/yassinebenameur/probara/shared/db"
)

const (
	lookbackDays = 30
	windowSecs   = 600 // co-firing window: alerts within 10 minutes
	minCoFire    = 2   // ignore one-off coincidences
	maxPairs     = 50
	maxMonitors  = 250 // cap monitors fed to the model to bound candidate collection
)

// configProvider yields the effective local-service config for a tenant (satisfied by
// *aisettings.Service).
type configProvider interface {
	EffectiveConfig(ctx context.Context, tenantID uuid.UUID) (ai.Config, bool, error)
}

// advisorFactory builds a DependencyAdvisor from a config. Indirected for tests.
type advisorFactory func(ai.Config) (ai.DependencyAdvisor, error)

// Suggestion is one resolved, ready-to-accept dependency proposal.
type Suggestion struct {
	MonitorID     uuid.UUID   `json:"monitor_id"`
	MonitorName   string      `json:"monitor_name"`
	DependsOnID   uuid.UUID   `json:"depends_on_id"`
	DependsOnName string      `json:"depends_on_name"`
	Basis         string      `json:"basis"` // ai.DependencyGuard rule: timing | variant | structural
	Reason        string      `json:"reason"`
	Confidence    string      `json:"confidence"`
	Metadata      ai.Metadata `json:"metadata"`
}

// Result is the suggestion endpoint response.
type Result struct {
	Suggestions []Suggestion `json:"suggestions"`
	Model       string       `json:"model,omitempty"`
	// Analyzed is the number of candidate pairs judged (by the evidence guard,
	// then the model) and Abstained those not suggested, so Analyzed ==
	// Abstained + len(Suggestions). Lets the UI distinguish "no signal" from
	// "no suggestions".
	Analyzed  int `json:"analyzed_pairs"`
	Abstained int `json:"abstained_pairs"`
}

// Service computes dependency suggestions.
type Service struct {
	db      *shareddb.Client
	cfg     configProvider
	advisor advisorFactory
}

// NewService wires the DB and effective-config provider. The advisor factory
// defaults to ai.NewDependencyAdvisor.
func NewService(db *shareddb.Client, cfg configProvider) *Service {
	return &Service{db: db, cfg: cfg, advisor: ai.NewDependencyAdvisor}
}

type coFire struct {
	earlier uuid.UUID
	later   uuid.UUID
	count   int
	lead    int
	reverse int
	// windowMonitors: median number of monitors alerting in the shared windows.
	windowMonitors int
}

// Suggest builds the co-firing evidence, asks the model for edges, and resolves
// them to real monitors. Returns ai.ErrNotConfigured when local AI is disabled.
func (s *Service) Suggest(ctx context.Context, tenantID uuid.UUID) (*Result, error) {
	cfg, ok, err := s.cfg.EffectiveConfig(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ai.ErrNotConfigured
	}

	set, err := s.buildInput(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if len(set.input.Monitors) == 0 {
		return &Result{Suggestions: []Suggestion{}, Analyzed: set.coFiring, Abstained: set.coFiring}, nil
	}
	input, idByRef, names, existing := set.input, set.idByRef, set.names, set.existing

	advisor, err := s.advisor(cfg)
	if err != nil {
		return nil, err
	}
	out, err := advisor.SuggestDependencies(ctx, input)
	if err != nil {
		return nil, err
	}

	existingSet := map[[2]uuid.UUID]bool{}
	for _, e := range existing {
		existingSet[[2]uuid.UUID{e.monitor, e.dependsOn}] = true
	}
	seen := map[[2]uuid.UUID]bool{}
	result := &Result{Suggestions: []Suggestion{}, Model: out.Model, Analyzed: out.Analyzed, Abstained: out.Abstained}
	graph := append([]ai.DependencyEdgeRef{}, input.ExistingDependencies...)
	for _, sg := range out.Suggestions {
		mID, ok1 := idByRef[sg.Monitor]
		dID, ok2 := idByRef[sg.DependsOn]
		// A dropped suggestion is an abstention, keeping Analyzed ==
		// Abstained + len(Suggestions).
		if !ok1 || !ok2 || mID == dID {
			result.Abstained++
			continue // hallucinated ref or self-edge
		}
		key := [2]uuid.UUID{mID, dID}
		if existingSet[key] || seen[key] || ai.WouldCycle(graph, sg.Monitor, sg.DependsOn) {
			result.Abstained++
			continue
		}
		seen[key] = true
		graph = append(graph, ai.DependencyEdgeRef{Monitor: sg.Monitor, DependsOn: sg.DependsOn})
		result.Suggestions = append(result.Suggestions, Suggestion{
			MonitorID:     mID,
			MonitorName:   names[mID],
			DependsOnID:   dID,
			DependsOnName: names[dID],
			Basis:         sg.Basis,
			Reason:        sg.Reason,
			Confidence:    sg.Confidence,
			Metadata:      sg.Metadata,
		})
	}
	return result, nil
}

// inputSet is the advisor input for a tenant plus the maps that resolve its
// opaque refs back to monitors.
type inputSet struct {
	input    ai.DependencyInput
	idByRef  map[string]uuid.UUID
	refByID  map[uuid.UUID]string
	names    map[uuid.UUID]string
	existing []edge
	coFiring int // co-firing pairs found, before ref resolution
}

// buildInput is the single builder of the advisor input. Suggest and
// PairDecision both go through it, so a label's evidence is exactly what
// Suggest would send for that pair at the same moment.
func (s *Service) buildInput(ctx context.Context, tenantID uuid.UUID) (*inputSet, error) {
	pairs, err := s.coFiringPairs(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	set := &inputSet{idByRef: map[string]uuid.UUID{}, refByID: map[uuid.UUID]string{}, names: map[uuid.UUID]string{}, coFiring: len(pairs)}

	// Monitors that co-fired are the highest-signal candidates; load them first,
	// then fill with the rest (so name/tag/group inference works beyond
	// co-firing), capped to keep candidate collection bounded.
	involvedOrder := make([]uuid.UUID, 0, len(pairs)*2)
	involved := map[uuid.UUID]bool{}
	for _, p := range pairs {
		for _, id := range [2]uuid.UUID{p.earlier, p.later} {
			if !involved[id] {
				involved[id] = true
				involvedOrder = append(involvedOrder, id)
			}
		}
	}
	monitors, all, err := s.loadMonitors(ctx, tenantID, involvedOrder)
	if err != nil {
		return nil, err
	}
	if len(monitors) == 0 {
		return set, nil
	}

	ids := make([]uuid.UUID, 0, len(monitors))
	for i, m := range monitors {
		ref := "m" + strconv.Itoa(i)
		set.refByID[m.id] = ref
		set.idByRef[ref] = m.id
		set.names[m.id] = m.name
		ids = append(ids, m.id)
	}

	groups, err := s.loadGroups(ctx, tenantID, ids)
	if err != nil {
		return nil, err
	}
	windows, err := s.alertWindows(ctx, tenantID, ids)
	if err != nil {
		return nil, err
	}
	set.existing, err = s.existingEdges(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	refFor := func(id uuid.UUID) string {
		if ref, ok := set.refByID[id]; ok {
			return ref
		}
		return id.String()
	}
	// Tag and name-prefix populations are tenant-wide: every monitor
	// loadMonitors found, not only the capped candidates.
	everyone := make([]ai.DependencyMonitorRef, 0, len(all))
	for _, m := range all {
		everyone = append(everyone, ai.DependencyMonitorRef{Name: m.name, Tags: m.tags})
	}
	set.input.TagMonitors, set.input.PrefixMonitors = ai.MonitorPopulations(everyone)
	// Groups reach the advisor as opaque refs, so two groups sharing a name
	// stay two groups.
	groupRefs := map[uuid.UUID]string{}
	for _, m := range monitors {
		ref := ai.DependencyMonitorRef{
			Ref:          set.refByID[m.id],
			Name:         m.name,
			Type:         m.typ,
			Tags:         m.tags,
			AlertWindows: windows[m.id],
		}
		for _, g := range groups[m.id] {
			if _, ok := groupRefs[g.id]; !ok {
				groupRefs[g.id] = "g" + strconv.Itoa(len(groupRefs))
			}
			ref.Groups = append(ref.Groups, ai.DependencyGroupRef{Ref: groupRefs[g.id], Members: g.members})
		}
		set.input.Monitors = append(set.input.Monitors, ref)
	}
	for _, e := range set.existing {
		set.input.ExistingDependencies = append(set.input.ExistingDependencies, ai.DependencyEdgeRef{
			Monitor:   refFor(e.monitor),
			DependsOn: refFor(e.dependsOn),
		})
	}
	for _, p := range pairs {
		// Co-firing monitors are loaded first; a disabled or capped-out one
		// resolves to "" and ai.CandidatePairs drops the pair.
		set.input.CoFiringAlerts = append(set.input.CoFiringAlerts, ai.CoFiringPair{
			Earlier:           set.refByID[p.earlier],
			Later:             set.refByID[p.later],
			TimesCoFired:      p.count,
			MedianLeadSeconds: p.lead,
			ReverseCoFires:    p.reverse,
			WindowMonitors:    p.windowMonitors,
		})
	}
	return set, nil
}

var (
	// ErrMonitorNotFound: a monitor of the pair is not a live monitor of the tenant.
	ErrMonitorNotFound = apperr.ErrMonitorNotFound
	// ErrPairUnavailable: the pair is not currently a dependency candidate
	// (disabled monitor, group, outside the monitor cap, or no signal).
	ErrPairUnavailable = errors.New("pair evidence unavailable")
)

// PairDecision recomputes, through the same input builder as Suggest, the
// pair evidence for (monitorID, dependsOnID). Does not call the model and does
// not require local AI to be enabled. The decision is oriented like the
// advisor's (a < b by ref), and its Pair carries the monitor UUIDs.
func (s *Service) PairDecision(ctx context.Context, tenantID, monitorID, dependsOnID uuid.UUID) (ai.SnapshotDecision, error) {
	var live int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM monitors WHERE tenant_id=$1 AND deleted_at IS NULL AND id=ANY($2)`,
		tenantID, pq.Array([]uuid.UUID{monitorID, dependsOnID})).Scan(&live); err != nil {
		return ai.SnapshotDecision{}, fmt.Errorf("pair monitors query: %w", err)
	}
	if monitorID == dependsOnID || live != 2 {
		return ai.SnapshotDecision{}, ErrMonitorNotFound
	}
	set, err := s.buildInput(ctx, tenantID)
	if err != nil {
		return ai.SnapshotDecision{}, err
	}
	refX, okX := set.refByID[monitorID]
	refY, okY := set.refByID[dependsOnID]
	if !okX || !okY {
		return ai.SnapshotDecision{}, ErrPairUnavailable
	}
	aRef, bRef, e, ok := ai.PairDecisionFor(set.input, refX, refY)
	if !ok {
		return ai.SnapshotDecision{}, ErrPairUnavailable
	}
	return ai.PairSnapshotDecision(set.idByRef[aRef].String(), set.idByRef[bRef].String(), e), nil
}

// alertBucketsCTE is the one definition of an alert window: each monitor's
// availability alerts of the tenant in the lookback, collapsed to one row per
// (monitor, 10-minute bucket) with the bucket's first trigger. $1 tenant, $2
// lookback days, $3 window seconds. coFiringPairs and alertWindows both use
// it, so a monitor's window total counts the same windows its pairs share.
// Only candidate-eligible monitors (enabled, not deleted, not a group) count,
// so a group's roll-up alert or a disabled monitor never inflates a window.
const alertBucketsCTE = `
		firsts AS (
			SELECT monitor_id,
			       floor(extract(epoch FROM triggered_at) / $3)::bigint AS bucket,
			       MIN(triggered_at) AS first_at
			FROM alerts a JOIN monitors m ON m.id=a.monitor_id AND m.tenant_id=$1
			     AND m.deleted_at IS NULL AND m.enabled = TRUE AND m.type <> 'group'
			WHERE a.tenant_id=$1 AND a.kind='availability' AND triggered_at > NOW() - ($2 || ' days')::interval
			GROUP BY monitor_id, bucket
		)`

func (s *Service) coFiringPairs(ctx context.Context, tenantID uuid.UUID) ([]coFire, error) {
	// Bucket each monitor's alerts into fixed windows first (collapsing bursts to
	// one row per monitor+window), then join on shared windows. This bounds the
	// join — a raw alert self-join explodes combinatorially when monitors flap.
	// cnt = number of shared windows; median_lead = median(later.first -
	// earlier.first) across them (sign indicates which monitor tends to fail
	// first, hence is likely upstream); window_monitors = median number of
	// monitors alerting in those windows, rounded up.
	//
	// Cap order: first pairs that only ever failed in one direction with a
	// median of at most 3 monitors alerting per shared window (what a timing
	// suggestion needs), then other one-direction pairs (variants), then the
	// rest; within each, more co-fires, then smaller windows. So neither a
	// simultaneous pile-up nor a large one-direction one (fast- and
	// slow-interval monitors failing together) pushes a small directed pair
	// out of the cap.
	rows, err := s.db.QueryContext(ctx, `
		WITH `+alertBucketsCTE+`,
		windows AS (
			SELECT monitor_id, bucket, first_at, COUNT(*) OVER (PARTITION BY bucket) AS alerting
			FROM firsts
		)
		SELECT f1.monitor_id, f2.monitor_id, COUNT(*),
		       COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (
		           ORDER BY EXTRACT(EPOCH FROM (f2.first_at - f1.first_at))), 0)::int,
		       COUNT(*) FILTER (WHERE f2.first_at<f1.first_at), COUNT(*) FILTER (WHERE f2.first_at>f1.first_at),
		       CEIL(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY f1.alerting))::int
		FROM windows f1
		JOIN windows f2 ON f1.bucket = f2.bucket AND f1.monitor_id <> f2.monitor_id
		GROUP BY f1.monitor_id, f2.monitor_id
		HAVING COUNT(*) >= $4
		ORDER BY (LEAST(COUNT(*) FILTER (WHERE f2.first_at<f1.first_at), COUNT(*) FILTER (WHERE f2.first_at>f1.first_at)) = 0
		          AND GREATEST(COUNT(*) FILTER (WHERE f2.first_at<f1.first_at), COUNT(*) FILTER (WHERE f2.first_at>f1.first_at)) > 0
		          AND CEIL(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY f1.alerting)) <= 3) DESC,
		         (LEAST(COUNT(*) FILTER (WHERE f2.first_at<f1.first_at), COUNT(*) FILTER (WHERE f2.first_at>f1.first_at)) = 0
		          AND GREATEST(COUNT(*) FILTER (WHERE f2.first_at<f1.first_at), COUNT(*) FILTER (WHERE f2.first_at>f1.first_at)) > 0) DESC,
		         COUNT(*) DESC, 7, f1.monitor_id, f2.monitor_id
		LIMIT $5`,
		tenantID, strconv.Itoa(lookbackDays), windowSecs, minCoFire, maxPairs*2)
	if err != nil {
		return nil, fmt.Errorf("co-firing query: %w", err)
	}
	defer rows.Close()

	// The query yields both (A,B) and (B,A) with equal counts and opposite lead
	// signs. Collapse to one row per unordered pair, oriented so "earlier" is the
	// monitor that tends to fail first (non-negative lead).
	seen := map[[2]uuid.UUID]bool{}
	var pairs []coFire
	for rows.Next() {
		var m1, m2 uuid.UUID
		var count, lead, reverse, positive, windowMonitors int
		if err := rows.Scan(&m1, &m2, &count, &lead, &reverse, &positive, &windowMonitors); err != nil {
			return nil, err
		}
		key := [2]uuid.UUID{m1, m2}
		if m1.String() > m2.String() {
			key = [2]uuid.UUID{m2, m1}
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		p := coFire{earlier: m1, later: m2, count: count, lead: lead, reverse: reverse, windowMonitors: windowMonitors}
		if lead < 0 { // orient so earlier truly leads
			p.earlier, p.later, p.lead = m2, m1, -lead
			p.reverse = positive
		}
		pairs = append(pairs, p)
		if len(pairs) >= maxPairs {
			break
		}
	}
	return pairs, rows.Err()
}

// alertWindows counts each monitor's alert windows (alertBucketsCTE) in the
// lookback, for the monitors in ids.
func (s *Service) alertWindows(ctx context.Context, tenantID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH `+alertBucketsCTE+`
		SELECT monitor_id, COUNT(*) FROM firsts WHERE monitor_id = ANY($4) GROUP BY monitor_id`,
		tenantID, strconv.Itoa(lookbackDays), windowSecs, pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("alert windows query: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

type monitorRow struct {
	id   uuid.UUID
	name string
	typ  string
	tags []string
}

// loadMonitors returns candidate monitors with tags, ordering the prioritized
// (co-firing) ones first, then the rest, capped at maxMonitors, and every
// candidate-eligible monitor of the tenant uncapped (for populations).
// Group-type monitors are excluded (they are aggregates, not check targets).
func (s *Service) loadMonitors(ctx context.Context, tenantID uuid.UUID, prioritized []uuid.UUID) (capped, all []monitorRow, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, type, COALESCE(tags, '{}')
		FROM monitors
		WHERE tenant_id = $1 AND deleted_at IS NULL AND enabled = TRUE AND type <> 'group' ORDER BY id`,
		tenantID)
	if err != nil {
		return nil, nil, fmt.Errorf("monitors query: %w", err)
	}
	defer rows.Close()

	byID := map[uuid.UUID]monitorRow{}
	for rows.Next() {
		var m monitorRow
		var tags pq.StringArray
		if err := rows.Scan(&m.id, &m.name, &m.typ, &tags); err != nil {
			return nil, nil, err
		}
		m.tags = []string(tags)
		byID[m.id] = m
		all = append(all, m)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	result := make([]monitorRow, 0, maxMonitors)
	added := map[uuid.UUID]bool{}
	add := func(id uuid.UUID) {
		if added[id] || len(result) >= maxMonitors {
			return
		}
		if m, ok := byID[id]; ok {
			result = append(result, m)
			added[id] = true
		}
	}
	for _, id := range prioritized {
		add(id)
	}
	for _, m := range all {
		add(m.id)
	}
	return result, all, nil
}

type groupRef struct {
	id      uuid.UUID
	members int
}

// loadGroups maps each monitor to the groups it belongs to: group id and
// member count (the group's live members of the tenant).
func (s *Service) loadGroups(ctx context.Context, tenantID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID][]groupRef, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT mg.monitor_id, g.id,
		       (SELECT COUNT(*) FROM monitor_groups gm
		        JOIN monitors mm ON mm.id=gm.monitor_id AND mm.tenant_id=$2 AND mm.deleted_at IS NULL
		        WHERE gm.group_id=g.id)
		FROM monitor_groups mg
		JOIN monitors g ON g.id=mg.group_id AND g.tenant_id=$2
		JOIN monitors m ON m.id=mg.monitor_id AND m.tenant_id=$2
		WHERE mg.monitor_id=ANY($1) ORDER BY mg.monitor_id,g.id`, pq.Array(ids), tenantID)
	if err != nil {
		return nil, fmt.Errorf("groups query: %w", err)
	}
	defer rows.Close()
	groups := map[uuid.UUID][]groupRef{}
	for rows.Next() {
		var id uuid.UUID
		var g groupRef
		if err := rows.Scan(&id, &g.id, &g.members); err != nil {
			return nil, err
		}
		groups[id] = append(groups[id], g)
	}
	return groups, rows.Err()
}

type edge struct {
	monitor   uuid.UUID
	dependsOn uuid.UUID
}

func (s *Service) existingEdges(ctx context.Context, tenantID uuid.UUID) ([]edge, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT md.monitor_id,md.depends_on_id FROM monitor_dependencies md
 JOIN monitors m ON m.id=md.monitor_id AND m.tenant_id=$1
 JOIN monitors u ON u.id=md.depends_on_id AND u.tenant_id=$1
 ORDER BY md.monitor_id,md.depends_on_id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("existing edges query: %w", err)
	}
	defer rows.Close()
	var edges []edge
	for rows.Next() {
		var e edge
		if err := rows.Scan(&e.monitor, &e.dependsOn); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}
