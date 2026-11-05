// Package slackapp serves the API side of the Slack app channel type
// (shared/notifications/plugin/builtin/slackapp):
//
//   - GET /api/v1/integrations/slack/install (tenant admins, a browser
//     session): starts "Add to Slack", Slack's OAuth v2 install, with a signed
//     state bound to the browser by a nonce cookie.
//   - GET /api/v1/integrations/slack/callback (public): Slack sends the
//     browser back here with the code; the install becomes a slack_app alert
//     channel in the workspace the state names.
//   - POST /api/v1/integrations/slack/interactions (public): Slack posts the
//     card's Acknowledge button presses here, signed with the app's signing
//     secret. A press acknowledges through the alerts service, so
//     first_acknowledged_at and every other acknowledgement rule apply, and
//     the alerter's card reconcile then rewrites the card.
//
// The public routes sit outside the audited protected group and record their
// own audit events; the router-wide anonymous rate limiter covers them.
package slackapp

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/shared/auth"
	ctxpkg "github.com/yassinebenameur/probara/shared/context"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
	slackplugin "github.com/yassinebenameur/probara/shared/notifications/plugin/builtin/slackapp"
	"github.com/yassinebenameur/probara/shared/tenancy"
)

// ChannelType is the alert channel type the install creates.
const ChannelType = "slack_app"

const (
	// CallbackPath is the OAuth redirect path, under PUBLIC_BASE_URL.
	CallbackPath = "/api/v1/integrations/slack/callback"
	// InteractionsPath is the interactivity request URL path.
	InteractionsPath = "/api/v1/integrations/slack/interactions"

	nonceCookie   = "probara_slack_oauth"
	cookiePath    = "/api/v1/integrations/slack"
	stateLifetime = 10 * time.Minute
	// maxInteractionBody bounds a button press; Slack's are a few KB.
	maxInteractionBody = 64 << 10
)

// Config is what the routes need from shared/config.
type Config struct {
	ClientID      string
	ClientSecret  string
	SigningSecret string
	// PublicBaseURL is the API's external origin; the OAuth redirect URI is
	// PublicBaseURL + CallbackPath.
	PublicBaseURL string
	// AppBaseURL is the operator UI origin the browser lands on after the
	// install; empty keeps the redirect relative (same origin).
	AppBaseURL   string
	CookieSecure bool
}

func (c Config) installEnabled() bool {
	return c.ClientID != "" && c.ClientSecret != "" && c.PublicBaseURL != ""
}

func (c Config) redirectURI() string { return c.PublicBaseURL + CallbackPath }

// Installer exchanges an OAuth code (*slackplugin.OAuthClient).
type Installer interface {
	Exchange(ctx context.Context, clientID, clientSecret, code, redirectURI string) (slackplugin.Installation, error)
}

// Replier answers a button press through its response_url
// (*slackplugin.Responder).
type Replier interface {
	Reply(ctx context.Context, responseURL, text string) error
}

// Alerts is the slice of the alerts service the interactions use.
type Alerts interface {
	GetAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error)
	AcknowledgeAlert(ctx context.Context, tenantID, alertID uuid.UUID, actor alertservice.Actor) (*models.AlertWithDetails, error)
}

// Channels creates the installed channel (the alert channel service, which
// encrypts the bot token).
type Channels interface {
	CreateAlertChannel(ctx context.Context, tenantID uuid.UUID, req *models.CreateAlertChannelRequest) (*models.AlertChannel, error)
}

// Broadcaster fans an acknowledgement out to open alert streams.
type Broadcaster interface {
	BroadcastAlertAcknowledged(tenantID uuid.UUID, alert *models.AlertWithDetails)
}

// AuditRecorder records install and acknowledge events.
type AuditRecorder interface {
	Record(event audit.Event)
}

// Handlers serves the Slack integration routes.
type Handlers struct {
	cfg       Config
	db        db.Querier
	installer Installer
	replier   Replier
	alerts    Alerts
	channels  Channels
	hub       Broadcaster
	audit     AuditRecorder
	log       *logger.Logger
	now       func() time.Time
}

