//go:build !unit

package monitors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	testcontainers "github.com/testcontainers/testcontainers-go"

	"github.com/yassinebenameur/probara/api/internal/models"
	depservice "github.com/yassinebenameur/probara/api/internal/services/dependencies"
	groupservice "github.com/yassinebenameur/probara/api/internal/services/groups"
	locationservice "github.com/yassinebenameur/probara/api/internal/services/locations"
	monitorservice "github.com/yassinebenameur/probara/api/internal/services/monitors"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type historyHarness struct {
	t      *testing.T
	ctx    context.Context
	db     *shareddb.Client
	tenant uuid.UUID
	router chi.Router
	as     context.Context
}

func newHistoryHarness(t *testing.T, name string) (*historyHarness, func()) {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	tenant := testutil.InsertTenant(ctx, t, db, name)
	svc := monitorservice.NewService(monitorservice.NewPostgresRepository(db))
	h := NewHandlers(svc, groupservice.NewService(db), nil, logger.New("test", "error"), t.TempDir())
	h.ConfigureDependencies(depservice.NewService(db))
	r := chi.NewRouter()
	r.Route("/monitors", func(r chi.Router) {
		r.Post("/", h.CreateMonitor)
		r.Patch("/{id}", h.UpdateMonitor)
		r.Get("/{id}/versions", h.ListMonitorVersions)
		r.Get("/{id}/versions/{version}", h.GetMonitorVersion)
		r.Get("/{id}/versions/{version}/diff", h.DiffMonitorVersion)
		r.Post("/{id}/versions/{version}/restore", h.RestoreMonitorVersion)
	})
	as := ctxpkg.WithRole(ctxpkg.WithTenantID(ctx, tenant.String()), "admin")
	return &historyHarness{t: t, ctx: ctx, db: db, tenant: tenant, router: r, as: as}, cleanup
}

func (h *historyHarness) call(method, path string, body any, want int) []byte {
	h.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, httptest.NewRequest(method, "/monitors"+path, &buf).WithContext(h.as))
	if w.Code != want {
		h.t.Fatalf("%s %s: status %d, want %d: %s", method, path, w.Code, want, w.Body.String())
	}
	return w.Body.Bytes()
}

