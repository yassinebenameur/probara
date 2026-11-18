// Package alertactions serves the signed one-click acknowledge links that
// notifications carry (shared/actionlink):
//
//	GET  /api/v1/alert-actions/acknowledge  describes the alert, never acts
//	POST /api/v1/alert-actions/acknowledge  acknowledges it
//
// The routes are public: the token is the credential, bound to one tenant,
// alert and channel and valid for 72 hours, and only in the alert episode it
// was sent in: once the alert relapses, a link from an earlier page reads as
// invalid (issuedInEarlierEpisode). GET never changes anything,
// because mail scanners and link previewers fetch every link they see; the
// operator UI's confirm page (/alert-action) POSTs on a button press.
//
// The token travels in the TokenHeader request header, never in the URL, so
// the request log, the panic log and the per-route metric labels (all keyed
// on the URL path) never see it. A forged or malformed token is refused
// before any database read, and every refusal of that kind answers exactly
// the same, so probing learns nothing.
package alertactions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apierrors "github.com/yassinebenameur/probara/api/internal/errors"
	"github.com/yassinebenameur/probara/api/internal/models"
	alertservice "github.com/yassinebenameur/probara/api/internal/services/alerts"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/shared/actionlink"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/present"
	"github.com/yassinebenameur/probara/shared/tenancy"
)

// TokenHeader carries the action token. The confirm page reads the token
// from its URL fragment and sends it here.
const TokenHeader = "X-Action-Token"

// States a response reports in "state".
const (
	// StatePending: the alert is active and can be acknowledged (GET only).
	StatePending = "pending"
	// StateAcknowledged: this request acknowledged it (POST only).
	StateAcknowledged = "acknowledged"
	// StateAlready: the alert was acknowledged before. Not an error: the
	// link did what it is for.
	StateAlready = "already"
	// StateResolved: the alert is closed; there is nothing to acknowledge.
	StateResolved = "resolved"
	// StateExpired: an authentic token past its lifetime.
	StateExpired = "expired"
	// StateInvalid: a missing, malformed, forged or revoked token (retired
	// key version, deleted channel or alert, or a link from an alert
	// episode that has since relapsed).
	StateInvalid = "invalid"
)

// ViaLink names this surface in audit details.
const ViaLink = "acknowledge_link"

// Verifier checks a token (an *actionlink.Signer).
type Verifier interface {
	Verify(token, action string) (actionlink.Claims, error)
}

// Alerts is the slice of the alerts service the routes use. Acknowledging
// goes through the same service method as the Alerts page, so the state
// change is identical.
type Alerts interface {
	GetAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error)
	AcknowledgeAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error)
}

// Channels names the channel a token was delivered on. ok is false when the
// channel no longer exists in the tenant: deleting a channel, or its
// workspace, revokes every link it delivered.
type Channels interface {
	ChannelName(ctx context.Context, tenantID, channelID uuid.UUID) (name string, ok bool, err error)
}

// DBChannels is Channels backed by the database.
func DBChannels(q db.Querier) Channels { return dbChannels{q: q} }

type dbChannels struct{ q db.Querier }

