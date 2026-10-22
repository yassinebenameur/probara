package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yassinebenameur/probara/shared/monitorstate"

	_ "github.com/lib/pq"
)

// world holds everything created so far, by catalog key.
type world struct {
	o          options
	env        Env
	startedAt  time.Time // local clock when seed started
	now        time.Time
	start      time.Time
	rawStart   time.Time
	specs      []Spec
	byKey      map[string]*Spec
	ids        map[string]string // spec key -> monitor id
	tokens     map[string]string // push key -> token
	agentIDs   map[string]string
	locIDs     map[string]string
	locCreds   map[string]string
	chans      map[string]string // channel key -> id
	users      map[string]string // username -> id
	pages      map[string]string // page key -> id
	slaIDs     map[string]string
	mw         []maintWindow
	events     map[string]time.Time
	hits       []hit
	alerts     map[string][]alertRef // event -> alerts
	openAlerts map[string]string     // monitor key -> open alert id
	sims       map[string]*simResult
}

type alertRef struct {
	id, monitorKey string
	triggered      time.Time
	resolved       *time.Time
}

type maintWindow struct {
	id, title, desc string
	start, end      time.Time
	keys            []string
}

func runSeed(ctx context.Context, o options) error {
	t0 := time.Now()
	pool, err := pgx.Connect(ctx, o.dbURL)
	if err != nil {
		return err
	}
	defer pool.Close(context.Background())
	sqldb, err := sql.Open("postgres", o.dbURL)
	if err != nil {
		return err
	}
	defer sqldb.Close()

	w := &world{o: o, env: Env{Base: o.mockPort}, startedAt: t0, ids: map[string]string{}, tokens: map[string]string{}, agentIDs: map[string]string{},
		locIDs: map[string]string{}, locCreds: map[string]string{}, chans: map[string]string{}, users: map[string]string{},
		pages: map[string]string{}, slaIDs: map[string]string{}, alerts: map[string][]alertRef{}, openAlerts: map[string]string{}, sims: map[string]*simResult{}}
	if err := pool.QueryRow(ctx, "SELECT now()").Scan(&w.now); err != nil {
		return err
	}
	w.now = w.now.UTC()
	w.start = w.now.Truncate(24*time.Hour).AddDate(0, 0, -o.historyDays)
	w.rawStart = w.now.Truncate(time.Hour).Add(-time.Duration(o.rawHours) * time.Hour)
	w.specs = catalog(w.env)
	specDefaults(w.specs)
	w.byKey = byKey(w.specs)
	if err := checkCatalog(w.specs); err != nil {
		return err
	}

	logf("tearing down any previous %s tenant", TenantName)
	if err := teardown(ctx, pool); err != nil {
		return err
	}
	logf("creating tenant %s (%s)", TenantName, TenantID)
	if _, err := pool.Exec(ctx, `INSERT INTO tenants (id, name, data_retention_days, created_at, updated_at) VALUES ($1, $2, 90, $3, $3)`, TenantID, TenantName, w.start.Add(-48*time.Hour)); err != nil {
		return fmt.Errorf("insert tenant: %w", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO tenant_memberships (admin_user_id, tenant_id, role) VALUES ($1, $2, 'admin') ON CONFLICT DO NOTHING`, AdminUserID, TenantID); err != nil {
		logf("warn: admin membership: %v", err)
	}

	c := newAPIClient(o.apiURL, o.user, o.pass)
	if err := c.login(ctx); err != nil {
		return err
	}
	steps := []struct {
		name string
		fn   func(context.Context, *apiClient, *pgx.Conn) error
	}{
		{"tenant settings", w.seedSettings},
		{"alert channels", w.seedChannels},
		{"locations", w.seedLocations},
		{"users + API keys", w.seedUsers},
		{"monitors", w.seedMonitors},
		{"groups", w.seedGroups},
		{"dependencies", w.seedDependencies},
		{"history", w.seedHistory},
		{"status pages", w.seedStatusPages},
		{"incidents", w.seedIncidents},
		{"SLAs + reports", w.seedSLAs},
		{"mesh + agent metrics", func(ctx context.Context, c *apiClient, p *pgx.Conn) error { return w.seedMeshAndMetrics(ctx, p, sqldb) }},
		{"audit trail", w.seedAudit},
	}
	for _, s := range steps {
		st := time.Now()
		if err := s.fn(ctx, c, pool); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
		logf("✓ %-22s %s", s.name, time.Since(st).Round(time.Millisecond))
	}
	logf("done in %s — tenant %s; open http://localhost:3000 and switch to %q", time.Since(t0).Round(time.Second), TenantID, TenantName)
	logf("keep it live: go run ./scripts/seed-demo targets (or make seed-demo, which starts it in the background)")
	return nil
}

// pausedRawWindow is how much raw history a paused monitor keeps before
// its pause (see seedHistory).
const pausedRawWindow = 24 * time.Hour

func checkCatalog(specs []Spec) error {
	names := map[string]bool{}
	keys := map[string]bool{}
	// Raw check_results are pruned after 30 days; a pause older than that
	// would leave the monitor with no raw rows, i.e. "Never" checked.
	maxPausedAgo := time.Duration(monitorstate.CheckResultsRawRetentionDays-3)*24*time.Hour - pausedRawWindow
	for _, s := range specs {
		if names[s.Name] || keys[s.Key] {
			return fmt.Errorf("duplicate catalog entry %q / %q", s.Key, s.Name)
		}
		if s.PausedAgo > maxPausedAgo {
			return fmt.Errorf("%s: PausedAgo %s is past the raw retention (max %s)", s.Key, s.PausedAgo, maxPausedAgo)
		}
		names[s.Name], keys[s.Key] = true, true
	}
	for _, s := range specs {
		for _, d := range append(append([]string{}, s.DependsOn...), s.Members...) {
			if !keys[d] {
				return fmt.Errorf("%s references unknown key %q", s.Key, d)
			}
		}
	}
	return nil
}

func runTeardown(ctx context.Context, o options) error {
	pool, err := pgx.Connect(ctx, o.dbURL)
	if err != nil {
		return err
	}
	defer pool.Close(context.Background())
	return teardown(ctx, pool)
}

// teardown deletes the Northwind tenant (everything tenant-scoped cascades),
// its audit rows (no FK) and the seeded users (platform-scoped).
func teardown(ctx context.Context, pool *pgx.Conn) error {
	var name string
	err := pool.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, TenantID).Scan(&name)
	if err == nil && name != TenantName {
		return fmt.Errorf("tenant %s is named %q, not %q — refusing to delete it", TenantID, name, TenantName)
	}
	stmts := []string{
		`DELETE FROM audit_log WHERE tenant_id = '` + TenantID + `'`,
		`DELETE FROM admin_users WHERE email ILIKE '%@` + UserEmailDomain + `'`,
		// Delete the big tables first, in their own statements, so the
		// tenant cascade stays quick.
		`DELETE FROM check_results WHERE tenant_id = '` + TenantID + `'`,
		`DELETE FROM monitor_hourly_rollups WHERE tenant_id = '` + TenantID + `'`,
		`DELETE FROM metric_series WHERE tenant_id = '` + TenantID + `'`,
		`DELETE FROM tenants WHERE id = '` + TenantID + `'`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("teardown: %s: %w", s, err)
		}
	}
	return nil
}

// ── settings, channels, locations, users ─────────────────────────────────

func (w *world) seedSettings(ctx context.Context, c *apiClient, _ *pgx.Conn) error {
	return c.do(ctx, "PATCH", "/api/v1/tenant-settings", map[string]any{"data_retention_days": 90, "dashboard_group_tags": teams}, nil)
}

func (w *world) seedChannels(ctx context.Context, c *apiClient, pool *pgx.Conn) error {
	type ch struct {
		key, name, typ string
		cfg            map[string]any
		active         bool
	}
	// Webhooks point at private addresses: the notification egress guard
	// refuses them as a permanent error, so dispatch is real but silent.
	chs := []ch{
		{"ops-bot", "Ops incident bot (webhook)", "generic_webhook", map[string]any{"url": "https://10.20.0.15/hooks/probara"}, true},
		{"escalation", "SRE escalation (webhook)", "generic_webhook", map[string]any{"url": "https://10.20.0.16/escalate"}, true},
		{"data-email", "Data team email", "email", map[string]any{"to": []string{"data-oncall@northwind.example"}}, false},
		{"voice-email", "Voice NOC email", "email", map[string]any{"to": []string{"voice-noc@northwind.example", "telecoms@northwind.example"}}, false},
	}
	for _, x := range chs {
		var r idResp
		if err := c.do(ctx, "POST", "/api/v1/alert-channels", map[string]any{"name": x.name, "type": x.typ, "config": x.cfg, "is_active": x.active}, &r); err != nil {
			return err
		}
		w.chans[x.key] = r.ID
	}
	if err := c.do(ctx, "PUT", "/api/v1/notification-settings", map[string]any{
		"default_channels":                     []map[string]any{{"channel_id": w.chans["ops-bot"], "delay_seconds": 0}, {"channel_id": w.chans["escalation"], "delay_seconds": 900}},
		"alert_reminder_seconds":               3600,
		"auto_create_incident":                 false,
		"latency_anomaly_enabled":              true,
		"latency_anomaly_sensitivity":          4.0,
		"latency_anomaly_min_delta_pct":        50,
		"dependency_suppression_enabled":       true,
		"dependency_suppression_grace_seconds": 120,
	}, nil); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, `UPDATE alert_channels SET created_at = $2, updated_at = $2 WHERE tenant_id = $1`, TenantID, w.start.Add(-24*time.Hour))
	return err
}

func newCredential() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (w *world) seedLocations(ctx context.Context, c *apiClient, pool *pgx.Conn) error {
	for _, l := range locationCatalog() {
		body := map[string]any{"name": l.Name, "description": l.Description}
		if l.MeshOff != 0 {
			body["mesh_endpoint"] = l.meshEndpoint(w.env)
		}
		var r idResp
		if err := c.do(ctx, "POST", "/api/v1/locations", body, &r); err != nil {
			return err
		}
		w.locIDs[l.Key] = r.ID
		// A plaintext credential (secrets.Decrypt passes non-envelopes
		// through) lets `targets` start the location worker without the
		// platform key or PUBLIC_NATS_URL.
		cred := newCredential()
		w.locCreds[l.Key] = cred
		var lastSeen any
		if l.LastSeenAgo > 0 {
			lastSeen = w.now.Add(-l.LastSeenAgo)
		}
		if _, err := pool.Exec(ctx, `UPDATE locations SET worker_credential = $2, enabled = $3, last_seen_at = COALESCE($4, last_seen_at), created_at = $5 WHERE id = $1`,
			r.ID, cred, l.Enabled, lastSeen, w.start.Add(-20*24*time.Hour)); err != nil {
			return err
		}
	}
	return nil
}

var seedUsers = []struct{ user, email, role string }{
	{"maya.chen", "maya.chen@northwind.example", "admin"},
	{"lucas.martin", "lucas.martin@northwind.example", "editor"},
	{"priya.raman", "priya.raman@northwind.example", "editor"},
	{"tom.okafor", "tom.okafor@northwind.example", "viewer"},
	{"elena.rossi", "elena.rossi@northwind.example", "viewer"},
}

func (w *world) seedUsers(ctx context.Context, c *apiClient, pool *pgx.Conn) error {
	for i, u := range seedUsers {
		var r idResp
		if err := c.do(ctx, "POST", "/api/v1/users", map[string]any{"username": u.user, "email": u.email, "password": "Northwind-Demo-2026!",
			"platform_role": "member", "memberships": []map[string]any{{"tenant_id": TenantID, "role": u.role}}}, &r); err != nil {
			return err
		}
		w.users[u.user] = r.ID
		created := w.start.Add(time.Duration(i*9) * 24 * time.Hour)
		var lastLogin any = w.now.Add(-time.Duration(1+i*19) * time.Hour)
		var disabled any
		if u.user == "elena.rossi" {
			lastLogin = w.now.Add(-26 * 24 * time.Hour)
			disabled = w.now.Add(-21 * 24 * time.Hour)
		}
		if _, err := pool.Exec(ctx, `UPDATE admin_users SET created_at = $2, last_login_at = $3, disabled_at = $4 WHERE id = $1`, r.ID, created, lastLogin, disabled); err != nil {
			return err
		}
	}

	keys := []struct {
		name, scope string
		created     time.Duration
		used        time.Duration
		expires     *time.Duration
		after       string
	}{
		{"otel-collector (hosts)", "write", 60 * 24 * time.Hour, 0, nil, ""},
		{"terraform-ci", "write", 95 * 24 * time.Hour, 3 * time.Hour, durPtr(180 * 24 * time.Hour), ""},
		{"grafana-readonly", "read", 80 * 24 * time.Hour, 6 * time.Minute, nil, ""},
		{"jenkins-legacy", "write", 104 * 24 * time.Hour, 33 * 24 * time.Hour, durPtr(30 * 24 * time.Hour), "expire"},
		{"backup-script (old laptop)", "write", 70 * 24 * time.Hour, 40 * 24 * time.Hour, nil, "revoke"},
	}
	for _, k := range keys {
		body := map[string]any{"name": k.name, "scope": k.scope}
		if k.expires != nil {
			body["expires_at"] = w.now.Add(*k.expires).Format(time.RFC3339)
		}
		var r idResp
		if err := c.do(ctx, "POST", "/api/v1/api-keys", body, &r); err != nil {
			return err
		}
		if k.name == "otel-collector (hosts)" {
			if err := os.WriteFile(w.o.keyFile, []byte(r.Key+"\n"), 0o600); err != nil {
				return err
			}
		}
		var used any
		if k.used > 0 {
			used = w.now.Add(-k.used)
		}
		if _, err := pool.Exec(ctx, `UPDATE api_keys SET created_at = $2, last_used_at = $3, created_by = $4 WHERE id = $1`, r.ID, w.now.Add(-k.created), used, w.users["maya.chen"]); err != nil {
			return err
		}
		switch k.after {
		case "expire":
			if _, err := pool.Exec(ctx, `UPDATE api_keys SET expires_at = $2 WHERE id = $1`, r.ID, w.now.Add(-2*24*time.Hour)); err != nil {
				return err
			}
		case "revoke":
			if err := c.do(ctx, "DELETE", "/api/v1/api-keys/"+r.ID, nil, nil); err != nil {
				return err
			}
			if _, err := pool.Exec(ctx, `UPDATE api_keys SET revoked_at = $2 WHERE id = $1`, r.ID, w.now.Add(-12*24*time.Hour)); err != nil {
				return err
			}
		}
	}
	return nil
}

func durPtr(d time.Duration) *time.Duration { return &d }

// ── monitors ─────────────────────────────────────────────────────────────

func (w *world) monitorConfig(s *Spec) map[string]any {
	e := w.env
	switch s.Type {
	case "http":
		cfg := map[string]any{"url": s.monitorURL(e), "method": "GET"}
		if s.Keyword != "" {
			cfg["body_assertions"] = []any{map[string]any{"op": "contains", "value": s.Keyword}}
		}
		if s.JSON != nil && s.Profile != External {
			cfg["json_assertions"] = []any{map[string]any{"path": "status", "op": "equals", "value": "ok"}}
		}
		if s.Key == "vault" {
			cfg["json_assertions"] = []any{map[string]any{"path": "status", "op": "equals", "value": "ok"}, map[string]any{"path": "sealed", "op": "bool_is", "value": "false"}}
		}
		if s.Key == "registry" {
			cfg["expected_statuses"] = []int{200, 401}
		}
		if len(s.Display) > 0 {
			var df []any
			for _, d := range s.Display {
				df = append(df, map[string]any{"path": d[0], "label": d[1]})
			}
			cfg["display_fields"] = df
		}
		if s.Profile == External {
			cfg["tls_min_days_valid"] = 14
			cfg["collect_timing"] = true
		}
		return cfg
	case "websocket":
		return map[string]any{"url": fmt.Sprintf("ws://%s:%d%s", e.fqdn(s.Host), e.port(offHTTP), s.Path), "send_message": "ping", "expected_substring": "pong",
			"warn_latency_ms": 150, "max_latency_ms": 3000}
	case "grpc":
		return map[string]any{"host": e.fqdn(s.Host), "port": e.port(offGRPC), "service": s.Service, "use_tls": false}
	case "tcp":
		if s.Config != nil {
			return s.Config
		}
		return map[string]any{"host": e.fqdn(s.Host), "port": e.port(s.Port)}
	case "sip":
		// A hostname (not a loopback literal) also stays clear of the
		// worker's SIP loopback→host.docker.internal rewrite.
		return map[string]any{"host": e.fqdn(s.Host), "port": e.port(s.Port), "transport": "udp", "method": "options"}
	case "agent":
		cfg := map[string]any{"expected_interval_seconds": s.Interval}
		for k, v := range s.Config {
			cfg[k] = v
		}
		return cfg
	}
	return s.Config
}

func (w *world) seedMonitors(ctx context.Context, c *apiClient, _ *pgx.Conn) error {
	for i := range w.specs {
		s := &w.specs[i]
		if s.Type == "group" {
			continue
		}
		body := map[string]any{
			"name": s.Name, "type": s.Type, "config": w.monitorConfig(s),
			"interval_seconds": s.Interval, "timeout_seconds": s.Timeout, "enabled": false,
			"tags": append([]string{s.Team}, s.Tags...), "consecutive_failures_threshold": s.Threshold,
		}
		switch s.Notify {
		case "custom-disabled":
			ch := w.chans["data-email"]
			if s.Team == "voice" {
				ch = w.chans["voice-email"]
			}
			body["notification_mode"] = "custom"
			body["notification_channels"] = []map[string]any{{"channel_id": ch, "delay_seconds": 0}}
		case "custom-empty":
			body["notification_mode"] = "custom"
		}
		if len(s.Locations) > 0 {
			var ids []string
			for _, l := range s.Locations {
				ids = append(ids, w.locIDs[l])
			}
			body["location_ids"] = ids
			body["location_quorum"] = s.Quorum
		}
		if s.Key == "returns" || s.Key == "carrier-dhl" {
			body["dependency_suppression"] = "off"
		}
		var r idResp
		if err := c.do(ctx, "POST", "/api/v1/monitors", body, &r); err != nil {
			return fmt.Errorf("%s: %w", s.Key, err)
		}
		w.ids[s.Key] = r.ID
		if r.PushToken != nil {
			w.tokens[s.Key] = *r.PushToken
		}
		if r.AgentID != nil {
			w.agentIDs[s.Key] = *r.AgentID
		}
	}
	return nil
}

func (w *world) seedGroups(ctx context.Context, c *apiClient, pool *pgx.Conn) error {
	for i := range w.specs {
		s := &w.specs[i]
		if s.Type != "group" {
			continue
		}
		var members []string
		for _, m := range s.Members {
			members = append(members, w.ids[m])
		}
		body := map[string]any{"name": s.Name, "type": "group", "config": map[string]any{"monitor_ids": members},
			"interval_seconds": 60, "timeout_seconds": 0, "tags": append([]string{s.Team}, s.Tags...)}
		if s.Rollup != "" {
			body["member_alert_rollup"] = s.Rollup
		}
		var r idResp
		if err := c.do(ctx, "POST", "/api/v1/monitors", body, &r); err != nil {
			return fmt.Errorf("%s: %w", s.Key, err)
		}
		w.ids[s.Key] = r.ID
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM monitor_groups WHERE group_id = $1`, r.ID).Scan(&n); err != nil {
			return err
		}
		if n != len(members) {
			return fmt.Errorf("group %s: monitor_groups has %d rows, want %d", s.Key, n, len(members))
		}
	}
	return nil
}

