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

	"github.com/yassinebenameur/probara/api/internal/middleware"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications/deliverylog"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// Viewers read the history of their own workspace's alerts and channels, and
// nothing of another workspace's.
func TestDeliveryHandlersTenantScoped(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantA := testutil.InsertTenant(ctx, t, db, "deliveries-api-a")
	tenantB := testutil.InsertTenant(ctx, t, db, "deliveries-api-b")
	monitor := testutil.InsertHTTPMonitor(ctx, t, db, tenantA, "api")
	alertID, channelID := uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, failure_count) VALUES ($1, $2, $3, 'availability', 'active', NOW(), 1)`, []any{alertID, tenantA, monitor}},
		{`INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active) VALUES ($1, $2, 'hook', 'webhook', '{}'::jsonb, TRUE)`, []any{channelID, tenantA}},
	} {
		if _, err := db.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []deliverylog.Outcome{deliverylog.OutcomeFailed, deliverylog.OutcomeDelivered, deliverylog.OutcomeDropped} {
		if err := deliverylog.Record(ctx, db, deliverylog.Attempt{
			AlertID: alertID, ChannelID: channelID, EventType: "created", Path: deliverylog.PathAsync, Number: 1,
			Outcome: o, Reason: map[deliverylog.Outcome]deliverylog.Reason{deliverylog.OutcomeDropped: deliverylog.ReasonMuted}[o],
		}); err != nil {
			t.Fatal(err)
		}
	}

	h := NewHandlers(db, logger.New("test", "error"))
	r := chi.NewRouter()
	r.Use(middleware.RequireWrite)
	r.Get("/alerts/{id}/deliveries", h.ListAlertDeliveries)
	r.Get("/alert-channels/{id}/deliveries", h.ListChannelDeliveries)
	get := func(tenant uuid.UUID, path string) (int, deliverylog.Page) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rctx := ctxpkg.WithRole(ctxpkg.WithTenantID(req.Context(), tenant.String()), "viewer")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req.WithContext(rctx))
		var page deliverylog.Page
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
		}
		return rec.Code, page
	}

	if code, page := get(tenantA, "/alerts/"+alertID.String()+"/deliveries"); code != http.StatusOK || page.Total != 3 {
		t.Fatalf("alert deliveries = %d, %d rows; want 200, 3", code, page.Total)
	}
	code, page := get(tenantA, "/alert-channels/"+channelID.String()+"/deliveries?outcome=dropped")
	if code != http.StatusOK || page.Total != 1 || page.Items[0].Reason == nil || *page.Items[0].Reason != deliverylog.ReasonMuted {
		t.Fatalf("filtered channel deliveries = %d, %+v", code, page)
	}
	if code, _ := get(tenantA, "/alert-channels/"+channelID.String()+"/deliveries?outcome=bogus"); code != http.StatusBadRequest {
		t.Fatalf("unknown outcome = %d, want 400", code)
	}
	if code, _ := get(tenantB, "/alerts/"+alertID.String()+"/deliveries"); code != http.StatusNotFound {
		t.Fatalf("another workspace's alert = %d, want 404", code)
	}
	if code, _ := get(tenantB, "/alert-channels/"+channelID.String()+"/deliveries"); code != http.StatusNotFound {
		t.Fatalf("another workspace's channel = %d, want 404", code)
	}
}
