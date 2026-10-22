package monitors

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/secrets"
)

const (
	gateCallConfig    = `{"host":"trunk.example.com","method":"invite","username":"trunk","password":"secret","call":{"number":"+33123456789"}}`
	gateOptionsConfig = `{"host":"trunk.example.com","method":"options"}`
)

// callerContexts are the identities the test-call gate distinguishes. Only a
// tenant admin's session (or a superadmin) may manage a monitor that places
// calls; editors and API keys of any scope may not.
func callerContexts() map[string]struct {
	ctx     context.Context
	allowed bool
} {
	bg := ctxpkg.WithTenantID(context.Background(), uuid.NewString())
	user := ctxpkg.WithAdminID(bg, uuid.NewString())
	return map[string]struct {
		ctx     context.Context
		allowed bool
	}{
		"tenant admin":    {ctxpkg.WithRole(user, "admin"), true},
		"superadmin":      {ctxpkg.WithPlatformRole(user, "superadmin"), true},
		"tenant editor":   {ctxpkg.WithRole(user, "editor"), false},
		"tenant viewer":   {ctxpkg.WithRole(user, "viewer"), false},
		"write API key":   {ctxpkg.WithAuthScope(ctxpkg.WithActorType(ctxpkg.WithAPIKeyID(bg, "k"), ctxpkg.ActorTypeAPIKey), "write"), false},
		"admin-role key":  {ctxpkg.WithRole(ctxpkg.WithAPIKeyID(bg, "k"), "admin"), false},
		"no identity set": {bg, false},
	}
}

func TestCreateMonitor_SIPCallRequiresTenantAdmin(t *testing.T) {
	for name, caller := range callerContexts() {
		t.Run(name, func(t *testing.T) {
			svc := &Service{repo: NewMockRepository(), encryptor: secrets.NoOpEncryptor{}}
			_, err := svc.CreateMonitor(caller.ctx, uuid.New(), &models.CreateMonitorRequest{
				Name: "call", Type: models.MonitorTypeSIP, Config: json.RawMessage(gateCallConfig),
				IntervalSeconds: 60, TimeoutSeconds: 15,
			})
			if caller.allowed && err != nil {
				t.Fatalf("CreateMonitor() = %v, want success", err)
			}
			if !caller.allowed && !errors.Is(err, ErrCallMonitorForbidden) {
				t.Fatalf("CreateMonitor() = %v, want ErrCallMonitorForbidden", err)
			}
		})
	}
}

// The gate is only for calls: an editor still manages OPTIONS monitors.
func TestCreateMonitor_SIPProbeNotGated(t *testing.T) {
	svc := &Service{repo: NewMockRepository(), encryptor: secrets.NoOpEncryptor{}}
	ctx := callerContexts()["tenant editor"].ctx
	if _, err := svc.CreateMonitor(ctx, uuid.New(), &models.CreateMonitorRequest{
		Name: "probe", Type: models.MonitorTypeSIP, Config: json.RawMessage(gateOptionsConfig),
		IntervalSeconds: 60, TimeoutSeconds: 15,
	}); err != nil {
		t.Fatalf("CreateMonitor(options, editor) = %v, want success", err)
	}
}

// Any change to an existing test-call monitor (a rename or a pause included)
// is a tenant admin's, as is any change that would make a monitor one.
func TestUpdateMonitor_SIPCallRequiresTenantAdmin(t *testing.T) {
	for name, caller := range callerContexts() {
		t.Run(name, func(t *testing.T) {
			repo := NewMockRepository()
			svc := &Service{repo: repo, encryptor: secrets.NoOpEncryptor{}}
			tenantID := uuid.New()
			callID, probeID := uuid.New(), uuid.New()
			repo.monitors[callID] = &models.Monitor{ID: callID, TenantID: tenantID, Type: models.MonitorTypeSIP, Config: json.RawMessage(gateCallConfig), IntervalSeconds: 60, TimeoutSeconds: 15, Enabled: true}
			repo.monitors[probeID] = &models.Monitor{ID: probeID, TenantID: tenantID, Type: models.MonitorTypeSIP, Config: json.RawMessage(gateOptionsConfig), IntervalSeconds: 60, TimeoutSeconds: 15, Enabled: true}

			renamed := "renamed"
			_, err := svc.UpdateMonitor(caller.ctx, tenantID, callID, &models.UpdateMonitorRequest{Name: &renamed})
			if caller.allowed && err != nil {
				t.Fatalf("UpdateMonitor(call, rename) = %v, want success", err)
			}
			if !caller.allowed && !errors.Is(err, ErrCallMonitorForbidden) {
				t.Fatalf("UpdateMonitor(call, rename) = %v, want ErrCallMonitorForbidden", err)
			}

			_, err = svc.UpdateMonitor(caller.ctx, tenantID, probeID, &models.UpdateMonitorRequest{Config: json.RawMessage(gateCallConfig)})
			if !caller.allowed && !errors.Is(err, ErrCallMonitorForbidden) {
				t.Fatalf("UpdateMonitor(probe → call) = %v, want ErrCallMonitorForbidden", err)
			}

			_, err = svc.UpdateMonitor(caller.ctx, tenantID, probeID, &models.UpdateMonitorRequest{Name: &renamed})
			if err != nil {
				t.Fatalf("UpdateMonitor(probe, rename) = %v, want success for every writer", err)
			}
		})
	}
}