func (w *world) seedDependencies(ctx context.Context, c *apiClient, _ *pgx.Conn) error {
	for i := range w.specs {
		s := &w.specs[i]
		for _, d := range s.DependsOn {
			if err := c.do(ctx, "POST", "/api/v1/monitors/"+w.ids[s.Key]+"/dependencies", map[string]any{"depends_on_id": w.ids[d]}, nil); err != nil {
				return fmt.Errorf("%s -> %s: %w", s.Key, d, err)
			}
		}
	}
	return nil
}

// ── scripted timeline ────────────────────────────────────────────────────

// at is a UTC wall-clock time `daysAgo` days before today.
func (w *world) at(daysAgo, hh, mm int) time.Time {
	return w.now.Truncate(24*time.Hour).AddDate(0, 0, -daysAgo).Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute)
}

func (w *world) planTimeline() {
	m := time.Minute
	w.events = map[string]time.Time{
		"orders-db-failover": w.at(21, 14, 12),
		"search-timeouts":    w.at(48, 9, 30),
		"sso-outage":         w.at(63, 22, 5),
		"dhl-tracking":       w.at(9, 11, 0),
		"trunk-b-flap":       w.at(34, 3, 10),
		"psp-timeouts":       w.pspEventStart(),
		"mw-orders-db":       w.at(40, 2, 0),
		"mw-leeds":           w.at(26, 21, 0),
		"mw-cdn":             w.at(70, 1, 0),
	}
	dbErr := pgTimeoutErr(w.env, w.byKey["orders-db"])
	w.hits = []hit{
		{Key: "orders-db", Off: 0, Dur: 47 * m, Status: "error", Err: dbErr, Event: "orders-db-failover"},
		{Key: "checkout-api", Off: 1 * m, Dur: 47 * m, Status: "failure", HTTP: 503, Event: "orders-db-failover"},
		{Key: "orders-api", Off: 1 * m, Dur: 46 * m, Status: "failure", HTTP: 502, Event: "orders-db-failover"},
		{Key: "checkout-web", Off: 2 * m, Dur: 46 * m, Status: "failure", HTTP: 502, Event: "orders-db-failover"},
		{Key: "order-history", Off: 2 * m, Dur: 45 * m, Status: "failure", HTTP: 502, Event: "orders-db-failover"},
		{Key: "checkout-journey", Off: 1 * m, Dur: 47 * m, Status: "failure", Event: "orders-db-failover"},
		{Key: "geo-checkout", Off: 2 * m, Dur: 45 * m, Status: "failure", HTTP: 502, Event: "orders-db-failover"},

		{Key: "search-api", Off: 0, Dur: 18 * m, Status: "failure", HTTP: 504, Event: "search-timeouts"},
		{Key: "search-suggest", Off: 1 * m, Dur: 17 * m, Status: "failure", HTTP: 504, Event: "search-timeouts"},
		{Key: "sf-search", Off: 2 * m, Dur: 15 * m, Status: "failure", HTTP: 502, Event: "search-timeouts"},
		{Key: "geo-search", Off: 1 * m, Dur: 16 * m, Status: "failure", HTTP: 504, Event: "search-timeouts"},
		{Key: "search-node-2", Off: -2 * m, Dur: 22 * m, Status: "error", Event: "search-timeouts"},

		{Key: "identity-api", Off: 0, Dur: 12 * m, Status: "failure", HTTP: 503, Event: "sso-outage"},
		{Key: "login-page", Off: 1 * m, Dur: 11 * m, Status: "failure", HTTP: 503, Event: "sso-outage"},
		{Key: "oauth-token", Off: 0, Dur: 12 * m, Status: "failure", HTTP: 503, Event: "sso-outage"},
		{Key: "login-journey", Off: 1 * m, Dur: 11 * m, Status: "failure", Event: "sso-outage"},
		{Key: "geo-login", Off: 1 * m, Dur: 10 * m, Status: "failure", HTTP: 503, Event: "sso-outage"},
		{Key: "mfa-api", Off: 2 * m, Dur: 9 * m, Status: "failure", HTTP: 503, Event: "sso-outage"},

		{Key: "carrier-dhl", Off: 0, Dur: 25 * m, Status: "failure", HTTP: 504, Event: "dhl-tracking"},
		{Key: "tracking-api", Off: 3 * m, Dur: 15 * m, Status: "failure", HTTP: 503, Event: "dhl-tracking"},

		{Key: "sip-trunk-b", Off: 0, Dur: 9 * m, Status: "error", Event: "trunk-b-flap"},

		{Key: "payment-auth", Off: 0, Dur: w.pspOutage(), Status: "error", Event: "psp-timeouts"},

		// planned (inside maintenance windows)
		{Key: "orders-db", Off: 12 * m, Dur: 19 * m, Status: "error", Planned: true, Event: "mw-orders-db"},
		{Key: "orders-api", Off: 13 * m, Dur: 17 * m, Status: "failure", HTTP: 503, Planned: true, Event: "mw-orders-db"},
		{Key: "wms-leeds", Off: 40 * m, Dur: 25 * m, Status: "error", Planned: true, Event: "mw-leeds"},
		{Key: "label-svc", Off: 41 * m, Dur: 22 * m, Status: "failure", HTTP: 502, Planned: true, Event: "mw-leeds"},
		{Key: "sf-cdn", Off: 20 * m, Dur: 6 * m, Status: "failure", HTTP: 503, Planned: true, Event: "mw-cdn"},
	}
	w.mw = []maintWindow{
		{title: "Orders DB minor upgrade (PostgreSQL 16.4)", desc: "Rolling minor upgrade of the orders cluster; short write unavailability expected during switchover.",
			start: w.events["mw-orders-db"], end: w.events["mw-orders-db"].Add(time.Hour), keys: []string{"orders-db", "orders-db-replica", "orders-api"}},
		{title: "Leeds DC network cutover", desc: "Core switch replacement at the Leeds distribution centre. WMS and label printing offline for up to 30 minutes.",
			start: w.events["mw-leeds"], end: w.events["mw-leeds"].Add(2 * time.Hour), keys: []string{"g-warehouse"}},
		{title: "IdP signing key rotation", desc: "Rotate the OIDC signing key; JWKS publishes both keys for 24h.",
			start: w.at(12, 5, 0), end: w.at(12, 5, 30), keys: []string{"identity-api", "oidc-discovery", "jwks"}},
		{title: "Storefront CDN migration", desc: "Move static assets and the image resizer to the new CDN tenant.",
			start: w.events["mw-cdn"], end: w.events["mw-cdn"].Add(3 * time.Hour), keys: []string{"sf-cdn", "sf-images"}},
		{title: "Search cluster reindex (v9 mapping)", desc: "Full reindex onto the v9 mapping. The indexer admin API is unavailable while shards relocate; search stays up.",
			start: w.now.Add(-35 * m), end: w.now.Add(85 * m), keys: []string{"search-indexer", "search-node-1", "search-node-2", "search-node-3", "search-sync"}},
		{title: "PSP TLS certificate rotation", desc: "Rotate the mTLS client certificate used towards Adyen.",
			start: w.at(-2, 3, 0), end: w.at(-2, 3, 30), keys: []string{"payment-auth", "payments-3ds", "payments-webhooks"}},
		{title: "Colt SIP trunk carrier maintenance", desc: "Carrier-side maintenance on trunk A (Colt LON). Calls fail over to trunk B.",
			start: w.at(-6, 1, 0), end: w.at(-6, 4, 0), keys: []string{"sip-trunk-a"}},
		{title: "Kafka 3.8 rolling upgrade", desc: "Rolling broker restarts, one at a time.",
			start: w.at(-12, 22, 0), end: w.at(-12, 23, 30), keys: []string{"kafka-1", "kafka-2", "kafka-3"}},
	}
}

