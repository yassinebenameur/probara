//go:build !unit

package deliveries

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
	"github.com/yassinebenameur/probara/shared/testutil"
)

func seed(ctx context.Context, t *testing.T, db *shareddb.Client, tenantID uuid.UUID) (alertID, channelID uuid.UUID) {
	t.Helper()
	monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, "API")
	alertID, channelID = uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'ops', 'webhook', '{}'::jsonb, TRUE, NOW(), NOW())
	`, channelID, tenantID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, alert_policy_id, kind, status,
			triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, 'availability', 'active', NOW(), 3, NOW(), NOW())
	`, alertID, tenantID, monitorID); err != nil {
		t.Fatalf("insert alert: %v", err)
	}
	if err := deliverylog.Record(ctx, db, deliverylog.Entry{
		AlertID: alertID, ChannelID: channelID, EventType: "created", DeliveryKey: "created",
		Path: deliverylog.PathSync, Outcome: deliverylog.OutcomeDelivered,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	return alertID, channelID
}

// Each workspace reads its own alerts' and channels' deliveries; another
// workspace's IDs answer 404, not an empty list.
func TestDeliveryRoutesAreTenantScoped(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()

	tenantA := testutil.InsertTenant(ctx, t, db, "a")
	tenantB := testutil.InsertTenant(ctx, t, db, "b")
	alertA, channelA := seed(ctx, t, db, tenantA)

	h := NewHandlers(db, logger.New("test", "error"))
	r := chi.NewRouter()
	r.Get("/alerts/{id}/deliveries", h.ListAlertDeliveries)
	r.Get("/alert-channels/{id}/deliveries", h.ListChannelDeliveries)
	get := func(tenant uuid.UUID, path string) (int, deliverylog.Page) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(ctxpkg.WithTenantID(req.Context(), tenant.String()))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var page deliverylog.Page
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
		}
		return rec.Code, page
	}

	for _, path := range []string{
		"/alerts/" + alertA.String() + "/deliveries",
		"/alert-channels/" + channelA.String() + "/deliveries",
	} {
		code, page := get(tenantA, path)
		if code != http.StatusOK || page.Total != 1 || len(page.Items) != 1 ||
			page.Items[0].Outcome != deliverylog.OutcomeDelivered {
			t.Errorf("owner GET %s = %d %+v, want one delivered row", path, code, page)
		}
		if code, _ := get(tenantB, path); code != http.StatusNotFound {
			t.Errorf("other workspace GET %s = %d, want 404", path, code)
		}
	}
	if code, _ := get(tenantA, "/alerts/not-a-uuid/deliveries"); code != http.StatusBadRequest {
		t.Errorf("bad id = %d, want 400", code)
	}
}