// A payload that decodes as OPTIONS but carries the call under another key
// spelling (sharedmodels.IsSIPCallConfig) is a call to the gate: refused for
// editors and API keys on create and on a PATCH of an OPTIONS monitor.
func TestSIPCallGate_CaseVariantKeys(t *testing.T) {
	const sneaky = `{"host":"trunk.example.com","method":"invite","call":{"number":"+33123"},"Method":"options","Call":null}`
	for _, name := range []string{"tenant editor", "write API key", "admin-role key"} {
		caller := callerContexts()[name]
		t.Run(name, func(t *testing.T) {
			if err := RequireCallPermission(caller.ctx, models.MonitorTypeSIP, json.RawMessage(sneaky)); !errors.Is(err, ErrCallMonitorForbidden) {
				t.Fatalf("RequireCallPermission() = %v, want ErrCallMonitorForbidden", err)
			}
			repo := NewMockRepository()
			svc := &Service{repo: repo, encryptor: secrets.NoOpEncryptor{}}
			tenantID := uuid.New()
			_, err := svc.CreateMonitor(caller.ctx, tenantID, &models.CreateMonitorRequest{
				Name: "call", Type: models.MonitorTypeSIP, Config: json.RawMessage(sneaky),
				IntervalSeconds: 60, TimeoutSeconds: 15,
			})
			if !errors.Is(err, ErrCallMonitorForbidden) {
				t.Fatalf("CreateMonitor() = %v, want ErrCallMonitorForbidden", err)
			}
			probeID := uuid.New()
			repo.monitors[probeID] = &models.Monitor{ID: probeID, TenantID: tenantID, Type: models.MonitorTypeSIP, Config: json.RawMessage(gateOptionsConfig), IntervalSeconds: 60, TimeoutSeconds: 15, Enabled: true}
			_, err = svc.UpdateMonitor(caller.ctx, tenantID, probeID, &models.UpdateMonitorRequest{Config: json.RawMessage(sneaky)})
			if !errors.Is(err, ErrCallMonitorForbidden) {
				t.Fatalf("UpdateMonitor(probe → sneaky call) = %v, want ErrCallMonitorForbidden", err)
			}
		})
	}
}

// Dependency edges are outside the test-call gate (as on the edge routes):
// an editor's PATCH carrying only depends_on_ids passes the service (the
// handler syncs the edges), while anything touching the monitor's row is
// still refused.
func TestUpdateMonitor_SIPCallDependenciesNotGated(t *testing.T) {
	repo := NewMockRepository()
	svc := &Service{repo: repo, encryptor: secrets.NoOpEncryptor{}}
	tenantID, callID := uuid.New(), uuid.New()
	repo.monitors[callID] = &models.Monitor{ID: callID, TenantID: tenantID, Type: models.MonitorTypeSIP, Config: json.RawMessage(gateCallConfig), IntervalSeconds: 60, TimeoutSeconds: 15, Enabled: true}
	editor := callerContexts()["tenant editor"].ctx

	deps := []string{uuid.NewString()}
	if _, err := svc.UpdateMonitor(editor, tenantID, callID, &models.UpdateMonitorRequest{DependsOnIDs: &deps}); err != nil {
		t.Fatalf("UpdateMonitor(depends_on_ids) = %v, want success for an editor", err)
	}
	paused := false
	if _, err := svc.UpdateMonitor(editor, tenantID, callID, &models.UpdateMonitorRequest{Enabled: &paused}); !errors.Is(err, ErrCallMonitorForbidden) {
		t.Fatalf("UpdateMonitor(enabled) = %v, want ErrCallMonitorForbidden", err)
	}
}

// updateCountingRepo records which monitors reached Repository.Update.
type updateCountingRepo struct {
	*MockRepository
	updated []uuid.UUID
}

func (r *updateCountingRepo) Update(ctx context.Context, monitor *models.Monitor, setParts []string, args []interface{}) error {
	r.updated = append(r.updated, monitor.ID)
	return r.MockRepository.Update(ctx, monitor, setParts, args)
}

// Bulk alerting edits keep the sentinel, so the handler can answer 403, and
// refuse the whole edit before writing anything: a probe listed before the
// call monitor is left as it was.
func TestBulkUpdateAlerting_SIPCallKeepsForbiddenError(t *testing.T) {
	repo := &updateCountingRepo{MockRepository: NewMockRepository()}
	svc := &Service{repo: repo, encryptor: secrets.NoOpEncryptor{}}
	tenantID, probeID, callID := uuid.New(), uuid.New(), uuid.New()
	repo.monitors[probeID] = &models.Monitor{ID: probeID, TenantID: tenantID, Type: models.MonitorTypeSIP, Config: json.RawMessage(gateOptionsConfig), IntervalSeconds: 60, TimeoutSeconds: 15, Enabled: true}
	repo.monitors[callID] = &models.Monitor{ID: callID, TenantID: tenantID, Type: models.MonitorTypeSIP, Config: json.RawMessage(gateCallConfig), IntervalSeconds: 60, TimeoutSeconds: 15, Enabled: true}
	threshold := 3
	editor := callerContexts()["tenant editor"].ctx

	_, err := svc.BulkUpdateAlerting(editor, tenantID, []uuid.UUID{probeID, callID}, &threshold, nil, nil)
	if !errors.Is(err, ErrCallMonitorForbidden) {
		t.Fatalf("BulkUpdateAlerting(probe, call) = %v, want ErrCallMonitorForbidden", err)
	}
	if len(repo.updated) != 0 {
		t.Fatalf("a refused bulk edit wrote monitors %v, want none", repo.updated)
	}

	// Without the call monitor the same editor's edit goes through.
	if n, err := svc.BulkUpdateAlerting(editor, tenantID, []uuid.UUID{probeID}, &threshold, nil, nil); err != nil || n != 1 {
		t.Fatalf("BulkUpdateAlerting(probe) = %d, %v, want 1, nil", n, err)
	}
	if len(repo.updated) != 1 || repo.updated[0] != probeID {
		t.Fatalf("updated = %v, want [%s]", repo.updated, probeID)
	}
}
