// Package depsuggest produces AI-suggested monitor dependency edges from
// co-firing alert history. It's a live, read-only advisory: nothing is
// persisted until the user accepts a suggestion (which goes through the normal
// add-dependency path).
package depsuggest

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/lib/pq"

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
	Reason        string      `json:"reason"`
	Confidence    string      `json:"confidence"`
	Metadata      ai.Metadata `json:"metadata"`
}

// Result is the suggestion endpoint response.
type Result struct {
	Suggestions []Suggestion `json:"suggestions"`
	Model       string       `json:"model,omitempty"`
	// Analyzed is the number of co-firing pairs fed to the model — lets the UI
	// distinguish "no signal" from "no suggestions".
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

	pairs, err := s.coFiringPairs(ctx, tenantID)
	if err != nil {
		return nil, err
	}

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
	monitors, err := s.loadMonitors(ctx, tenantID, involvedOrder)
	if err != nil {
		return nil, err
	}
	if len(monitors) == 0 {
		return &Result{Suggestions: []Suggestion{}, Analyzed: len(pairs)}, nil
	}

	idByRef := map[string]uuid.UUID{}
	refByID := map[uuid.UUID]string{}
	names := map[uuid.UUID]string{}
	ids := make([]uuid.UUID, 0, len(monitors))
	for i, m := range monitors {
		ref := "m" + strconv.Itoa(i)
		refByID[m.id] = ref
		idByRef[ref] = m.id
		names[m.id] = m.name
		ids = append(ids, m.id)
	}

	groups, err := s.loadGroups(ctx, tenantID, ids)
	if err != nil {
		return nil, err
	}
	existing, err := s.existingEdges(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	refFor := func(id uuid.UUID) string {
		if ref, ok := refByID[id]; ok {
			return ref
		}
		return id.String()
	}
	input := ai.DependencyInput{}
	for _, m := range monitors {
		input.Monitors = append(input.Monitors, ai.DependencyMonitorRef{
			Ref:    refByID[m.id],
			Name:   m.name,
			Type:   m.typ,
			Tags:   m.tags,
			Groups: groups[m.id],
		})
	}
	for _, e := range existing {
		input.ExistingDependencies = append(input.ExistingDependencies, ai.DependencyEdgeRef{
			Monitor:   refFor(e.monitor),
			DependsOn: refFor(e.dependsOn),
		})
	}
	for _, p := range pairs {
		// Co-firing monitors are always in the set (loaded first), so refs exist.
		input.CoFiringAlerts = append(input.CoFiringAlerts, ai.CoFiringPair{
			Earlier:           refByID[p.earlier],
			Later:             refByID[p.later],
			TimesCoFired:      p.count,
			MedianLeadSeconds: p.lead,
			ReverseCoFires:    p.reverse,
		})
	}

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
		if !ok1 || !ok2 || mID == dID {
			continue // hallucinated ref or self-edge
		}
		key := [2]uuid.UUID{mID, dID}
		if existingSet[key] || seen[key] || ai.WouldCycle(graph, sg.Monitor, sg.DependsOn) {
			continue
		}
		seen[key] = true
		graph = append(graph, ai.DependencyEdgeRef{Monitor: sg.Monitor, DependsOn: sg.DependsOn})
		result.Suggestions = append(result.Suggestions, Suggestion{
			MonitorID:     mID,
			MonitorName:   names[mID],
			DependsOnID:   dID,
			DependsOnName: names[dID],
			Reason:        sg.Reason,
			Confidence:    sg.Confidence,
			Metadata:      sg.Metadata,
		})
	}
	return result, nil
}

func (s *Service) coFiringPairs(ctx context.Context, tenantID uuid.UUID) ([]coFire, error) {
	// Bucket each monitor's alerts into fixed windows first (collapsing bursts to
	// one row per monitor+window), then join on shared windows. This bounds the
	// join — a raw alert self-join explodes combinatorially when monitors flap.
	// cnt = number of shared windows; median_lead = median(later.first -
	// earlier.first) across them (sign indicates which monitor tends to fail
	// first, hence is likely upstream).
	rows, err := s.db.QueryContext(ctx, `
		WITH firsts AS (
			SELECT monitor_id,
			       floor(extract(epoch FROM triggered_at) / $3)::bigint AS bucket,
			       MIN(triggered_at) AS first_at
			FROM alerts a JOIN monitors m ON m.id=a.monitor_id AND m.tenant_id=$1
 WHERE a.tenant_id=$1 AND a.kind='availability' AND triggered_at > NOW() - ($2 || ' days')::interval
			GROUP BY monitor_id, bucket
		)
		SELECT f1.monitor_id, f2.monitor_id, COUNT(*),
		       COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (
		           ORDER BY EXTRACT(EPOCH FROM (f2.first_at - f1.first_at))), 0)::int,
 COUNT(*) FILTER (WHERE f2.first_at<f1.first_at),COUNT(*) FILTER (WHERE f2.first_at>f1.first_at)
		FROM firsts f1
		JOIN firsts f2 ON f1.bucket = f2.bucket AND f1.monitor_id <> f2.monitor_id
		GROUP BY f1.monitor_id, f2.monitor_id
		HAVING COUNT(*) >= $4
		ORDER BY COUNT(*) DESC,f1.monitor_id,f2.monitor_id
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
		var count, lead, reverse, positive int
		if err := rows.Scan(&m1, &m2, &count, &lead, &reverse, &positive); err != nil {
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
		p := coFire{earlier: m1, later: m2, count: count, lead: lead, reverse: reverse}
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

type monitorRow struct {
	id   uuid.UUID
	name string
	typ  string
	tags []string
}

// loadMonitors returns candidate monitors with tags, ordering the prioritized
// (co-firing) ones first, then the rest, capped at maxMonitors. Group-type
// monitors are excluded (they are aggregates, not check targets).
func (s *Service) loadMonitors(ctx context.Context, tenantID uuid.UUID, prioritized []uuid.UUID) ([]monitorRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, type, COALESCE(tags, '{}')
		FROM monitors
		WHERE tenant_id = $1 AND deleted_at IS NULL AND enabled = TRUE AND type <> 'group' ORDER BY id`,
		tenantID)
	if err != nil {
		return nil, fmt.Errorf("monitors query: %w", err)
	}
	defer rows.Close()

	byID := map[uuid.UUID]monitorRow{}
	order := make([]uuid.UUID, 0)
	for rows.Next() {
		var m monitorRow
		var tags pq.StringArray
		if err := rows.Scan(&m.id, &m.name, &m.typ, &tags); err != nil {
			return nil, err
		}
		m.tags = []string(tags)
		byID[m.id] = m
		order = append(order, m.id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
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
	for _, id := range order {
		add(id)
	}
	return result, nil
}

// loadGroups maps each monitor to the names of the groups it belongs to.
func (s *Service) loadGroups(ctx context.Context, tenantID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID][]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT mg.monitor_id, g.name
		FROM monitor_groups mg
		JOIN monitors g ON g.id=mg.group_id AND g.tenant_id=$2
 JOIN monitors m ON m.id=mg.monitor_id AND m.tenant_id=$2
 WHERE mg.monitor_id=ANY($1) ORDER BY mg.monitor_id,g.id`, pq.Array(ids), tenantID)
	if err != nil {
		return nil, fmt.Errorf("groups query: %w", err)
	}
	defer rows.Close()
	groups := map[uuid.UUID][]string{}
	for rows.Next() {
		var id uuid.UUID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		groups[id] = append(groups[id], name)
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
