//go:build !unit

package alertactions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/shared/actionlink"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// The link acknowledges through the real alerts service: the alert row ends
// up exactly as the Alerts page leaves it, once, in its own tenant only.
func TestAcknowledgeLinkAgainstTheDatabase(t *testing.T) {
	ctx := context.Background()
	dbClient, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenant := testutil.InsertTenant(ctx, t, dbClient, "")
	other := testutil.InsertTenant(ctx, t, dbClient, "other")
	channelID := insertChannel(ctx, t, dbClient, tenant, "On-call email")
	otherChannel := insertChannel(ctx, t, dbClient, other, "Other team")
	open := insertAlert(ctx, t, dbClient, tenant, "active")
	closed := insertAlert(ctx, t, dbClient, tenant, "resolved")
	foreign := insertAlert(ctx, t, dbClient, other, "active")

	audit := &fakeAudit{}
	hub := &fakeHub{}
	h := NewHandlers(signer, alertservice.NewService(dbClient, nil), DBChannels(dbClient), hub, audit, nil)
	r := chi.NewRouter()
	r.Route("/api/v1/alert-actions", h.Routes)
	call := func(method string, tenantID, alertID, channel uuid.UUID) (int, Response) {
		tok, err := signer.ActionToken(actionlink.ActionAcknowledge, tenantID.String(), alertID.String(), channel.String(), time.Now())
		require.NoError(t, err)
		req := httptest.NewRequest(method, "/api/v1/alert-actions/acknowledge", nil)
		req.Header.Set(TokenHeader, tok)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var resp Response
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		return w.Code, resp
	}
	status := func(id uuid.UUID) (string, bool) {
		var s string
		var acked bool
		require.NoError(t, dbClient.QueryRowContext(ctx,
			`SELECT status, acknowledged_at IS NOT NULL FROM alerts WHERE id = $1`, id).Scan(&s, &acked))
		return s, acked
	}

	code, resp := call(http.MethodGet, tenant, open, channelID)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, StatePending, resp.State)
	require.Equal(t, "probe", resp.Alert.Subject)
	s, _ := status(open)
	require.Equal(t, "active", s, "GET must never acknowledge")

	code, resp = call(http.MethodPost, tenant, open, channelID)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, StateAcknowledged, resp.State)
	s, acked := status(open)
	require.Equal(t, "acknowledged", s)
	require.True(t, acked)
	require.Equal(t, 1, hub.acked)

	code, resp = call(http.MethodPost, tenant, open, channelID)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, StateAlready, resp.State)
	require.Equal(t, 1, hub.acked, "a replay must not acknowledge again")

	code, resp = call(http.MethodPost, tenant, closed, channelID)
	require.Equal(t, http.StatusConflict, code)
	require.Equal(t, StateResolved, resp.State)

	// A token for this tenant naming another tenant's alert (or channel)
	// reaches nothing.
	code, resp = call(http.MethodPost, tenant, foreign, channelID)
	require.Equal(t, http.StatusNotFound, code)
	require.Equal(t, StateInvalid, resp.State)
	code, _ = call(http.MethodPost, tenant, open, otherChannel)
	require.Equal(t, http.StatusNotFound, code)
	s, _ = status(foreign)
	require.Equal(t, "active", s)

	// Deleting the channel revokes the links it delivered.
	reopen := insertAlert(ctx, t, dbClient, tenant, "active")
	_, err := dbClient.ExecContext(ctx, `DELETE FROM alert_channels WHERE id = $1`, channelID)
	require.NoError(t, err)
	code, _ = call(http.MethodPost, tenant, reopen, channelID)
	require.Equal(t, http.StatusNotFound, code)
	s, _ = status(reopen)
	require.Equal(t, "active", s)

	require.NotEmpty(t, audit.events)
	require.Equal(t, "link sent to On-call email", audit.events[0].ActorLabel)
}

func insertChannel(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, dbClient.QueryRowContext(ctx, `
		INSERT INTO alert_channels (tenant_id, name, type, config)
		VALUES ($1, $2, 'email', '{"to":["ops@example.com"]}')
		RETURNING id`, tenantID, name).Scan(&id))
	return id
}

// insertAlert opens an alert on a monitor of its own, so several open
// alerts never collide on the one-open-alert-per-monitor rule.
func insertAlert(ctx context.Context, t *testing.T, dbClient *shareddb.Client, tenantID uuid.UUID, status string) uuid.UUID {
	t.Helper()
	monitorID := testutil.InsertHTTPMonitor(ctx, t, dbClient, tenantID, "probe")
	id := uuid.New()
	_, err := dbClient.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, status, triggered_at, resolved_at,
		    failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4::text::alert_status, NOW(), CASE WHEN $4::text = 'resolved' THEN NOW() END, 3, NOW(), NOW())`,
		id, tenantID, monitorID, status)
	require.NoError(t, err)
	return id
}
