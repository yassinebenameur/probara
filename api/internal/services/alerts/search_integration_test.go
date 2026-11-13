//go:build !unit

package alerts_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	alertsvc "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// The incident attach picker searches with q= instead of reading the newest
// page, so an alert older than any page size stays reachable.
func TestListAlerts_Search(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantID := testutil.InsertTenant(ctx, t, dbc, "search")
	otherTenant := testutil.InsertTenant(ctx, t, dbc, "search-other")
	checkout := testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, "Checkout API")
	billing := testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, "billing_100%")
	noisy := testutil.InsertHTTPMonitor(ctx, t, dbc, tenantID, "noisy")
	foreign := testutil.InsertHTTPMonitor(ctx, t, dbc, otherTenant, "Checkout API")
	now := time.Now().Truncate(time.Second)

	// The checkout alert is older than a full page of newer alerts.
	oldCheckout := insertAlert(ctx, t, dbc, tenantID, checkout, "resolved", now.Add(-30*24*time.Hour))
	for i := 0; i < 105; i++ {
		insertAlert(ctx, t, dbc, tenantID, noisy, "resolved", now.Add(-time.Duration(i)*time.Minute))
	}
	billingAlert := insertAlert(ctx, t, dbc, tenantID, billing, "active", now.Add(-2*time.Hour))
	insertAlert(ctx, t, dbc, otherTenant, foreign, "active", now.Add(-time.Hour))

	paris, frankfurt := uuid.New(), uuid.New()
	for id, name := range map[uuid.UUID]string{paris: "Paris edge", frankfurt: "Frankfurt"} {
		_, err := dbc.ExecContext(ctx, `INSERT INTO locations (id, tenant_id, name, slug) VALUES ($1, $2, $3, $4)`,
			id, tenantID, name, strings.ReplaceAll(strings.ToLower(name), " ", "-"))
		require.NoError(t, err)
	}
	meshAlert := uuid.New()
	_, err := dbc.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, kind, source_location_id, target_location_id,
			status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, NULL, 'mesh_edge', $3, $4, 'active', $5, 1, NOW(), NOW())
	`, meshAlert, tenantID, paris, frankfurt, now.Add(-3*time.Hour))
	require.NoError(t, err)

	svc := alertsvc.NewService(dbc, nil)
	search := func(q string, pageSize int) *models.AlertListResponse {
		t.Helper()
		res, err := svc.ListAlerts(ctx, tenantID, &models.AlertListParams{Query: q, Page: 1, PageSize: pageSize}, alertsvc.ListSort{})
		require.NoError(t, err)
		return res
	}
	ids := func(res *models.AlertListResponse) []uuid.UUID {
		out := make([]uuid.UUID, len(res.Items))
		for i, a := range res.Items {
			out[i] = a.ID
		}
		return out
	}

	t.Run("the newest page alone misses the old alert", func(t *testing.T) {
		res := search("", 100)
		require.Equal(t, 108, res.Total)
		require.NotContains(t, ids(res), oldCheckout)
	})

	t.Run("monitor name, case-insensitive substring, own tenant only", func(t *testing.T) {
		res := search("checkout", 20)
		require.Equal(t, 1, res.Total)
		require.Equal(t, []uuid.UUID{oldCheckout}, ids(res))
	})

	t.Run("LIKE wildcards in the term match literally", func(t *testing.T) {
		require.Equal(t, []uuid.UUID{billingAlert}, ids(search("_100%", 20)))
		require.Equal(t, 0, search("check%api", 20).Total, "a % in the term is no wildcard")
	})

	t.Run("either location of a mesh alert", func(t *testing.T) {
		require.Equal(t, []uuid.UUID{meshAlert}, ids(search("paris", 20)))
		require.Equal(t, []uuid.UUID{meshAlert}, ids(search("FRANKFURT", 20)))
	})

	t.Run("exact alert id", func(t *testing.T) {
		require.Equal(t, []uuid.UUID{oldCheckout}, ids(search(strings.ToUpper(oldCheckout.String()), 20)))
	})

	t.Run("search pages like the full list", func(t *testing.T) {
		first, err := svc.ListAlerts(ctx, tenantID, &models.AlertListParams{Query: "nois", Page: 1, PageSize: 100}, alertsvc.ListSort{})
		require.NoError(t, err)
		second, err := svc.ListAlerts(ctx, tenantID, &models.AlertListParams{Query: "nois", Page: 2, PageSize: 100}, alertsvc.ListSort{})
		require.NoError(t, err)
		require.Equal(t, 105, first.Total)
		require.Len(t, first.Items, 100)
		require.Len(t, second.Items, 5)
	})
}
