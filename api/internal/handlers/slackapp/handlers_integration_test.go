//go:build !unit

package slackapp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/handlers/slackapp"
	alertchannelservice "github.com/yassinebenameur/probara/api/internal/services/alertchannels"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	slackplugin "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/slackapp"
	"github.com/yassinebenameur/probara/shared/secrets"
	"github.com/yassinebenameur/probara/shared/testutil"
)

const signingSecret = "test-signing-secret"

type auditCapture struct {
	mu     sync.Mutex
	events []audit.Event
}

func (a *auditCapture) Record(e audit.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
}

type replyCapture struct {
	mu      sync.Mutex
	replies []string
}

func (r *replyCapture) Reply(_ context.Context, responseURL, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.replies = append(r.replies, responseURL+" "+text)
	return nil
}

func (r *replyCapture) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.replies) == 0 {
		return ""
	}
	return r.replies[len(r.replies)-1]
}

type fakeInstaller struct {
	inst slackplugin.Installation
	err  error
	code string
}

func (f *fakeInstaller) Exchange(_ context.Context, _, _, code, _ string) (slackplugin.Installation, error) {
	f.code = code
	return f.inst, f.err
}

// card inserts an active alert in tenant posted as a card on a slack_app
// channel of that tenant, posting to team/slackChannel.
func card(ctx context.Context, t *testing.T, dbc *db.Client, tenant uuid.UUID, name, team, slackChannel string) (alertID, channelID uuid.UUID) {
	t.Helper()
	monitor := testutil.InsertHTTPMonitor(ctx, t, dbc, tenant, name)
	alertID, channelID = uuid.New(), uuid.New()
	cfg, _ := json.Marshal(map[string]any{"bot_token": "xoxb-1", "team_id": team, "channel_id": slackChannel, "interactive": true})
	_, err := dbc.ExecContext(ctx, `
		INSERT INTO alert_channels (id, tenant_id, name, type, config, is_active, created_at, updated_at)
		VALUES ($1, $2, 'slack', 'slack_app', $3, TRUE, NOW(), NOW())`, channelID, tenant, cfg)
	require.NoError(t, err)
	_, err = dbc.ExecContext(ctx, `
		INSERT INTO alerts (id, tenant_id, monitor_id, status, triggered_at, failure_count, created_at, updated_at)
		VALUES ($1, $2, $3, 'active', NOW() - INTERVAL '5 minutes', 1, NOW(), NOW())`, alertID, tenant, monitor)
	require.NoError(t, err)
	_, err = dbc.ExecContext(ctx, `
		INSERT INTO alert_notification_messages (alert_id, channel_id, episode, provider_ref, rendered_state)
		VALUES ($1, $2, 0, '{"channel":"C0","ts":"1.2"}', 'active')`, alertID, channelID)
	require.NoError(t, err)
	return alertID, channelID
}

func press(t *testing.T, router http.Handler, secret string, at time.Time, team, slackChannel, actionID, value string) *httptest.ResponseRecorder {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"type":         "block_actions",
		"team":         map[string]any{"id": team},
		"user":         map[string]any{"id": "U1", "username": "ada"},
		"channel":      map[string]any{"id": slackChannel},
		"container":    map[string]any{"type": "message", "channel_id": slackChannel},
		"response_url": "https://hooks.slack.com/actions/T/1/x",
		"actions":      []map[string]any{{"action_id": actionID, "value": value, "type": "button"}},
	})
	body := []byte(url.Values{"payload": {string(payload)}}.Encode())
	req := httptest.NewRequest(http.MethodPost, slackapp.InteractionsPath, strings.NewReader(string(body)))
	ts := strconv.FormatInt(at.Unix(), 10)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", slackplugin.Sign(secret, ts, body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func alertState(ctx context.Context, t *testing.T, dbc *db.Client, alertID uuid.UUID) (status, via string, firstAck bool) {
	t.Helper()
	var v *string
	require.NoError(t, dbc.QueryRowContext(ctx, `
		SELECT status::text, acknowledged_via, first_acknowledged_at IS NOT NULL FROM alerts WHERE id = $1`, alertID).
		Scan(&status, &v, &firstAck))
	if v != nil {
		via = *v
	}
	return status, via, firstAck
}