// Deps are the collaborators of NewHandlers; hub and Audit may be nil.
type Deps struct {
	DB        db.Querier
	Installer Installer
	Replier   Replier
	Alerts    Alerts
	Channels  Channels
	Hub       Broadcaster
	Audit     AuditRecorder
	Log       *logger.Logger
}

// NewHandlers wires the routes.
func NewHandlers(cfg Config, d Deps) *Handlers {
	cfg.PublicBaseURL = strings.TrimRight(cfg.PublicBaseURL, "/")
	cfg.AppBaseURL = strings.TrimRight(cfg.AppBaseURL, "/")
	return &Handlers{
		cfg: cfg, db: d.DB, installer: d.Installer, replier: d.Replier, alerts: d.Alerts,
		channels: d.Channels, hub: d.Hub, audit: d.Audit, log: d.Log, now: time.Now,
	}
}

// --- install ----------------------------------------------------------------

// stateClaims is the signed OAuth state: who started the install, for which
// workspace, the hash of the browser's nonce cookie, and when it expires.
type stateClaims struct {
	TenantID  uuid.UUID `json:"t"`
	UserID    uuid.UUID `json:"u"`
	NonceHash string    `json:"n"`
	ExpiresAt int64     `json:"e"`
}

// stateKey derives the state's HMAC key from the client secret, which only
// this API knows.
func (h *Handlers) stateKey() []byte {
	mac := hmac.New(sha256.New, []byte(h.cfg.ClientSecret))
	mac.Write([]byte("probara slack oauth state v1"))
	return mac.Sum(nil)
}

func (h *Handlers) signState(c stateClaims) (string, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, h.stateKey())
	mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

var errBadState = errors.New("invalid or expired install state")

func (h *Handlers) verifyState(state string) (stateClaims, error) {
	body, sig, ok := strings.Cut(state, ".")
	if !ok {
		return stateClaims{}, errBadState
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return stateClaims{}, errBadState
	}
	mac := hmac.New(sha256.New, h.stateKey())
	mac.Write([]byte(body))
	if !hmac.Equal(got, mac.Sum(nil)) {
		return stateClaims{}, errBadState
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return stateClaims{}, errBadState
	}
	var c stateClaims
	if err := json.Unmarshal(payload, &c); err != nil || h.now().Unix() > c.ExpiresAt {
		return stateClaims{}, errBadState
	}
	return c, nil
}

func nonceHash(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return hex.EncodeToString(sum[:])
}

// Install handles GET /api/v1/integrations/slack/install: a tenant admin's
// browser navigates here from the channel form and is sent on to Slack.
func (h *Handlers) Install(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.installEnabled() {
		h.redirectApp(w, r, "/alert-channels/new?type="+ChannelType+"&slack_error=not_configured")
		return
	}
	tenantRaw, _ := ctxpkg.GetTenantID(r.Context())
	userRaw, _ := ctxpkg.GetAdminID(r.Context())
	tenantID, terr := uuid.Parse(tenantRaw)
	userID, uerr := uuid.Parse(userRaw)
	if terr != nil || uerr != nil {
		// An API key cannot complete a browser consent flow.
		http.Error(w, "a signed-in tenant admin session is required", http.StatusForbidden)
		return
	}
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	state, err := h.signState(stateClaims{
		TenantID: tenantID, UserID: userID, NonceHash: nonceHash(nonce),
		ExpiresAt: h.now().Add(stateLifetime).Unix(),
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: nonceCookie, Value: nonce, Path: cookiePath, HttpOnly: true,
		Secure: h.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: int(stateLifetime.Seconds()),
	})
	noStore(w)
	http.Redirect(w, r, slackplugin.AuthorizeURL(h.cfg.ClientID, state, h.cfg.redirectURI()), http.StatusFound)
}

