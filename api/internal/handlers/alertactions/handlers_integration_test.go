//go:build !unit

package alertactions_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/handlers/alertactions"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/shared/actionlink"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

type auditCapture struct{ events []audit.Event }

func (a *auditCapture) Record(e audit.Event) { a.events = append(a.events, e) }

func insertAlert(ctx context.Context, t *testing.T, dbc *db.Client, tenantID, monitorID uuid.UUID, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var resolvedAt any
	if status == "resolved" {
		resolvedAt = time.Now()
	}
	_, err := dbc.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, status, triggered_at, resolved_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW() - INTERVAL '5 minutes', $5, 1, NOW(), NOW())`,
		id, tenantID, monitorID, status, resolvedAt)
	require.NoError(t, err)
	return id
}

func insertChannel(ctx context.Context, t *testing.T, dbc *db.Client, tenantID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := dbc.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'ops mail', 'email', '{}'::jsonb, TRUE, NOW(), NOW())`, id, tenantID)
	require.NoError(t, err)
	return id
}

// The public acknowledge route against a real database: the ack lands with
// acknowledged_via = magic_link and no user, a replay changes nothing, and a
// token is refused for another workspace's alert, a resolved alert, after
// expiry, and once its channel or workspace is deleted.
func TestAlertActions_Postgres(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	t.Setenv("PROBARA_SECRETS_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)))
	kp, err := secrets.NewEnvKeyProvider()
	require.NoError(t, err)
	signer := actionlink.New(kp)

	tenant := testutil.InsertTenant(ctx, t, dbc, "links")
	other := testutil.InsertTenant(ctx, t, dbc, "links-other")
	monitor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "checkout")
	otherMonitor := testutil.InsertHTTPMonitor(ctx, t, dbc, other, "theirs")
	// One open alert per (monitor, kind): each further open alert below
	// needs a monitor of its own.
	mineMonitor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, "checkout-2")
	otherMonitor2 := testutil.InsertHTTPMonitor(ctx, t, dbc, other, "theirs-2")
	channel := insertChannel(ctx, t, dbc, tenant)
	otherChannel := insertChannel(ctx, t, dbc, other)

	recorder := &auditCapture{}
	h := alertactions.NewHandlers(signer, alertservice.NewService(dbc, nil), alertactions.DBScope(dbc), nil, recorder, logger.New("test", "error"))
	router := chi.NewRouter()
	router.Get("/api/v1/alert-actions/{token}", h.Describe)
	router.Post("/api/v1/alert-actions/{token}", h.Acknowledge)

	mint := func(s *actionlink.Signer, tenantID, alertID, channelID uuid.UUID) string {
		tok, err := s.ActionToken(actionlink.ActionAcknowledge, tenantID.String(), alertID.String(), channelID.String())
		require.NoError(t, err)
		return tok
	}
	call := func(method, token string) (int, alertactions.Response) {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, "/api/v1/alert-actions/"+token, nil))
		var resp alertactions.Response
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), w.Body.String())
		return w.Code, resp
	}

	// Happy path, then replay.
	alert := insertAlert(ctx, t, dbc, tenant, monitor, "active")
	tok := mint(signer, tenant, alert, channel)
	code, resp := call(http.MethodGet, tok)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, alertactions.StatePending, resp.State)
	require.Equal(t, "checkout", resp.Alert.Subject)

	code, resp = call(http.MethodPost, tok)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, alertactions.StateAcknowledged, resp.State)
	var status string
	var via *string
	var by *uuid.UUID
	require.NoError(t, dbc.QueryRowContext(ctx,
		`SELECT status, acknowledged_via, acknowledged_by_user_id FROM alerts WHERE id = $1`, alert).Scan(&status, &via, &by))
	require.Equal(t, "acknowledged", status)
	require.NotNil(t, via)
	require.Equal(t, alertservice.ViaMagicLink, *via)
	require.Nil(t, by)
	require.Len(t, recorder.events, 1)
	require.Equal(t, audit.OutcomeSuccess, recorder.events[0].Outcome)

	code, resp = call(http.MethodPost, tok)
	require.Equal(t, http.StatusConflict, code)
	require.Equal(t, alertactions.StateAlready, resp.State)

	// Cross-tenant: a token for this workspace naming another's alert, and
	// one for the other workspace naming this one's.
	theirs := insertAlert(ctx, t, dbc, other, otherMonitor, "active")
	code, resp = call(http.MethodPost, mint(signer, tenant, theirs, channel))
	require.Equal(t, http.StatusNotFound, code)
	require.Equal(t, alertactions.StateInvalid, resp.State)
	mine := insertAlert(ctx, t, dbc, tenant, mineMonitor, "active")
	code, _ = call(http.MethodPost, mint(signer, other, mine, otherChannel))
	require.Equal(t, http.StatusNotFound, code)
	// A channel of another workspace cannot scope a link either.
	code, _ = call(http.MethodPost, mint(signer, tenant, mine, otherChannel))
	require.Equal(t, http.StatusNotFound, code)
	for _, id := range []uuid.UUID{theirs, mine} {
		require.NoError(t, dbc.QueryRowContext(ctx, `SELECT status FROM alerts WHERE id = $1`, id).Scan(&status))
		require.Equal(t, "active", status, "a cross-tenant token acknowledged an alert")
	}

	// Resolved.
	closed := insertAlert(ctx, t, dbc, tenant, monitor, "resolved")
	code, resp = call(http.MethodPost, mint(signer, tenant, closed, channel))
	require.Equal(t, http.StatusConflict, code)
	require.Equal(t, alertactions.StateResolved, resp.State)

	// Expired: minted 73 hours ago with the same key.
	past := actionlink.New(kp).WithClock(func() time.Time { return time.Now().Add(-73 * time.Hour) })
	code, resp = call(http.MethodPost, mint(past, tenant, mine, channel))
	require.Equal(t, http.StatusGone, code)
	require.Equal(t, alertactions.StateExpired, resp.State)

	// Deleting the channel revokes its links.
	tok = mint(signer, tenant, mine, channel)
	_, err = dbc.ExecContext(ctx, `DELETE FROM alert_channels WHERE id = $1`, channel)
	require.NoError(t, err)
	code, _ = call(http.MethodPost, tok)
	require.Equal(t, http.StatusNotFound, code)

	// A soft-deleted workspace's links die with it.
	otherAlert := insertAlert(ctx, t, dbc, other, otherMonitor2, "active")
	tok = mint(signer, other, otherAlert, otherChannel)
	code, _ = call(http.MethodGet, tok)
	require.Equal(t, http.StatusOK, code)
	_, err = dbc.ExecContext(ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, other)
	require.NoError(t, err)
	code, resp = call(http.MethodPost, tok)
	require.Equal(t, http.StatusNotFound, code)
	require.Equal(t, alertactions.StateInvalid, resp.State)
	require.NoError(t, dbc.QueryRowContext(ctx, `SELECT status FROM alerts WHERE id = $1`, otherAlert).Scan(&status))
	require.Equal(t, "active", status)
}