// Button presses against a real database: a forged, stale or foreign press
// changes nothing, a press from another workspace (or on another tenant's
// alert) is refused, and a genuine one acknowledges as a slack actor with
// first_acknowledged_at set, then reports "already" on a repeat.
func TestInteractions_Postgres(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)

	tenantA := testutil.InsertTenant(ctx, t, dbc, "slack-a")
	tenantB := testutil.InsertTenant(ctx, t, dbc, "slack-b")
	alertA, channelA := card(ctx, t, dbc, tenantA, "checkout", "T0AAA", "C0AAA")
	alertB, _ := card(ctx, t, dbc, tenantB, "theirs", "T0BBB", "C0BBB")

	recorder, replies := &auditCapture{}, &replyCapture{}
	h := slackapp.NewHandlers(slackapp.Config{SigningSecret: signingSecret}, slackapp.Deps{
		DB: dbc, Replier: replies, Alerts: alertservice.NewService(dbc, nil), Audit: recorder,
		Log: logger.New("test", "error"),
	})
	router := chi.NewRouter()
	router.Post(slackapp.InteractionsPath, h.Interactions)
	now := time.Now()
	value := slackplugin.AckValue(alertA.String(), channelA.String())

	require.Equal(t, http.StatusUnauthorized, press(t, router, "wrong-secret", now, "T0AAA", "C0AAA", slackplugin.ActionAcknowledge, value).Code)
	require.Equal(t, http.StatusUnauthorized, press(t, router, signingSecret, now.Add(-6*time.Minute), "T0AAA", "C0AAA", slackplugin.ActionAcknowledge, value).Code)
	// Another Slack workspace (B's) pressing a button that names A's card.
	require.Equal(t, http.StatusForbidden, press(t, router, signingSecret, now, "T0BBB", "C0AAA", slackplugin.ActionAcknowledge, value).Code)
	// The right workspace, but another channel of it.
	require.Equal(t, http.StatusForbidden, press(t, router, signingSecret, now, "T0AAA", "C0OTHER", slackplugin.ActionAcknowledge, value).Code)
	// A's channel naming B's alert: no card of that alert was ever posted there.
	require.Equal(t, http.StatusForbidden, press(t, router, signingSecret, now, "T0AAA", "C0AAA", slackplugin.ActionAcknowledge,
		slackplugin.AckValue(alertB.String(), channelA.String())).Code)
	for _, id := range []uuid.UUID{alertA, alertB} {
		status, _, _ := alertState(ctx, t, dbc, id)
		require.Equal(t, "active", status, "a refused press acknowledged %s", id)
	}
	require.Contains(t, replies.last(), "does not belong")

	rec := press(t, router, signingSecret, now, "T0AAA", "C0AAA", slackplugin.ActionAcknowledge, value)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"acknowledged"`)
	status, via, firstAck := alertState(ctx, t, dbc, alertA)
	require.Equal(t, "acknowledged", status)
	require.Equal(t, alertservice.ViaSlack, via)
	require.True(t, firstAck, "first_acknowledged_at not set")
	require.Contains(t, replies.last(), "https://hooks.slack.com/actions/T/1/x")
	require.Contains(t, replies.last(), "You acknowledged *checkout*")

	rec = press(t, router, signingSecret, now, "T0AAA", "C0AAA", slackplugin.ActionAcknowledge, value)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"already"`)

	// Other controls are acknowledged with a 200 and ignored.
	rec = press(t, router, signingSecret, now, "T0AAA", "C0AAA", "probara_open", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"ignored"`)

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	var ok, refused int
	for _, e := range recorder.events {
		require.Equal(t, "alert.acknowledge", e.Action)
		require.Equal(t, "slack:ada", e.ActorLabel)
		if e.Outcome == audit.OutcomeSuccess {
			ok++
			require.Equal(t, tenantA, *e.TenantID)
		} else {
			refused++
		}
	}
	require.Equal(t, 1, ok)
	require.GreaterOrEqual(t, refused, 3)

	// Without a signing secret the endpoint does not exist.
	off := slackapp.NewHandlers(slackapp.Config{}, slackapp.Deps{DB: dbc})
	offRouter := chi.NewRouter()
	offRouter.Post(slackapp.InteractionsPath, off.Interactions)
	require.Equal(t, http.StatusNotFound, press(t, offRouter, signingSecret, now, "T0AAA", "C0AAA", slackplugin.ActionAcknowledge, value).Code)
}

// Add to Slack: the install signs a state bound to the browser's nonce
// cookie; the callback with both creates a slack_app channel in the state's
// workspace. A missing cookie, a tampered state or a demoted user creates
// nothing.
func TestInstallFlow_Postgres(t *testing.T) {
	ctx := context.Background()
	dbc, cleanup := testutil.SetupPostgresDB(ctx, t)
	t.Cleanup(cleanup)
	tenant := testutil.InsertTenant(ctx, t, dbc, "slack-install")
	user := uuid.New()
	_, err := dbc.ExecContext(ctx, `
		INSERT INTO admin_users (id, username, password_hash, platform_role, created_at, updated_at)
		VALUES ($1, 'ada', 'x', 'member', NOW(), NOW())`, user)
	require.NoError(t, err)
	_, err = dbc.ExecContext(ctx, `
		INSERT INTO tenant_memberships (id, admin_user_id, tenant_id, role, created_at, updated_at)
		VALUES ($1, $2, $3, 'admin', NOW(), NOW())`, uuid.New(), user, tenant)
	require.NoError(t, err)

	installer := &fakeInstaller{inst: slackplugin.Installation{
		BotToken: "xoxb-installed", TeamID: "T0AAA", TeamName: "Acme", ChannelID: "C0ALERTS", ChannelName: "alerts",
	}}
	recorder := &auditCapture{}
	cfg := slackapp.Config{
		ClientID: "cid", ClientSecret: "csecret", SigningSecret: signingSecret,
		PublicBaseURL: "https://probara.example.com", AppBaseURL: "https://app.example.com",
	}
	h := slackapp.NewHandlers(cfg, slackapp.Deps{
		DB: dbc, Installer: installer, Channels: alertchannelservice.NewService(dbc, secrets.NoOpEncryptor{}),
		Audit: recorder, Log: logger.New("test", "error"),
	})
	router := chi.NewRouter()
	router.Get("/install", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(ctxpkg.WithAdminID(ctxpkg.WithTenantID(r.Context(), tenant.String()), user.String()))
		h.Install(w, r)
	})
	router.Get(slackapp.CallbackPath, h.Callback)

	install := func() (state string, cookie *http.Cookie) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/install", nil))
		require.Equal(t, http.StatusFound, rec.Code)
		loc, err := url.Parse(rec.Header().Get("Location"))
		require.NoError(t, err)
		require.Equal(t, "slack.com", loc.Host)
		require.Equal(t, "https://probara.example.com"+slackapp.CallbackPath, loc.Query().Get("redirect_uri"))
		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		return loc.Query().Get("state"), cookies[0]
	}
	callback := func(state string, cookie *http.Cookie) string {
		req := httptest.NewRequest(http.MethodGet, slackapp.CallbackPath+"?code=the-code&state="+url.QueryEscape(state), nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusFound, rec.Code)
		return rec.Header().Get("Location")
	}
	channels := func() int {
		var n int
		require.NoError(t, dbc.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_channels WHERE tenant_id = $1 AND type = 'slack_app'`, tenant).Scan(&n))
		return n
	}

	state, cookie := install()
	require.Contains(t, callback(state, nil), "slack_error=invalid_state")
	require.Contains(t, callback(state[:len(state)-2]+"xx", cookie), "slack_error=invalid_state")
	require.Equal(t, 0, channels())

	loc := callback(state, cookie)
	require.True(t, strings.HasPrefix(loc, "https://app.example.com/alert-channels/"), loc)
	require.Contains(t, loc, "slack=installed")
	require.Equal(t, "the-code", installer.code)
	require.Equal(t, 1, channels())
	var name string
	var raw []byte
	require.NoError(t, dbc.QueryRowContext(ctx, `SELECT name, config FROM alert_channels WHERE tenant_id = $1 AND type = 'slack_app'`, tenant).Scan(&name, &raw))
	require.Equal(t, "Slack #alerts (Acme)", name)
	var stored map[string]any
	require.NoError(t, json.Unmarshal(raw, &stored))
	require.Equal(t, "T0AAA", stored["team_id"])
	require.Equal(t, "C0ALERTS", stored["channel_id"])
	require.Equal(t, true, stored["interactive"])

	// Demoted between install and callback: nothing is created.
	state, cookie = install()
	_, err = dbc.ExecContext(ctx, `UPDATE tenant_memberships SET role = 'viewer' WHERE admin_user_id = $1`, user)
	require.NoError(t, err)
	require.Contains(t, callback(state, cookie), "slack_error=forbidden")
	require.Equal(t, 1, channels())

	// Not configured: the install sends the browser back with the reason.
	off := slackapp.NewHandlers(slackapp.Config{AppBaseURL: "https://app.example.com"}, slackapp.Deps{DB: dbc})
	rec := httptest.NewRecorder()
	off.Install(rec, httptest.NewRequest(http.MethodGet, "/install", nil))
	require.Equal(t, http.StatusFound, rec.Code)
	require.Contains(t, rec.Header().Get("Location"), "slack_error=not_configured")

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	var created bool
	for _, e := range recorder.events {
		if e.Action == "alert_channel.create" && e.Outcome == audit.OutcomeSuccess {
			created = true
			require.Equal(t, user, *e.ActorID)
		}
	}
	require.True(t, created, "the install was not audited")
}
