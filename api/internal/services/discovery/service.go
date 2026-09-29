package discovery

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/yassinebenameur/probara/shared/alertrouting"
	"github.com/yassinebenameur/probara/shared/db"
	contract "github.com/yassinebenameur/probara/shared/discovery"
	"github.com/yassinebenameur/probara/shared/monitorstate"
)

var ErrNotFound = errors.New("discovery resource not found")
var ErrConflict = errors.New("inventory changed; refresh and try again")

type Invalid struct{ Message string }

func (e Invalid) Error() string { return e.Message }
func invalid(s string) error    { return Invalid{s} }

type Service struct{ db *db.Client }

func New(database *db.Client) *Service { return &Service{db: database} }

type Integration struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Namespaces []string   `json:"namespaces"`
	LocationID *uuid.UUID `json:"location_id"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	Revision   int64      `json:"revision"`
	Token      string     `json:"token,omitempty"`
}
type CreateRequest struct {
	Name       string     `json:"name"`
	Namespaces []string   `json:"namespaces"`
	LocationID *uuid.UUID `json:"location_id"`
}

func tokenPair() (string, string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", "", e
	}
	t := base64.RawURLEncoding.EncodeToString(b)
	return t, hash(t), nil
}
func hash(t string) string { s := sha256.Sum256([]byte(t)); return hex.EncodeToString(s[:]) }
func (s *Service) Create(ctx context.Context, tenant uuid.UUID, req CreateRequest) (*Integration, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 100 || len(req.Namespaces) == 0 || len(req.Namespaces) > 20 {
		return nil, invalid("name and 1–20 namespaces are required")
	}
	seen := map[string]bool{}
	for _, ns := range req.Namespaces {
		if !contract.ValidNamespace(ns) || seen[ns] {
			return nil, invalid("invalid or duplicate namespace")
		}
		seen[ns] = true
	}
	token, digest, err := tokenPair()
	if err != nil {
		return nil, err
	}
	out := &Integration{ID: uuid.New(), Name: req.Name, Namespaces: req.Namespaces, LocationID: req.LocationID, Token: token}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Serialize the per-tenant limit with all integration creates. An advisory
	// lock, not a tenants row lock: FOR UPDATE on tenants would block every
	// concurrent FK insert that references the tenant (monitors, channels…).
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "discovery-integrations:"+tenant.String()); err != nil {
		return nil, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM discovery_integrations WHERE tenant_id=$1`, tenant).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 20 {
		return nil, invalid("at most 20 discovery integrations per workspace")
	}
	if req.LocationID != nil {
		var ok bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM locations WHERE id=$1 AND tenant_id=$2 AND enabled AND deleted_at IS NULL)`, req.LocationID, tenant).Scan(&ok)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, invalid("choose an enabled location in this workspace")
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO discovery_integrations(id,tenant_id,name,namespaces,location_id,token_hash) VALUES($1,$2,$3,$4,$5,$6)`, out.ID, tenant, out.Name, pq.Array(out.Namespaces), out.LocationID, digest)
	if err != nil {
		var pe *pq.Error
		if errors.As(err, &pe) && pe.Code == "23505" {
			return nil, invalid("an integration with this name already exists")
		}
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *Service) List(ctx context.Context, tenant uuid.UUID) ([]Integration, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,namespaces,location_id,last_seen_at,revision FROM discovery_integrations WHERE tenant_id=$1 ORDER BY name`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Integration{}
	for rows.Next() {
		var i Integration
		if err = rows.Scan(&i.ID, &i.Name, pq.Array(&i.Namespaces), &i.LocationID, &i.LastSeenAt, &i.Revision); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
func (s *Service) Rotate(ctx context.Context, tenant, id uuid.UUID) (string, error) {
	token, digest, err := tokenPair()
	if err != nil {
		return "", err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE discovery_integrations SET token_hash=$3,revision=revision+1 WHERE tenant_id=$1 AND id=$2`, tenant, id, digest)
	if err != nil {
		return "", err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return "", ErrNotFound
	}
	return token, nil
}
func (s *Service) Delete(ctx context.Context, tenant, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM discovery_integrations WHERE tenant_id=$1 AND id=$2`, tenant, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Service) CollectorConfig(ctx context.Context, id uuid.UUID, token string) (contract.Config, error) {
	var cfg contract.Config
	err := s.db.QueryRowContext(ctx, `SELECT namespaces,revision FROM discovery_integrations WHERE id=$1 AND token_hash=$2`, id, hash(token)).Scan(pq.Array(&cfg.Namespaces), &cfg.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, ErrNotFound
	}
	return cfg, err
}
func (s *Service) Snapshot(ctx context.Context, id uuid.UUID, token string, snapshot contract.Snapshot) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var tenant uuid.UUID
	var namespaces []string
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT tenant_id,namespaces,revision FROM discovery_integrations WHERE id=$1 AND token_hash=$2 FOR UPDATE`, id, hash(token)).Scan(&tenant, pq.Array(&namespaces), &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if snapshot.Revision != revision {
		return ErrConflict
	}
	if err = snapshot.Validate(namespaces); err != nil {
		return invalid(err.Error())
	}
	// Only a complete valid snapshot can mark resources missing. A failed scan
	// leaves inventory intact; staleness is computed from last_seen_at instead.
	// Reconciliation is three set-based statements, not one round trip per
	// resource: it runs under the integration row lock, which approvals and
	// links wait on, and inside the collector route's request timeout.
	_, err = tx.ExecContext(ctx, `UPDATE discovered_resources SET missing_since=COALESCE(missing_since,NOW()) WHERE integration_id=$1`, id)
	if err != nil {
		return err
	}
	type serviceRow struct {
		ID         uuid.UUID `json:"id"`
		ServiceKey string    `json:"service_key"`
		Name       string    `json:"name"`
		Namespace  string    `json:"namespace"`
	}
	type resourceRow struct {
		UID         string                `json:"uid"`
		ServiceKey  string                `json:"service_key"`
		Kind        string                `json:"kind"`
		Name        string                `json:"name"`
		Namespace   string                `json:"namespace"`
		Suggestions []contract.Suggestion `json:"suggestions"`
	}
	services := []serviceRow{}
	byKey := map[string]bool{}
	resources := make([]resourceRow, 0, len(snapshot.Resources))
	for _, r := range snapshot.Resources {
		key := r.ServiceKey()
		if !byKey[key] {
			byKey[key] = true
			name := r.Service
			if name == "" {
				name = r.Name
			}
			services = append(services, serviceRow{ID: uuid.New(), ServiceKey: key, Name: name, Namespace: r.Namespace})
		}
		resources = append(resources, resourceRow{UID: r.UID, ServiceKey: key, Kind: r.Kind, Name: r.Name, Namespace: r.Namespace, Suggestions: r.Suggestions})
	}
	servicesJSON, err := json.Marshal(services)
	if err != nil {
		return err
	}
	resourcesJSON, err := json.Marshal(resources)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO discovered_services(id,tenant_id,integration_id,service_key,name,namespace)
 SELECT x.id,$2,$1,x.service_key,x.name,x.namespace FROM jsonb_to_recordset($3::jsonb) AS x(id uuid,service_key text,name text,namespace text)
 ON CONFLICT(integration_id,service_key) DO UPDATE SET name=EXCLUDED.name`, id, tenant, string(servicesJSON))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO discovered_resources(integration_id,uid,service_id,kind,name,namespace,suggestions)
 SELECT $1,x.uid,s.id,x.kind,x.name,x.namespace,x.suggestions FROM jsonb_to_recordset($2::jsonb) AS x(uid text,service_key text,kind text,name text,namespace text,suggestions jsonb)
 JOIN discovered_services s ON s.integration_id=$1 AND s.service_key=x.service_key
 ON CONFLICT(integration_id,uid) DO UPDATE SET service_id=EXCLUDED.service_id,kind=EXCLUDED.kind,name=EXCLUDED.name,namespace=EXCLUDED.namespace,suggestions=EXCLUDED.suggestions,last_seen_at=NOW(),missing_since=NULL`, id, string(resourcesJSON))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE discovery_integrations SET revision=revision+1,last_seen_at=NOW() WHERE id=$1`, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

type Resource struct {
	UID          string                `json:"uid"`
	Kind         string                `json:"kind"`
	Name         string                `json:"name"`
	MissingSince *time.Time            `json:"missing_since"`
	Suggestions  []contract.Suggestion `json:"suggestions"`
}
type Monitor struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	Type          string    `json:"type"`
	Enabled       bool      `json:"enabled"`
	State         string    `json:"state"`
	Fresh         bool      `json:"fresh"`
	Routed        bool      `json:"routed"`
	SuggestionKey *string   `json:"suggestion_key"`
}
type Coverage struct {
	ID         uuid.UUID            `json:"id"`
	Name       string               `json:"name"`
	Namespace  string               `json:"namespace"`
	Resources  []Resource           `json:"resources"`
	Monitors   []Monitor            `json:"monitors"`
	Gaps       []string             `json:"gaps"`
	Candidates map[string][]Monitor `json:"candidates"`
}

// Summary counts every service of the integration, before filtering.
type Summary struct {
	Services    int `json:"services"`
	Covered     int `json:"covered"`
	WithGaps    int `json:"with_gaps"`
	Unmonitored int `json:"unmonitored"`
	Missing     int `json:"missing"`
}
type Overview struct {
	LastSeenAt     *time.Time `json:"last_seen_at"`
	Items          []Coverage `json:"items"`
	Total          int        `json:"total"`
	Page           int        `json:"page"`
	PageSize       int        `json:"page_size"`
	InventoryStale bool       `json:"inventory_stale"`
	Summary        Summary    `json:"summary"`
}

const PageSize = 50

// OverviewQuery filters and orders across every service of the integration,
// not only the returned page.
type OverviewQuery struct {
	Page     int
	Query    string
	GapsOnly bool
	SortGaps bool
}

// Overview assesses coverage for the whole integration in a fixed number of
// queries (no per-service round trips; the UI polls it), then returns one
// page. Only the page's resources carry suggestions and candidates.
func (s *Service) Overview(ctx context.Context, tenant, integration uuid.UUID, q OverviewQuery) (Overview, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	out := Overview{Items: []Coverage{}, Page: q.Page, PageSize: PageSize}
	var seen *time.Time
	err := s.db.QueryRowContext(ctx, `SELECT last_seen_at FROM discovery_integrations WHERE tenant_id=$1 AND id=$2`, tenant, integration).Scan(&seen)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.LastSeenAt = seen
	out.InventoryStale = seen == nil || time.Since(*seen) > 15*time.Minute

	// Presence per service, with the missing-since of each absent resource
	// folded into the two facts coverageGaps needs.
	rows, err := s.db.QueryContext(ctx, `SELECT s.id,s.name,s.namespace,
   COALESCE(bool_or(r.missing_since IS NULL),false),
   COALESCE(bool_or(r.missing_since>NOW()-interval '15 minutes'),false)
   FROM discovered_services s LEFT JOIN discovered_resources r ON r.service_id=s.id
   WHERE s.tenant_id=$1 AND s.integration_id=$2 GROUP BY s.id`, tenant, integration)
	if err != nil {
		return out, err
	}
	all := []Coverage{}
	type presence struct{ present, recentMissing bool }
	presenceOf := map[uuid.UUID]presence{}
	index := map[uuid.UUID]int{}
	for rows.Next() {
		var c Coverage
		var p presence
		if err = rows.Scan(&c.ID, &c.Name, &c.Namespace, &p.present, &p.recentMissing); err != nil {
			rows.Close()
			return out, err
		}
		c.Resources, c.Monitors, c.Candidates = []Resource{}, []Monitor{}, map[string][]Monitor{}
		presenceOf[c.ID] = p
		index[c.ID] = len(all)
		all = append(all, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}

	// Freshness is S-F1 through monitorstate: platform receipt time
	// (last_result_at, aged by the DB clock) against the per-type window.
	// Push config is read only for its grace period and never returned.
	mr, err := s.db.QueryContext(ctx, `SELECT l.service_id,m.id,m.name,m.type,m.enabled,m.current_state,m.interval_seconds,
   CASE WHEN m.type='push' THEN m.config END,
   EXTRACT(EPOCH FROM NOW()-m.last_result_at),
   NOT (`+alertrouting.UnreachablePredicate("m")+`),l.suggestion_key
   FROM discovery_monitor_links l
   JOIN discovered_services s ON s.id=l.service_id AND s.tenant_id=l.tenant_id
   JOIN monitors m ON m.id=l.monitor_id AND m.tenant_id=l.tenant_id AND m.deleted_at IS NULL
   WHERE l.tenant_id=$1 AND s.integration_id=$2 ORDER BY m.name,m.id`, tenant, integration)
	if err != nil {
		return out, err
	}
	for mr.Next() {
		var serviceID uuid.UUID
		var m Monitor
		var interval int
		var config []byte
		var age sql.NullFloat64
		if err = mr.Scan(&serviceID, &m.ID, &m.Name, &m.Type, &m.Enabled, &m.State, &interval, &config, &age, &m.Routed, &m.SuggestionKey); err != nil {
			mr.Close()
			return out, err
		}
		m.Fresh = age.Valid && age.Float64 < float64(monitorstate.EvidenceWindowSeconds(m.Type, interval, config))
		if i, ok := index[serviceID]; ok {
			all[i].Monitors = append(all[i].Monitors, m)
		}
	}
	err = mr.Err()
	mr.Close()
	if err != nil {
		return out, err
	}

	needle := strings.ToLower(strings.TrimSpace(q.Query))
	matched := []Coverage{}
	for i := range all {
		c := &all[i]
		p := presenceOf[c.ID]
		c.Gaps = gapsFor(p.present, p.recentMissing, c.Monitors, out.InventoryStale)
		out.Summary.Services++
		if len(c.Gaps) == 0 {
			out.Summary.Covered++
		} else {
			out.Summary.WithGaps++
		}
		if slices.Contains(c.Gaps, "no_enabled_monitor") {
			out.Summary.Unmonitored++
		}
		if slices.Contains(c.Gaps, "resource_missing") || slices.Contains(c.Gaps, "resource_missing_grace") {
			out.Summary.Missing++
		}
		if q.GapsOnly && len(c.Gaps) == 0 {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(c.Name), needle) && !strings.Contains(strings.ToLower(c.Namespace), needle) {
			continue
		}
		matched = append(matched, *c)
	}
	slices.SortFunc(matched, func(a, b Coverage) int {
		if q.SortGaps && len(a.Gaps) != len(b.Gaps) {
			return len(b.Gaps) - len(a.Gaps)
		}
		if a.Namespace != b.Namespace {
			return strings.Compare(a.Namespace, b.Namespace)
		}
		if a.Name != b.Name {
			return strings.Compare(a.Name, b.Name)
		}
		return strings.Compare(a.ID.String(), b.ID.String())
	})
	out.Total = len(matched)
	from := min((q.Page-1)*PageSize, len(matched))
	out.Items = matched[from:min(from+PageSize, len(matched))]
	if len(out.Items) == 0 {
		return out, nil
	}

	ids := make([]uuid.UUID, len(out.Items))
	pageIndex := map[uuid.UUID]int{}
	for i, c := range out.Items {
		ids[i] = c.ID
		pageIndex[c.ID] = i
	}
	rr, err := s.db.QueryContext(ctx, `SELECT service_id,uid,kind,name,missing_since,suggestions FROM discovered_resources WHERE integration_id=$1 AND service_id=ANY($2) ORDER BY kind,name`, integration, pq.Array(ids))
	if err != nil {
		return out, err
	}
	for rr.Next() {
		var serviceID uuid.UUID
		var r Resource
		var data []byte
		if err = rr.Scan(&serviceID, &r.UID, &r.Kind, &r.Name, &r.MissingSince, &data); err != nil {
			rr.Close()
			return out, err
		}
		if err = json.Unmarshal(data, &r.Suggestions); err != nil {
			rr.Close()
			return out, err
		}
		c := &out.Items[pageIndex[serviceID]]
		c.Resources = append(c.Resources, r)
	}
	err = rr.Err()
	rr.Close()
	if err != nil {
		return out, err
	}

	// Target matching is a suggestion only, never an automatic link. Never
	// return monitor config or credentials from this inventory endpoint.
	suggestions := []contract.Suggestion{}
	keys := map[string]bool{}
	for _, c := range out.Items {
		for _, r := range c.Resources {
			if r.MissingSince != nil {
				continue
			}
			for _, p := range r.Suggestions {
				if !keys[p.Key] {
					suggestions = append(suggestions, p)
					keys[p.Key] = true
				}
			}
		}
	}
	candidates, err := s.candidates(ctx, tenant, suggestions)
	if err != nil {
		return out, err
	}
	for i := range out.Items {
		c := &out.Items[i]
		for _, r := range c.Resources {
			if r.MissingSince != nil {
				continue
			}
			for _, p := range r.Suggestions {
				if m, ok := candidates[p.Key]; ok {
					c.Candidates[p.Key] = m
				}
			}
		}
	}
	return out, nil
}

func coverageGaps(c Coverage, stale bool) []string {
	present, recentMissing := false, false
	for _, r := range c.Resources {
		if r.MissingSince == nil {
			present = true
		} else if time.Since(*r.MissingSince) < 15*time.Minute {
			recentMissing = true
		}
	}
	return gapsFor(present, recentMissing, c.Monitors, stale)
}
func gapsFor(present, recentMissing bool, monitors []Monitor, stale bool) []string {
	gaps := []string{}
	if stale {
		gaps = append(gaps, "inventory_stale")
	}
	if !present {
		if recentMissing {
			gaps = append(gaps, "resource_missing_grace")
		} else {
			gaps = append(gaps, "resource_missing")
		}
	}
	active := 0
	for _, m := range monitors {
		if !m.Enabled {
			continue
		}
		active++
		if m.Type == "group" {
			if !slices.Contains(gaps, "freshness_unavailable") {
				gaps = append(gaps, "freshness_unavailable")
			}
		} else if !m.Fresh && !slices.Contains(gaps, "stale_checks") {
			gaps = append(gaps, "stale_checks")
		}
		if !m.Routed && !slices.Contains(gaps, "unrouted") {
			gaps = append(gaps, "unrouted")
		}
	}
	if active == 0 {
		gaps = append(gaps, "no_enabled_monitor")
	}
	return gaps
}

// targetForms lists the lower-cased spellings an existing monitor may use for
// the same suggested target, so candidate matching and approval duplicate
// prevention agree: an HTTP URL with or without the root slash and with or
// without its default port, and a cluster Service name with or without the
// .cluster.local search suffix. Compared against lower(config->>'url'|'host'),
// so path case is folded too — acceptable for a suggestion/refusal that
// always leaves the operator the explicit link path.
func targetForms(s contract.Suggestion) []string {
	hosts := func(h string) []string {
		h = strings.ToLower(h)
		if strings.HasSuffix(h, ".svc") {
			return []string{h, h + ".cluster.local"}
		}
		return []string{h}
	}
	if s.Type != "http" {
		return hosts(s.Target)
	}
	u, err := url.Parse(s.Target)
	if err != nil {
		return []string{strings.ToLower(s.Target)}
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	defaultPort := map[string]string{"http": "80", "https": "443"}[scheme]
	ports := []string{port}
	if port == "" || port == defaultPort {
		ports = []string{"", defaultPort}
	}
	paths := []string{u.EscapedPath()}
	if paths[0] == "/" || paths[0] == "" {
		paths = []string{"/", ""}
	}
	forms := []string{}
	for _, h := range hosts(u.Hostname()) {
		for _, p := range ports {
			hostport := h
			if p != "" {
				hostport = h + ":" + p
			}
			for _, path := range paths {
				forms = append(forms, strings.ToLower(scheme+"://"+hostport+path))
			}
		}
	}
	return forms
}

// targetMatchSQL matches monitor m against a suggestion type ($type), its
// targetForms ($forms, text[]) and TCP port ($port, text).
func targetMatchSQL(typ, forms, port string) string {
	return `m.type=` + typ + ` AND ((` + typ + `='http' AND lower(m.config->>'url')=ANY(` + forms + `)) OR (` + typ + `='tcp' AND lower(m.config->>'host')=ANY(` + forms + `) AND m.config->>'port'=` + port + `))`
}

func (s *Service) candidates(ctx context.Context, tenant uuid.UUID, suggestions []contract.Suggestion) (map[string][]Monitor, error) {
	out := map[string][]Monitor{}
	if len(suggestions) == 0 {
		return out, nil
	}
	type probe struct {
		Key   string   `json:"key"`
		Type  string   `json:"type"`
		Forms []string `json:"forms"`
		Port  string   `json:"port"`
	}
	probes := make([]probe, len(suggestions))
	for i, p := range suggestions {
		probes[i] = probe{Key: p.Key, Type: p.Type, Forms: targetForms(p), Port: strconv.Itoa(p.Port)}
	}
	data, err := json.Marshal(probes)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.key,m.id,m.name,m.type,m.enabled,m.current_state
 FROM jsonb_to_recordset($2::jsonb) AS p(key text,type text,forms text[],port text)
 CROSS JOIN LATERAL (SELECT m.id,m.name,m.type,m.enabled,m.current_state FROM monitors m
 WHERE m.tenant_id=$1 AND m.deleted_at IS NULL AND `+targetMatchSQL("p.type", "p.forms", "p.port")+`
 ORDER BY m.name,m.id LIMIT 10) m`, tenant, string(data))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var m Monitor
		if err = rows.Scan(&key, &m.ID, &m.Name, &m.Type, &m.Enabled, &m.State); err != nil {
			return nil, err
		}
		out[key] = append(out[key], m)
	}
	return out, rows.Err()
}