func (h *historyHarness) storedConfig(id uuid.UUID) (map[string]any, string) {
	h.t.Helper()
	var raw []byte
	if err := h.db.QueryRowContext(h.ctx, `SELECT config FROM monitors WHERE id = $1`, id).Scan(&raw); err != nil {
		h.t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(raw, &cfg)
	return cfg, string(raw)
}

func (h *historyHarness) versionCount(id uuid.UUID) int {
	h.t.Helper()
	var n int
	if err := h.db.QueryRowContext(h.ctx, `SELECT count(*) FROM monitor_config_versions WHERE monitor_id = $1`, id).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

// A row stored before credentials were moved out of plain headers on every
// save (here also under a miscased key) must not leak through history: a
// save that carries no config records versions holding no credential, in
// the table and in every history response; and a restore of a version that
// does hold one (recorded before snapshots were scrubbed) never writes it
// over the secret stored now.
func TestHistoryNeverHoldsLegacyCredentials_Integration(t *testing.T) {
	h, cleanup := newHistoryHarness(t, "history-legacy-credentials")
	defer cleanup()
	id := testutil.InsertHTTPMonitor(h.ctx, t, h.db, h.tenant, "legacy")
	if _, err := h.db.ExecContext(h.ctx, `UPDATE monitors SET config = $2 WHERE id = $1`, id,
		`{"url":"https://l.example","method":"GET","expected_status":200,"headers":{"Authorization":"Bearer legacy-cred","Accept":"json"},"Headers":{"Cookie":"cookie-cred"}}`); err != nil {
		t.Fatal(err)
	}
	base := "/" + id.String()

	h.call("PATCH", base, map[string]any{"name": "renamed"}, http.StatusOK)
	responses := []string{
		string(h.call("GET", base+"/versions", nil, http.StatusOK)),
		string(h.call("GET", base+"/versions/1", nil, http.StatusOK)),
		string(h.call("GET", base+"/versions/2", nil, http.StatusOK)),
		string(h.call("GET", base+"/versions/2/diff", nil, http.StatusOK)),
		string(h.call("GET", base+"/versions/1/diff?against=current", nil, http.StatusOK)),
	}
	var rows string
	if err := h.db.QueryRowContext(h.ctx, `SELECT string_agg(row_to_json(v)::text, E'\n') FROM monitor_config_versions v WHERE monitor_id = $1`, id).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	for _, text := range append(responses, rows) {
		for _, credential := range []string{"legacy-cred", "cookie-cred"} {
			if strings.Contains(text, credential) {
				t.Fatalf("history exposes %q: %s", credential, text)
			}
		}
	}
	var v1 models.MonitorVersion
	_ = json.Unmarshal([]byte(responses[1]), &v1)
	if v1.Source != monitorservice.VersionSourceBaseline || !strings.Contains(string(v1.Snapshot.Config), `"secret_headers":{"Authorization":"***","Cookie":"***"}`) {
		t.Fatalf("baseline = %s %s", v1.Source, v1.Snapshot.Config)
	}

	// The monitor moves to a secret header; then a version carrying the old
	// plain credential (as one recorded before the scrub would) is restored.
	h.call("PATCH", base, map[string]any{"config": map[string]any{
		"url": "https://l2.example", "method": "GET", "expected_status": 200,
		"secret_headers": map[string]any{"Authorization": "rotated-secret"},
	}}, http.StatusOK)
	if _, err := h.db.ExecContext(h.ctx, `
		UPDATE monitor_config_versions SET snapshot = jsonb_set(snapshot, '{config,headers}', '{"Authorization":"Bearer legacy-cred","Accept":"json"}')
		WHERE monitor_id = $1 AND version = 1`, id); err != nil {
		t.Fatal(err)
	}
	var restored models.RestoreMonitorVersionResponse
	_ = json.Unmarshal(h.call("POST", base+"/versions/1/restore", nil, http.StatusOK), &restored)
	cfg, raw := h.storedConfig(id)
	secretHeaders, _ := cfg["secret_headers"].(map[string]any)
	headers, _ := cfg["headers"].(map[string]any)
	if strings.Contains(raw, "legacy-cred") || secretHeaders["Authorization"] != "rotated-secret" || cfg["url"] != "https://l.example" || headers["Accept"] != "json" {
		t.Fatalf("restore wrote a version's credential or lost the current secret: %s", raw)
	}
	if strings.Join(restored.SecretsToReenter, ",") != "secret_headers.Cookie" {
		t.Fatalf("secrets to re-enter = %v", restored.SecretsToReenter)
	}
}

// A restore is all or nothing: a dependency that would now close a cycle is
// left out and reported while the rest applies; a version that conflicts
// with a secret held now, or needs a secret the monitor no longer holds to
// be valid, is refused with nothing written and no version recorded.
func TestRestoreIsAllOrNothing_Integration(t *testing.T) {
	h, cleanup := newHistoryHarness(t, "history-restore-atomic")
	defer cleanup()
	create := func(body map[string]any) models.Monitor {
		var m models.Monitor
		_ = json.Unmarshal(h.call("POST", "/", body, http.StatusCreated), &m)
		return m
	}
	httpMonitor := func(name string) models.Monitor {
		return create(map[string]any{"name": name, "type": "http", "interval_seconds": 60, "timeout_seconds": 10,
			"config": map[string]any{"url": "https://" + name + ".example", "method": "GET", "expected_status": 200}})
	}

	// Cycle: A depended on B (v2); now B depends on A.
	a, b := httpMonitor("a"), httpMonitor("b")
	h.call("PATCH", "/"+a.ID.String(), map[string]any{"name": "a2", "depends_on_ids": []string{b.ID.String()}}, http.StatusOK)
	h.call("PATCH", "/"+a.ID.String(), map[string]any{"name": "a3", "depends_on_ids": []string{}}, http.StatusOK)
	h.call("PATCH", "/"+b.ID.String(), map[string]any{"depends_on_ids": []string{a.ID.String()}}, http.StatusOK)
	var restored models.RestoreMonitorVersionResponse
	_ = json.Unmarshal(h.call("POST", "/"+a.ID.String()+"/versions/2/restore", nil, http.StatusOK), &restored)
	if strings.Join(restored.DroppedReferences, ",") != "dependency_cycle:"+b.ID.String() || restored.Monitor.Name != "a2" || restored.Version == nil {
		t.Fatalf("cycle restore = %+v", restored)
	}
	var edges int
	_ = h.db.QueryRowContext(h.ctx, `SELECT count(*) FROM monitor_dependencies WHERE monitor_id = $1`, a.ID).Scan(&edges)
	if edges != 0 {
		t.Fatalf("restore created a cycle edge")
	}

	// Conflict with a kept secret: v1 used basic auth, the monitor now has
	// an Authorization secret header.
	c := create(map[string]any{"name": "c", "type": "http", "interval_seconds": 60, "timeout_seconds": 10,
		"config": map[string]any{"url": "https://c.example", "method": "GET", "expected_status": 200, "basic_auth_username": "ops", "basic_auth_password": "pw"}})
	h.call("PATCH", "/"+c.ID.String(), map[string]any{"name": "c2", "config": map[string]any{
		"url": "https://c2.example", "method": "GET", "expected_status": 200, "basic_auth_username": "", "basic_auth_password": "",
		"secret_headers": map[string]any{"Authorization": "Bearer now"}}}, http.StatusOK)
	versions := h.versionCount(c.ID)
	body := string(h.call("POST", "/"+c.ID.String()+"/versions/1/restore", nil, http.StatusBadRequest))
	if !strings.Contains(body, "conflicts with a secret the monitor holds now") {
		t.Fatalf("conflict refusal = %s", body)
	}
	if cfg, _ := h.storedConfig(c.ID); cfg["url"] != "https://c2.example" || h.versionCount(c.ID) != versions {
		t.Fatalf("a refused restore wrote something: %v", cfg)
	}

	// A connection-string version restored onto a monitor that no longer
	// stores one would have neither host nor connection string.
	pg := create(map[string]any{"name": "pg", "type": "postgres", "interval_seconds": 60, "timeout_seconds": 10,
		"config": map[string]any{"connection_string": "postgres://u:p@db.example/app"}})
	h.call("PATCH", "/"+pg.ID.String(), map[string]any{"config": map[string]any{
		"host": "db.example", "port": 5432, "username": "u", "database": "app", "connection_string": ""}}, http.StatusOK)
	versions = h.versionCount(pg.ID)
	body = string(h.call("POST", "/"+pg.ID.String()+"/versions/1/restore", nil, http.StatusBadRequest))
	if !strings.Contains(body, "connection_string") || !strings.Contains(body, "enter them in Settings first") {
		t.Fatalf("missing-secret refusal = %s", body)
	}
	if cfg, _ := h.storedConfig(pg.ID); cfg["host"] != "db.example" || h.versionCount(pg.ID) != versions {
		t.Fatalf("a refused restore wrote something: %v", cfg)
	}
}

// A restore that fails after its first write leaves nothing behind: the
// update it applied and its version roll back together.
func TestRestoreFailingAfterAWriteRollsBack_Integration(t *testing.T) {
	h, cleanup := newHistoryHarness(t, "history-restore-late-failure")
	defer cleanup()
	var a, b models.Monitor
	for _, m := range []*models.Monitor{&a, &b} {
		_ = json.Unmarshal(h.call("POST", "/", map[string]any{"name": uuid.NewString()[:8], "type": "http", "interval_seconds": 60, "timeout_seconds": 10,
			"config": map[string]any{"url": "https://x.example", "method": "GET", "expected_status": 200}}, http.StatusCreated), m)
	}
	base := "/" + a.ID.String()
	h.call("PATCH", base, map[string]any{"name": "renamed", "depends_on_ids": []string{b.ID.String()}}, http.StatusOK)
	h.call("PATCH", base, map[string]any{"name": "renamed again", "depends_on_ids": []string{}}, http.StatusOK)
	versions := h.versionCount(a.ID)
	// The name is written first; the dependency edge then fails.
	if _, err := h.db.ExecContext(h.ctx, `
		CREATE FUNCTION refuse_edges() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'edges refused'; END $$;
		CREATE TRIGGER refuse_edges BEFORE INSERT ON monitor_dependencies FOR EACH ROW EXECUTE FUNCTION refuse_edges();
	`); err != nil {
		t.Fatal(err)
	}
	body := string(h.call("POST", base+"/versions/2/restore", nil, http.StatusInternalServerError))
	if !strings.Contains(body, "nothing changed") {
		t.Fatalf("failure answer = %s", body)
	}
	var name string
	_ = h.db.QueryRowContext(h.ctx, `SELECT name FROM monitors WHERE id = $1`, a.ID).Scan(&name)
	if name != "renamed again" || h.versionCount(a.ID) != versions {
		t.Fatalf("a failed restore kept its write: name %q, versions %d", name, h.versionCount(a.ID))
	}

	// A version of another type is refused before anything is written.
	h.call("PATCH", base, map[string]any{"type": "tcp", "config": map[string]any{"host": "x.example", "port": 443}}, http.StatusOK)
	body = string(h.call("POST", base+"/versions/1/restore", nil, http.StatusBadRequest))
	if !strings.Contains(body, "never changes the type") {
		t.Fatalf("type refusal = %s", body)
	}
}

// A group save naming a member it cannot add fails as a whole: the config,
// the removals and the version are all left as they were.
func TestGroupSaveWithAnUnknownMemberSavesNothing_Integration(t *testing.T) {
	h, cleanup := newHistoryHarness(t, "history-group-members")
	defer cleanup()
	a := testutil.InsertHTTPMonitor(h.ctx, t, h.db, h.tenant, "a")
	b := testutil.InsertHTTPMonitor(h.ctx, t, h.db, h.tenant, "b")
	c := testutil.InsertHTTPMonitor(h.ctx, t, h.db, h.tenant, "c")
	gone := testutil.InsertHTTPMonitor(h.ctx, t, h.db, h.tenant, "gone")
	if _, err := h.db.ExecContext(h.ctx, `UPDATE monitors SET deleted_at = NOW() WHERE id = $1`, gone); err != nil {
		t.Fatal(err)
	}
	var group models.Monitor
	_ = json.Unmarshal(h.call("POST", "/", map[string]any{"name": "edge", "type": "group", "interval_seconds": 60, "timeout_seconds": 10,
		"config": map[string]any{"monitor_ids": []string{a.String(), b.String(), c.String()}}}, http.StatusCreated), &group)
	versions := h.versionCount(group.ID)
	body := string(h.call("PATCH", "/"+group.ID.String(), map[string]any{
		"config": map[string]any{"monitor_ids": []string{a.String(), gone.String()}}}, http.StatusBadRequest))
	if !strings.Contains(body, gone.String()) || !strings.Contains(body, "nothing was saved") {
		t.Fatalf("refusal = %s", body)
	}
	var members int
	_ = h.db.QueryRowContext(h.ctx, `SELECT count(*) FROM monitor_groups WHERE group_id = $1`, group.ID).Scan(&members)
	cfg, _ := h.storedConfig(group.ID)
	if members != 3 || len(cfg["monitor_ids"].([]any)) != 3 || h.versionCount(group.ID) != versions {
		t.Fatalf("a refused group save kept something: %d members, config %v", members, cfg)
	}
}

// A save never needs a second pool connection: with one connection for the
// requests, a create and a first update after the upgrade (which records a
// baseline) succeed.
func TestSavesRunOnOneConnection_Integration(t *testing.T) {
	h, cleanup := newHistoryHarness(t, "history-one-connection")
	defer cleanup()
	legacy := testutil.InsertHTTPMonitor(h.ctx, t, h.db, h.tenant, "legacy")
	// The test database setup keeps connections of its own checked out;
	// the requests below get exactly one.
	h.db.DB.SetMaxOpenConns(h.db.DB.Stats().InUse + 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.call("POST", "/", map[string]any{"name": "fresh", "type": "http", "interval_seconds": 60, "timeout_seconds": 10,
			"config": map[string]any{"url": "https://f.example", "method": "GET", "expected_status": 200}}, http.StatusCreated)
		h.call("PATCH", "/"+legacy.String(), map[string]any{"name": "legacy renamed"}, http.StatusOK)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("a save waited for a second connection")
	}
	h.db.DB.SetMaxOpenConns(25)
	var sources string
	_ = h.db.QueryRowContext(h.ctx, `SELECT string_agg(source, ',' ORDER BY version) FROM monitor_config_versions WHERE monitor_id = $1`, legacy).Scan(&sources)
	if sources != "baseline,ui" && sources != "baseline,api" {
		t.Fatalf("legacy versions = %s", sources)
	}
}

// A PATCH adding a dependency races a location delete that locks both
// monitors: one lock order, so both succeed.
func TestDependencyPatchRacingLocationDelete_Integration(t *testing.T) {
	h, cleanup := newHistoryHarness(t, "history-dependency-race")
	defer cleanup()
	locations := locationservice.NewService(h.db)
	for round := 0; round < 8; round++ {
		m := testutil.InsertHTTPMonitor(h.ctx, t, h.db, h.tenant, fmt.Sprintf("m-%d", round))
		x := testutil.InsertHTTPMonitor(h.ctx, t, h.db, h.tenant, fmt.Sprintf("x-%d", round))
		location, err := locations.Create(h.ctx, h.tenant, &models.CreateLocationRequest{Name: fmt.Sprintf("loc-%d", round)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.db.ExecContext(h.ctx, `INSERT INTO monitor_locations (monitor_id, location_id) VALUES ($1, $3), ($2, $3)`, m, x, location.ID); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var deleteErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			h.call("PATCH", "/"+m.String(), map[string]any{"name": "dep", "depends_on_ids": []string{x.String()}}, http.StatusOK)
		}()
		go func() { defer wg.Done(); _, deleteErr = locations.Delete(h.ctx, h.tenant, location.ID) }()
		wg.Wait()
		if deleteErr != nil {
			t.Fatalf("location delete: %v", deleteErr)
		}
	}
}