// Callback handles GET /api/v1/integrations/slack/callback. The browser is
// navigating, so every outcome is a redirect to the operator UI.
func (h *Handlers) Callback(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.installEnabled() {
		http.NotFound(w, r)
		return
	}
	noStore(w)
	http.SetCookie(w, &http.Cookie{
		Name: nonceCookie, Value: "", Path: cookiePath, HttpOnly: true,
		Secure: h.cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	fail := func(code string) {
		h.redirectApp(w, r, "/alert-channels/new?type="+ChannelType+"&slack_error="+code)
	}
	q := r.URL.Query()
	if q.Get("error") != "" {
		fail("denied")
		return
	}
	claims, err := h.verifyState(q.Get("state"))
	if err != nil {
		fail("invalid_state")
		return
	}
	cookie, err := r.Cookie(nonceCookie)
	if err != nil || !hmac.Equal([]byte(nonceHash(cookie.Value)), []byte(claims.NonceHash)) {
		// The state was minted for another browser: someone else's install
		// link must never land a channel in their workspace.
		fail("invalid_state")
		return
	}
	allowed, err := h.stillTenantAdmin(r.Context(), claims.UserID, claims.TenantID)
	if err != nil {
		h.logError(err, "check installer role")
		fail("internal")
		return
	}
	if !allowed {
		h.recordInstall(r, claims, "", http.StatusForbidden, "not a tenant admin")
		fail("forbidden")
		return
	}
	code := q.Get("code")
	if code == "" {
		fail("invalid_state")
		return
	}
	inst, err := h.installer.Exchange(r.Context(), h.cfg.ClientID, h.cfg.ClientSecret, code, h.cfg.redirectURI())
	if err != nil {
		h.logError(err, "exchange slack oauth code")
		h.recordInstall(r, claims, "", http.StatusBadGateway, "exchange failed")
		fail("exchange_failed")
		return
	}
	if inst.ChannelID == "" {
		fail("no_channel")
		return
	}
	cfg := map[string]any{
		"bot_token":   inst.BotToken,
		"team_id":     inst.TeamID,
		"channel_id":  inst.ChannelID,
		"interactive": h.cfg.SigningSecret != "",
	}
	raw, _ := json.Marshal(cfg)
	if p, ok := plugin.DefaultRegistry.Get(ChannelType); ok {
		if err := p.Validate(raw); err != nil {
			h.logError(err, "validate slack installation")
			fail("exchange_failed")
			return
		}
	}
	channel, err := h.channels.CreateAlertChannel(r.Context(), claims.TenantID, &models.CreateAlertChannelRequest{
		Name:   channelName(inst),
		Type:   models.AlertChannelType(ChannelType),
		Config: raw,
	})
	if err != nil {
		h.logError(err, "create slack channel")
		fail("internal")
		return
	}
	h.recordInstall(r, claims, channel.ID.String(), http.StatusCreated, "")
	h.redirectApp(w, r, "/alert-channels/"+url.PathEscape(channel.ID.String())+"?slack=installed")
}

func channelName(inst slackplugin.Installation) string {
	name := "Slack"
	if inst.ChannelName != "" {
		name += " #" + inst.ChannelName
	}
	if inst.TeamName != "" {
		name += " (" + inst.TeamName + ")"
	}
	return name
}

// stillTenantAdmin re-checks, when Slack sends the browser back, that the
// user who started the install may still administer the workspace (an
// enabled account, a superadmin or the tenant's admin) and that the
// workspace is live.
func (h *Handlers) stillTenantAdmin(ctx context.Context, userID, tenantID uuid.UUID) (bool, error) {
	var ok bool
	err := h.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM admin_users u
			LEFT JOIN tenant_memberships m ON m.admin_user_id = u.id AND m.tenant_id = $2
			WHERE u.id = $1 AND u.disabled_at IS NULL
			  AND (u.platform_role = $3 OR m.role = $4)
		) AND `+tenancy.LiveTenantSQL("$2::uuid"),
		userID, tenantID, auth.PlatformRoleSuperadmin, auth.RoleAdmin).Scan(&ok)
	return ok, err
}

func (h *Handlers) recordInstall(r *http.Request, c stateClaims, channelID string, status int, reason string) {
	if h.audit == nil {
		return
	}
	tenantID, userID := c.TenantID, c.UserID
	event := audit.FromRequest(r)
	event.TenantID = &tenantID
	event.ActorType = audit.ActorAdminUser
	event.ActorID = &userID
	event.Action = "alert_channel.create"
	event.ResourceType = "alert_channel"
	event.ResourceID = channelID
	event.StatusCode = status
	event.Outcome = audit.OutcomeSuccess
	details := map[string]any{"via": "slack_install", "type": ChannelType}
	if status >= 400 {
		event.Outcome = audit.OutcomeFailure
		details["reason"] = reason
	}
	event.MergeDetails(details)
	h.audit.Record(event)
}

// redirectApp sends the browser to path on the operator UI.
func (h *Handlers) redirectApp(w http.ResponseWriter, r *http.Request, path string) {
	http.Redirect(w, r, h.cfg.AppBaseURL+path, http.StatusFound)
}

// --- interactions -------------------------------------------------------------

// Interactions handles POST /api/v1/integrations/slack/interactions.
func (h *Handlers) Interactions(w http.ResponseWriter, r *http.Request) {
	if h.cfg.SigningSecret == "" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxInteractionBody+1))
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}
	if len(body) > maxInteractionBody {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	if err := slackplugin.VerifySignature(h.cfg.SigningSecret, r.Header, body, h.now()); err != nil {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	in, err := slackplugin.ParseInteraction(body)
	if err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	var action *slackplugin.Action
	if in.Type == "block_actions" {
		for i := range in.Actions {
			if in.Actions[i].ActionID == slackplugin.ActionAcknowledge {
				action = &in.Actions[i]
				break
			}
		}
	}
	if action == nil {
		// Other controls (the "Open in Probara" link button also posts
		// here) need no answer beyond the 200.
		writeOutcome(w, http.StatusOK, "ignored")
		return
	}
	alertRaw, channelRaw, ok := slackplugin.ParseAckValue(action.Value)
	alertID, aerr := uuid.Parse(alertRaw)
	channelID, cerr := uuid.Parse(channelRaw)
	if !ok || aerr != nil || cerr != nil {
		http.Error(w, "invalid action value", http.StatusBadRequest)
		return
	}

	card, err := h.lookupCard(r.Context(), alertID, channelID)
	if err != nil {
		h.logError(err, "look up slack card")
		h.reply(r.Context(), in, slackplugin.OutcomeFailed, "")
		writeOutcome(w, http.StatusInternalServerError, slackplugin.OutcomeFailed)
		return
	}
	// The press must come from the workspace (and channel) this alert
	// channel posts to: a Slack app shared by several Probara workspaces
	// must never let one of them acknowledge another's alerts.
	if card == nil || card.teamID != in.TeamID || (in.ChannelID != "" && card.slackChannel != in.ChannelID) {
		if card != nil {
			h.recordAck(r, in, card.tenantID, alertID, channelID, http.StatusForbidden, slackplugin.OutcomeRefused)
		}
		h.reply(r.Context(), in, slackplugin.OutcomeRefused, "")
		writeOutcome(w, http.StatusForbidden, slackplugin.OutcomeRefused)
		return
	}

	outcome := slackplugin.OutcomeAcknowledged
	status := http.StatusOK
	alert, err := h.alerts.AcknowledgeAlert(r.Context(), card.tenantID, alertID, alertservice.Actor{Via: alertservice.ViaSlack})
	switch {
	case err == nil:
		if h.hub != nil {
			h.hub.BroadcastAlertAcknowledged(card.tenantID, alert)
		}
	case strings.Contains(err.Error(), "not found or not active"):
		// Lost to another acknowledgement or a resolve: say which.
		alert, err = h.alerts.GetAlert(r.Context(), card.tenantID, alertID)
		switch {
		case err != nil:
			outcome, status = slackplugin.OutcomeFailed, http.StatusInternalServerError
		case alert.Status == models.AlertStatusAcknowledged:
			outcome, status = slackplugin.OutcomeAlready, http.StatusConflict
		default:
			outcome, status = slackplugin.OutcomeResolved, http.StatusConflict
		}
	default:
		h.logError(err, "acknowledge from slack")
		outcome, status = slackplugin.OutcomeFailed, http.StatusInternalServerError
	}
	h.recordAck(r, in, card.tenantID, alertID, channelID, status, outcome)
	h.reply(r.Context(), in, outcome, subjectOf(alert))
	// Slack shows any non-2xx as a failed press; the ephemeral reply says
	// what happened, so a lost race still answers 200.
	if status == http.StatusConflict {
		status = http.StatusOK
	}
	writeOutcome(w, status, outcome)
}

type cardTarget struct {
	tenantID     uuid.UUID
	teamID       string
	slackChannel string
}

// lookupCard finds the slack_app channel a card of alertID was posted on,
// with the workspace and Slack channel it posts to. Nil when no such card
// exists (the alert was never posted there, the channel is gone or another
// type, the alert belongs to another tenant, or the workspace is deleted).
func (h *Handlers) lookupCard(ctx context.Context, alertID, channelID uuid.UUID) (*cardTarget, error) {
	var t cardTarget
	err := h.db.QueryRowContext(ctx, `
		SELECT c.tenant_id, COALESCE(c.config->>'team_id', ''), COALESCE(c.config->>'channel_id', '')
		FROM alert_notification_messages msg
		JOIN alert_channels c ON c.id = msg.channel_id AND c.type = $3
		JOIN alerts a ON a.id = msg.alert_id AND a.tenant_id = c.tenant_id
		WHERE msg.alert_id = $1 AND msg.channel_id = $2
		  AND `+tenancy.LiveTenantSQL("c.tenant_id"),
		alertID, channelID, ChannelType).Scan(&t.tenantID, &t.teamID, &t.slackChannel)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (h *Handlers) reply(ctx context.Context, in slackplugin.Interaction, outcome, subject string) {
	if h.replier == nil || in.ResponseURL == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := h.replier.Reply(ctx, in.ResponseURL, slackplugin.ReplyText(outcome, subject)); err != nil {
		h.logError(err, "answer slack interaction")
	}
}

func (h *Handlers) recordAck(r *http.Request, in slackplugin.Interaction, tenantID, alertID, channelID uuid.UUID, status int, outcome string) {
	if h.audit == nil {
		return
	}
	event := audit.FromRequest(r)
	event.TenantID = &tenantID
	event.ActorLabel = "slack:" + in.UserName
	if in.UserName == "" {
		event.ActorLabel = "slack:" + in.UserID
	}
	event.Action = "alert.acknowledge"
	event.ResourceType = "alert"
	event.ResourceID = alertID.String()
	event.StatusCode = status
	event.Outcome = audit.OutcomeSuccess
	details := map[string]any{
		"via":           alertservice.ViaSlack,
		"channel_id":    channelID.String(),
		"slack_team_id": in.TeamID,
		"slack_user_id": in.UserID,
	}
	if outcome != slackplugin.OutcomeAcknowledged {
		event.Outcome = audit.OutcomeFailure
		details["reason"] = outcome
	}
	event.MergeDetails(details)
	h.audit.Record(event)
}

func subjectOf(a *models.AlertWithDetails) string {
	if a == nil {
		return ""
	}
	switch {
	case a.MonitorName != nil && strings.TrimSpace(*a.MonitorName) != "":
		return strings.TrimSpace(*a.MonitorName)
	case a.SourceLocationName != nil && a.TargetLocationName != nil:
		return *a.SourceLocationName + " → " + *a.TargetLocationName
	}
	return ""
}

func writeOutcome(w http.ResponseWriter, status int, outcome string) {
	noStore(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"state": outcome})
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func (h *Handlers) logError(err error, op string) {
	if h.log != nil {
		h.log.WithFields(map[string]interface{}{"error": err.Error(), "op": op}).Error("Slack integration failed")
	}
}