func (c dbChannels) ChannelName(ctx context.Context, tenantID, channelID uuid.UUID) (string, bool, error) {
	var name string
	err := c.q.QueryRowContext(ctx,
		`SELECT name FROM alert_channels WHERE id = $1 AND tenant_id = $2 AND `+tenancy.LiveTenantSQL("alert_channels.tenant_id"),
		channelID, tenantID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return name, true, nil
}

// Broadcaster fans an acknowledgement out to open alert streams, as the
// Alerts page's acknowledge does.
type Broadcaster interface {
	BroadcastAlertAcknowledged(tenantID uuid.UUID, alert *models.AlertWithDetails)
}

// AuditRecorder records acknowledge attempts.
type AuditRecorder interface {
	Record(event audit.Event)
}

// Handlers serves the alert-action routes.
type Handlers struct {
	links    Verifier
	alerts   Alerts
	channels Channels
	hub      Broadcaster
	audit    AuditRecorder
	log      *logger.Logger
}

// NewHandlers wires the routes. links may be a nil *actionlink.Signer (no
// PROBARA_SECRETS_KEY): every token is then invalid, matching the senders,
// which mint none. hub and recorder may be nil.
func NewHandlers(links Verifier, alerts Alerts, channels Channels, hub Broadcaster, recorder AuditRecorder, log *logger.Logger) *Handlers {
	return &Handlers{links: links, alerts: alerts, channels: channels, hub: hub, audit: recorder, log: log}
}

// Routes mounts the acknowledge routes on r (the /alert-actions subrouter).
func (h *Handlers) Routes(r chi.Router) {
	r.Get("/acknowledge", h.Describe)
	r.Post("/acknowledge", h.Acknowledge)
}

// Response is the body of both routes.
type Response struct {
	State     string        `json:"state"`
	Alert     *AlertSummary `json:"alert,omitempty"`
	ExpiresAt *time.Time    `json:"expires_at,omitempty"`
}

// AlertSummary is all a link holder learns about the alert: what the
// notification they hold already said, plus its current state.
type AlertSummary struct {
	// Subject is the monitor (or location) name.
	Subject string `json:"subject"`
	// Label is the kind-aware headline, e.g. "Alert Triggered" or
	// "CPU Usage High" (package present).
	Label          string     `json:"label"`
	Status         string     `json:"status"`
	TriggeredAt    time.Time  `json:"triggered_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	// OpenPath opens this alert on the Alerts page for someone signed in to
	// the workspace. It names only the alert and tenant IDs, which the token
	// the caller holds already carries in the clear.
	OpenPath string `json:"open_path"`
}

// lookup is a verified token, its channel and the alert it names.
type lookup struct {
	claims      actionlink.Claims
	authentic   bool // the MAC verified, so the claims can be trusted
	channelName string
	alert       *models.AlertWithDetails
	state       string
}

// Describe handles GET: it reports the alert and the link's state and never
// acts.
func (h *Handlers) Describe(w http.ResponseWriter, r *http.Request) {
	l, err := h.resolve(r)
	if err != nil {
		h.internalError(w, r, l, "describe", err)
		return
	}
	writeState(w, l, statusFor(l.state, false))
}

// Acknowledge handles POST: it acknowledges the alert when the token is
// valid and the alert still active. Acknowledging an acknowledged alert is
// reported, not refused.
func (h *Handlers) Acknowledge(w http.ResponseWriter, r *http.Request) {
	l, err := h.resolve(r)
	if err != nil {
		h.internalError(w, r, l, "acknowledge", err)
		return
	}
	if l.state == StatePending {
		acked, err := h.alerts.AcknowledgeAlert(r.Context(), l.claims.TenantID, l.claims.AlertID)
		switch {
		case err == nil:
			l.alert, l.state = acked, StateAcknowledged
			if h.hub != nil {
				h.hub.BroadcastAlertAcknowledged(l.claims.TenantID, acked)
			}
		case errors.Is(err, alertservice.ErrAlertNotActive):
			// Lost a race with another acknowledge or a resolve: report
			// what the alert is now.
			if l, err = h.reload(r.Context(), l); err != nil {
				h.internalError(w, r, l, "acknowledge", err)
				return
			}
		default:
			h.internalError(w, r, l, "acknowledge", err)
			return
		}
	}
	status := statusFor(l.state, true)
	h.record(r, l, status)
	writeState(w, l, status)
}

// resolve verifies the token and loads the alert it names. Only an
// unexpected failure (the database) is an error; every refusal is a state.
func (h *Handlers) resolve(r *http.Request) (lookup, error) {
	token := strings.TrimSpace(r.Header.Get(TokenHeader))
	if h.links == nil || token == "" {
		return lookup{state: StateInvalid}, nil
	}
	claims, err := h.links.Verify(token, actionlink.ActionAcknowledge)
	switch {
	case errors.Is(err, actionlink.ErrExpired):
		// Authentic but stale: say so, and nothing about the alert.
		return lookup{claims: claims, authentic: true, state: StateExpired}, nil
	case err != nil:
		return lookup{state: StateInvalid}, nil
	}
	l := lookup{claims: claims, authentic: true}

	name, ok, err := h.channels.ChannelName(r.Context(), claims.TenantID, claims.ChannelID)
	if err != nil {
		return l, err
	}
	if !ok {
		l.state = StateInvalid
		return l, nil
	}
	l.channelName = name
	return h.reload(r.Context(), l)
}

// reload reads the alert in the token's tenant and derives the state. An
// alert outside that tenant reads as not found, so a token can never reach
// another workspace's alert.
func (h *Handlers) reload(ctx context.Context, l lookup) (lookup, error) {
	alert, err := h.alerts.GetAlert(ctx, l.claims.TenantID, l.claims.AlertID)
	if errors.Is(err, alertservice.ErrAlertNotFound) {
		l.alert, l.state = nil, StateInvalid
		return l, nil
	}
	if err != nil {
		return l, err
	}
	if issuedInEarlierEpisode(l.claims.IssuedAt, alert) {
		// A link acts only on the episode it was sent for: whoever holds an
		// old page has not seen this relapse, so acknowledging it from there
		// would silence the new outage on a stale reading.
		l.alert, l.state = nil, StateInvalid
		return l, nil
	}
	l.alert = alert
	switch alert.Status {
	case models.AlertStatusActive:
		l.state = StatePending
	case models.AlertStatusAcknowledged:
		l.state = StateAlready
	default:
		l.state = StateResolved
	}
	return l, nil
}

// issuedInEarlierEpisode reports whether a link issued at issuedAt (whole
// seconds, rounded down: actionlink.Claims.IssuedAt) was sent before the
// alert's current episode. The cut-off is the recovery that ended the
// previous episode (the latest relapse's recovered_at), not the reopen:
// every page of an earlier episode was built before that recovery, and every
// page of this one after the reopen, at least one evaluation later, so the
// second rounding and the gap between the database clock (these times) and
// the sender's (the link's) fall inside that gap instead of refusing a link
// built just after the reopen. An alert that never relapsed is unaffected.
func issuedInEarlierEpisode(issuedAt time.Time, alert *models.AlertWithDetails) bool {
	if alert.RelapseCount == 0 {
		return false
	}
	var cutoff time.Time
	if n := len(alert.Relapses); n > 0 {
		cutoff = alert.Relapses[n-1].RecoveredAt
	} else if alert.ReopenedAt != nil {
		// No history row to read (it is written with the reopen, so this is
		// defensive): the reopen, to the second the link carries.
		cutoff = alert.ReopenedAt.Truncate(time.Second)
	}
	return issuedAt.Before(cutoff)
}

// statusFor maps a state to its HTTP status. Refusals of the link itself are
// 404 (invalid) and 410 (expired). An already-acknowledged alert is 200 on
// both routes; a resolved one is 409 only on POST, which could not act.
func statusFor(state string, post bool) int {
	switch state {
	case StateInvalid:
		return http.StatusNotFound
	case StateExpired:
		return http.StatusGone
	case StateResolved:
		if post {
			return http.StatusConflict
		}
	}
	return http.StatusOK
}

func writeState(w http.ResponseWriter, l lookup, status int) {
	resp := Response{State: l.state}
	if l.state != StateInvalid && l.state != StateExpired && l.alert != nil {
		resp.Alert = summarize(l.alert)
		exp := l.claims.ExpiresAt
		resp.ExpiresAt = &exp
	}
	noStore(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

// noStore keeps the exchange out of caches, referrers and search indexes.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex")
}

func summarize(a *models.AlertWithDetails) *AlertSummary {
	details := notifications.AlertDetails{
		Kind:           a.Kind,
		Status:         string(a.Status),
		MetricName:     a.MetricName,
		MetricValue:    a.MetricValue,
		ThresholdValue: a.ThresholdValue,
	}
	s := &AlertSummary{
		Label:          present.Label(notifications.AlertEvent{Type: present.EventCreated, Alert: details}),
		Status:         string(a.Status),
		TriggeredAt:    a.TriggeredAt,
		AcknowledgedAt: a.AcknowledgedAt,
		OpenPath:       present.AlertPath(a.ID.String(), a.TenantID.String()),
	}
	name := func(p *string) string {
		if p == nil {
			return ""
		}
		return strings.TrimSpace(*p)
	}
	switch {
	case name(a.MonitorName) != "":
		s.Subject = name(a.MonitorName)
	case name(a.SourceLocationName) != "" && name(a.TargetLocationName) != "":
		s.Subject = name(a.SourceLocationName) + " → " + name(a.TargetLocationName)
	case name(a.SourceLocationName) != "":
		s.Subject = name(a.SourceLocationName)
	default:
		s.Subject = "Alert"
	}
	return s
}

// record audits the POST that acknowledged (and server errors on an
// authentic token) as alert.acknowledge on the alert, attributed to the link
// and the channel it was sent to rather than to a user. Repeat uses of a
// link ("already", "resolved", "expired") write nothing: one forwarded link
// opened many times must not flood the audit queue, which every workspace
// shares and which drops when full. Forged tokens name no trustworthy
// tenant, so they are never audited.
func (h *Handlers) record(r *http.Request, l lookup, status int) {
	if h.audit == nil || !l.authentic {
		return
	}
	if l.state != StateAcknowledged && status < http.StatusInternalServerError {
		return
	}
	tenantID := l.claims.TenantID
	event := audit.FromRequest(r)
	event.TenantID = &tenantID
	event.ActorLabel = "acknowledge link"
	if l.channelName != "" {
		event.ActorLabel = "link sent to " + l.channelName
	}
	event.Action = "alert.acknowledge"
	event.ResourceType = "alert"
	event.ResourceID = l.claims.AlertID.String()
	event.StatusCode = status
	event.Outcome = audit.OutcomeSuccess
	details := map[string]any{
		"via":         ViaLink,
		"channel_id":  l.claims.ChannelID.String(),
		"key_version": l.claims.KeyVersion,
	}
	if l.channelName != "" {
		details["channel_name"] = l.channelName
	}
	if status >= http.StatusInternalServerError {
		event.Outcome = audit.OutcomeFailure
		details["reason"] = "internal_error"
	}
	event.MergeDetails(details)
	h.audit.Record(event)
}

// internalError logs a database failure with the IDs the token named, never
// the token, and answers 500.
func (h *Handlers) internalError(w http.ResponseWriter, r *http.Request, l lookup, op string, err error) {
	fields := map[string]interface{}{"error": err.Error(), "op": op}
	if l.authentic {
		fields["tenant_id"] = l.claims.TenantID.String()
		fields["alert_id"] = l.claims.AlertID.String()
	}
	if h.log != nil {
		h.log.WithFields(fields).Error("Alert action failed")
	}
	if op == "acknowledge" {
		h.record(r, l, http.StatusInternalServerError)
	}
	noStore(w)
	apierrors.WriteInternalError(w, "alert action failed")
}