// The at-risk payments SLA needs its October downtime to eat ~85% of the
// budget accrued so far, so size the PSP outage from the elapsed month.
func (w *world) monthStart() time.Time {
	return time.Date(w.now.Year(), w.now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func (w *world) pspEventStart() time.Time {
	t := w.at(5, 19, 42)
	if ms := w.monthStart().Add(2 * time.Hour); t.Before(ms) {
		t = ms
	}
	return t
}

func (w *world) pspOutage() time.Duration {
	elapsed := w.now.Sub(w.monthStart()).Seconds()
	allowed := elapsed * (1 - paymentsSLATarget/100)
	downSecs := 0.85 * allowed
	// payment-auth checks every 30s with threshold 2: n failing checks give
	// (n-1)*30s of down.
	n := int(downSecs/30+0.5) + 1
	if n < 3 {
		n = 3
	}
	return time.Duration(n)*30*time.Second - 5*time.Second
}

const paymentsSLATarget = 99.9

// windowsFor collects one monitor's failure windows.
func (w *world) windowsFor(s *Spec) []window {
	var quiet [][2]time.Time
	for _, mw := range w.mw {
		quiet = append(quiet, [2]time.Time{mw.start, mw.end})
	}
	return planWindows(s, w.hits, w.events, w.start, w.now, quiet)
}

// maintCovers reports whether a maintenance window covering t targets the
// monitor (directly or through a group it belongs to).
func (w *world) maintCovers(key string, from, to time.Time) bool {
	for _, mw := range w.mw {
		if !(from.Before(mw.end) && to.After(mw.start)) {
			continue
		}
		for _, k := range mw.keys {
			if k == key {
				return true
			}
			if g := w.byKey[k]; g != nil && g.Type == "group" {
				for _, m := range g.Members {
					if m == key {
						return true
					}
				}
			}
		}
	}
	return false
}

// ── history (one transaction) ────────────────────────────────────────────

func (w *world) seedHistory(ctx context.Context, _ *apiClient, pool *pgx.Conn) error {
	w.planTimeline()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM monitor_state_intervals WHERE tenant_id = $1`, TenantID); err != nil {
		return err
	}
	for i := range w.mw {
		mw := &w.mw[i]
		mw.id = uuid.NewString()
		if _, err := tx.Exec(ctx, `INSERT INTO maintenance_windows (id, tenant_id, title, description, starts_at, ends_at, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$7)`, mw.id, TenantID, mw.title, mw.desc, mw.start, mw.end, minTime(mw.start.Add(-5*24*time.Hour), w.now.Add(-2*time.Hour))); err != nil {
			return err
		}
		for _, k := range mw.keys {
			if _, err := tx.Exec(ctx, `INSERT INTO maintenance_window_monitors (maintenance_window_id, monitor_id) VALUES ($1,$2)`, mw.id, w.ids[k]); err != nil {
				return err
			}
		}
	}

	mods := w.planMods()
	var activeMW *maintWindow
	for i := range w.mw {
		if w.mw[i].start.Before(w.now) && w.mw[i].end.After(w.now) {
			activeMW = &w.mw[i]
		}
	}

	// Generating and writing ~110 days takes about a minute, during which
	// the wall clock moves on. Pass A writes the bulk up to the planning
	// clock; pass B re-runs the (deterministic) simulation up to the real
	// clock just before commit and writes only the tail plus everything that
	// must be current: intervals, location state, the monitor row. Without
	// it, push/agent and 30s monitors would cross their watchdog deadline the
	// moment they are enabled.
	split := w.now.Add(-7 * time.Second)
	inputs := map[string]simInput{}
	// The mock serves one latency profile per path (and per SIP port): the
	// first spec that claims it (the same rule targets uses).
	routeOwner := map[string]*Spec{}
	for i := range w.specs {
		if k := w.specs[i].routeKey(); k != "" && routeOwner[k] == nil {
			routeOwner[k] = &w.specs[i]
		}
	}
	totalRaw := 0
	var simTime time.Duration
	for i := range w.specs {
		s := &w.specs[i]
		if s.Type == "group" {
			continue
		}
		in := simInput{spec: s, env: w.env, start: w.start, now: w.now, end: w.now, rawStart: w.rawStart, windows: w.windowsFor(s),
			sinSince: w.at(3, 6, 40), mods: mods}
		if s.Profile == MaintDown && activeMW != nil {
			in.maintDown = activeMW.start.Add(4 * time.Minute)
		}
		if s.Key == "recs-api" {
			in.pauses = [][2]time.Time{{w.at(17, 10, 4), w.at(17, 12, 21)}}
		}
		if k := s.routeKey(); k != "" {
			in.latSpec = routeOwner[k]
		}
		if s.Profile == Paused && s.PausedAgo > 0 {
			// The monitors list draws its trace and "last checked" from raw
			// rows, so a paused monitor also gets raw rows for its last
			// hours before the pause. The window starts on an hour boundary
			// and nothing follows it, so those hours' rollups still equal
			// the rebuild from raw.
			in.rawStart = minTime(w.rawStart, w.now.Add(-s.PausedAgo).Truncate(time.Hour).Add(-pausedRawWindow))
		}
		inputs[s.Key] = in
		st := time.Now()
		res := simulate(in)
		simTime += time.Since(st)
		n, err := w.writeBulk(ctx, tx, s, res, split)
		if err != nil {
			return fmt.Errorf("%s: %w", s.Key, err)
		}
		totalRaw += n
	}
	var end time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&end); err != nil {
		return err
	}
	tailStart := time.Now()
	for i := range w.specs {
		s := &w.specs[i]
		if s.Type == "group" {
			continue
		}
		in := inputs[s.Key]
		in.end = end
		res := simulate(in)
		w.sims[s.Key] = res
		n, err := w.writeTail(ctx, tx, s, res, split, end)
		if err != nil {
			return fmt.Errorf("%s: %w", s.Key, err)
		}
		totalRaw += n
	}
	logf("  simulation %s per pass; tail pass took %s", simTime.Round(time.Millisecond), time.Since(tailStart).Round(time.Millisecond))
	logf("  wrote %d raw check rows (%s .. now)", totalRaw, w.rawStart.Format("Jan 2 15:04"))

	// Daily rollups are the SUM of hourly ones (what the rebuild derives).
	if _, err := tx.Exec(ctx, `
		INSERT INTO monitor_daily_rollups (tenant_id, monitor_id, bucket_day, total_checks, success_checks, error_checks,
			latency_success_sum_ms, latency_success_count, latest_status, latest_check_at, created_at, updated_at)
		SELECT tenant_id, monitor_id, (bucket_hour AT TIME ZONE 'UTC')::date, SUM(total_checks), SUM(success_checks), SUM(error_checks),
			SUM(latency_success_sum_ms), SUM(latency_success_count),
			(ARRAY_AGG(latest_status ORDER BY latest_check_at DESC NULLS LAST))[1], MAX(latest_check_at), NOW(), NOW()
		FROM monitor_hourly_rollups WHERE tenant_id = $1 GROUP BY 1,2,3`, TenantID); err != nil {
		return fmt.Errorf("daily rollups: %w", err)
	}

	if err := w.writeAlerts(ctx, tx); err != nil {
		return fmt.Errorf("alerts: %w", err)
	}

	// Groups: backdate; the alerter derives their state.
	for i := range w.specs {
		s := &w.specs[i]
		if s.Type == "group" {
			if _, err := tx.Exec(ctx, `UPDATE monitors SET created_at = $2, updated_at = $3 WHERE id = $1`, w.ids[s.Key], w.start, w.now); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	w.pokeTargets()
	return nil
}

// pokeTargets asks a running `targets` to reload the tenant now (new push
// tokens, agent IDs, maintenance windows, location credentials).
func (w *world) pokeTargets() {
	b, err := os.ReadFile(w.o.pidFile)
	if err != nil {
		logf("  targets not running (no %s): start it to keep the world live", w.o.pidFile)
		return
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if p, err := os.FindProcess(pid); err == nil && pid > 0 && p.Signal(syscall.SIGHUP) == nil {
		logf("  signalled targets (pid %d) to reload", pid)
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (w *world) rawCopy(ctx context.Context, tx pgx.Tx, s *Spec, res *simResult, keep func(time.Time) bool) (int, error) {
	mid := uuid.MustParse(w.ids[s.Key])
	tid := uuid.MustParse(TenantID)
	locUUIDs := make([]uuid.UUID, len(s.Locations))
	for i, l := range s.Locations {
		locUUIDs[i] = uuid.MustParse(w.locIDs[l])
	}
	rawRows := make([][]any, 0, len(res.raw))
	for _, r := range res.raw {
		if !keep(r.t) {
			continue
		}
		var httpS, lat, errMsg, metrics, loc any
		if r.http != 0 {
			httpS = int32(r.http)
		}
		if r.latency >= 0 {
			lat = int32(r.latency)
		}
		if r.errMsg != "" {
			errMsg = r.errMsg
		}
		if r.metrics != nil {
			metrics = string(r.metrics)
		}
		if r.loc >= 0 {
			loc = locUUIDs[r.loc]
		}
		done := r.t
		if r.latency > 0 {
			done = r.t.Add(time.Duration(r.latency) * time.Millisecond)
		}
		rawRows = append(rawRows, []any{uuid.New(), mid, tid, uuid.New(), r.status, httpS, lat, errMsg, false, r.t, r.t, done, metrics, "monitor", loc, done.Add(15 * time.Millisecond)})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"check_results"},
		[]string{"id", "monitor_id", "tenant_id", "job_id", "status", "http_status", "latency_ms", "error_message", "matched_body_substring",
			"created_at", "started_at", "completed_at", "metrics_data", "result_source", "location_id", "ingested_at"},
		pgx.CopyFromRows(rawRows)); err != nil {
		return 0, fmt.Errorf("check_results: %w", err)
	}
	return len(rawRows), nil
}

// hourlyCopy writes the hourly rollups: synthetic below rawStart, and
// exactly what the rebuild would derive from the raw rows above it.
func (w *world) hourlyCopy(ctx context.Context, tx pgx.Tx, s *Spec, res *simResult, keep func(time.Time) bool) error {
	mid := uuid.MustParse(w.ids[s.Key])
	tid := uuid.MustParse(TenantID)
	hours := make([]time.Time, 0, len(res.hours))
	for h := range res.hours {
		if keep(h) {
			hours = append(hours, h)
		}
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i].Before(hours[j]) })
	hRows := make([][]any, 0, len(hours))
	for _, h := range hours {
		a := res.hours[h]
		hRows = append(hRows, []any{tid, mid, h, int32(a.total), int32(a.success), int32(a.errors), a.latSum, int32(a.latCount), a.latestStatus, a.latestAt, w.now, w.now})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"monitor_hourly_rollups"},
		[]string{"tenant_id", "monitor_id", "bucket_hour", "total_checks", "success_checks", "error_checks", "latency_success_sum_ms",
			"latency_success_count", "latest_status", "latest_check_at", "created_at", "updated_at"}, pgx.CopyFromRows(hRows)); err != nil {
		return fmt.Errorf("hourly: %w", err)
	}
	return nil
}

// writeBulk (pass A): raw rows before split and hourly rollups of the hours
// that end before split's hour.
func (w *world) writeBulk(ctx context.Context, tx pgx.Tx, s *Spec, res *simResult, split time.Time) (int, error) {
	n, err := w.rawCopy(ctx, tx, s, res, func(t time.Time) bool { return t.Before(split) })
	if err != nil {
		return 0, err
	}
	splitHour := split.Truncate(time.Hour)
	return n, w.hourlyCopy(ctx, tx, s, res, func(h time.Time) bool { return h.Before(splitHour) })
}

// writeTail (pass B): the rest of the raw rows and hours, the whole interval
// timeline, per-location state and the monitor row, all as of `end`.
func (w *world) writeTail(ctx context.Context, tx pgx.Tx, s *Spec, res *simResult, split, end time.Time) (int, error) {
	id := w.ids[s.Key]
	mid := uuid.MustParse(id)
	tid := uuid.MustParse(TenantID)
	n, err := w.rawCopy(ctx, tx, s, res, func(t time.Time) bool { return !t.Before(split) })
	if err != nil {
		return 0, err
	}
	splitHour := split.Truncate(time.Hour)
	if err := w.hourlyCopy(ctx, tx, s, res, func(h time.Time) bool { return !h.Before(splitHour) }); err != nil {
		return 0, err
	}

	ivRows := make([][]any, 0, len(res.intervals))
	for _, iv := range res.intervals {
		var e any
		if iv.end != nil {
			e = *iv.end
		}
		ivRows = append(ivRows, []any{tid, mid, iv.state, iv.reason, iv.start, e})
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"monitor_state_intervals"}, []string{"tenant_id", "monitor_id", "state", "reason", "started_at", "ended_at"}, pgx.CopyFromRows(ivRows)); err != nil {
		return 0, fmt.Errorf("intervals: %w", err)
	}
	locUUIDs := make([]uuid.UUID, len(s.Locations))
	for i, l := range s.Locations {
		locUUIDs[i] = uuid.MustParse(w.locIDs[l])
	}

	// per-location state
	for i, lf := range res.locs {
		if lf.lastAt.IsZero() {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO monitor_location_state (monitor_id, location_id, tenant_id, current_state, consecutive_failures, last_latency_ms,
				last_check_at, last_state_change_at, last_result_started_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$7,$7)
			ON CONFLICT (monitor_id, location_id) DO UPDATE SET current_state=EXCLUDED.current_state, consecutive_failures=EXCLUDED.consecutive_failures,
				last_latency_ms=EXCLUDED.last_latency_ms, last_check_at=EXCLUDED.last_check_at, last_state_change_at=EXCLUDED.last_state_change_at,
				last_result_started_at=EXCLUDED.last_result_started_at, updated_at=EXCLUDED.updated_at`,
			id, locUUIDs[i], TenantID, string(lf.state), lf.cf, lf.lastLatency, lf.lastAt, lf.since); err != nil {
			return 0, fmt.Errorf("location state: %w", err)
		}
	}

	// the monitor row: open interval == current_state, evidence watermark at
	// the newest seeded row, due now.
	enabled := s.Profile != Paused
	// The first live dispatch continues the seeded cadence: one interval
	// after the newest seeded check. Dispatching at `end` instead put a live
	// check seconds after the last seeded one, and that extra sample (on a
	// cold DNS cache and fresh worker connections) tripped latency-anomaly
	// alerts on fast monitors right after every seed. The seeded phases are
	// random, so the monitors still come due spread out.
	nextRun := end.Add(time.Duration(rngFor(s.Key, 6).IntN(min(s.Interval, 30))) * time.Second)
	if !res.lastAt.IsZero() {
		if due := res.lastAt.Add(time.Duration(s.Interval) * time.Second); due.After(end) {
			nextRun = due
		}
	}
	var lastAt any
	if !res.lastAt.IsZero() {
		lastAt = res.lastAt
	}
	if _, err := tx.Exec(ctx, `UPDATE monitors SET enabled = $2, created_at = $3, updated_at = $4, current_state = $5, consecutive_failures = $6,
			last_state_change_at = $7, last_result_at = $8, last_result_started_at = $8, next_run_at = $9
		WHERE id = $1`, id, enabled, w.start, end, string(res.state), res.cf, res.stateSince, lastAt, nextRun); err != nil {
		return 0, err
	}
	return n, nil
}
