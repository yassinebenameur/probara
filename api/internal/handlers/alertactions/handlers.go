// Package alertactions serves the signed one-click alert actions that
// notifications link to (shared/actionlink): GET /api/v1/alert-actions/{token}
// describes the alert, POST acknowledges it.
//
// The routes are public: the token is the credential, bound to one tenant,
// alert and channel and valid for 72 hours. GET never changes anything, because
// mail scanners and chat unfurlers prefetch every link they see; the operator
// UI's confirm page (/alert-action/<token>) POSTs on a button press. The
// routes sit outside the audited protected group, so the POST records its own
// audit event.
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
	"github.com/yassinebenameur/probara/shared/tenancy"
)

// States a response reports in "state".
const (
	// StatePending: the alert is active and can be acknowledged (GET only).
	StatePending = "pending"
	// StateAcknowledged: this request acknowledged it (POST only).
	StateAcknowledged = "acknowledged"
	// StateAlready: someone acknowledged it before.
	StateAlready = "already"
	// StateResolved: the alert is closed; there is nothing to acknowledge.
	StateResolved = "resolved"
	// StateExpired: an authentic token past its lifetime.
	StateExpired = "expired"
	// StateInvalid: a forged, malformed or revoked token (retired key
	// version, deleted channel, alert or workspace).
	StateInvalid = "invalid"
)

// Verifier checks a token (an *actionlink.Signer).
type Verifier interface {
	Verify(token, action string) (actionlink.Claims, error)
}

// Alerts is the slice of the alerts service the routes use.
type Alerts interface {
	GetAlert(ctx context.Context, tenantID, alertID uuid.UUID) (*models.AlertWithDetails, error)
	AcknowledgeAlert(ctx context.Context, tenantID, alertID uuid.UUID, actor alertservice.Actor) (*models.AlertWithDetails, error)
}

// Broadcaster fans an acknowledgement out to open alert streams.
type Broadcaster interface {
	BroadcastAlertAcknowledged(tenantID uuid.UUID, alert *models.AlertWithDetails)
}

// Scope reports whether the workspace and channel a token names still
// stand. A deleted workspace's links die with it, before the purger drains
// it, and deleting a channel revokes every link it delivered.
type Scope interface {
	Live(ctx context.Context, tenantID, channelID uuid.UUID) (bool, error)
}

// DBScope is the Scope backed by the database.
func DBScope(q db.Querier) Scope { return dbScope{q: q} }

type dbScope struct{ q db.Querier }

func (s dbScope) Live(ctx context.Context, tenantID, channelID uuid.UUID) (bool, error) {
	deleted, err := tenancy.IsDeleted(ctx, s.q, tenantID)
	if err != nil || deleted {
		return false, err
	}
	var exists bool
	err = s.q.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM alert_channels WHERE id = $1 AND tenant_id = $2)`,
		channelID, tenantID).Scan(&exists)
	if err != nil || exists {
		return exists, err
	}
	// An escalation-policy page is scoped to the contact it went to
	// (plugin.DispatchRequest.View); deleting the contact, disabling its
	// user or removing them from the workspace revokes its links.
	owner, err := s.Owner(ctx, tenantID, channelID)
	return owner != nil, err
}

// ContactOwner is implemented by a Scope that can name the user a
// contact-scoped token was delivered to, so the acknowledgement is
// attributed to them.
type ContactOwner interface {
	Owner(ctx context.Context, tenantID, contactID uuid.UUID) (*uuid.UUID, error)
}

// Owner returns the user whose verified contact contactID is, when that
// user can still be paged in the workspace; nil otherwise.
func (s dbScope) Owner(ctx context.Context, tenantID, contactID uuid.UUID) (*uuid.UUID, error) {
	var userID uuid.UUID
	err := s.q.QueryRowContext(ctx, `
		SELECT u.id FROM user_contacts c
		JOIN admin_users u ON u.id = c.user_id
		WHERE c.id = $1 AND c.verified_at IS NOT NULL AND u.disabled_at IS NULL
		  AND (u.platform_role = 'superadmin' OR EXISTS (
			SELECT 1 FROM tenant_memberships m WHERE m.admin_user_id = u.id AND m.tenant_id = $2))`,
		contactID, tenantID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &userID, nil
}

// AuditRecorder records the acknowledge attempts.
type AuditRecorder interface {
	Record(event audit.Event)
}

// Handlers serves the alert-action routes.
type Handlers struct {
	links  Verifier
	alerts Alerts
	scope  Scope
	hub    Broadcaster
	audit  AuditRecorder
	log    *logger.Logger
}

// NewHandlers wires the routes. links may be a nil *actionlink.Signer (no
// PROBARA_SECRETS_KEY): every token is then invalid, matching the alerter,
// which mints none. hub and recorder may be nil.
func NewHandlers(links Verifier, alerts Alerts, scope Scope, hub Broadcaster, recorder AuditRecorder, log *logger.Logger) *Handlers {
	return &Handlers{links: links, alerts: alerts, scope: scope, hub: hub, audit: recorder, log: log}
}

// Response is the body of both routes.
type Response struct {
	State     string        `json:"state"`
	Alert     *AlertSummary `json:"alert,omitempty"`
	ExpiresAt *time.Time    `json:"expires_at,omitempty"`
}

// AlertSummary is the little a link holder learns about the alert: what the
// notification they hold already said.
type AlertSummary struct {
	Subject        string     `json:"subject"`
	Kind           string     `json:"kind"`
	Status         string     `json:"status"`
	TriggeredAt    time.Time  `json:"triggered_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
}

// lookup is a verified token and the alert it names.
type lookup struct {
	claims    actionlink.Claims
	authentic bool // the signature verified (claims are trustworthy)
	alert     *models.AlertWithDetails
	state     string
}

// Describe handles GET /api/v1/alert-actions/{token}. It never acts.
func (h *Handlers) Describe(w http.ResponseWriter, r *http.Request) {
	l, err := h.resolve(r)
	if err != nil {
		h.internalError(w, r, l, "describe", err)
		return
	}
	writeState(w, l, statusFor(l.state, false))
}

// Acknowledge handles POST /api/v1/alert-actions/{token}: it acknowledges the
// alert as a magic-link actor (no user) when the token is valid and the alert
// still active.
func (h *Handlers) Acknowledge(w http.ResponseWriter, r *http.Request) {
	l, err := h.resolve(r)
	if err != nil {
		h.internalError(w, r, l, "acknowledge", err)
		return
	}
	reason := ""
	if l.state == StatePending {
		actor := alertservice.Actor{Via: alertservice.ViaMagicLink}
		if owners, ok := h.scope.(ContactOwner); ok {
			// A link from an escalation page acknowledges as the person paged.
			if uid, err := owners.Owner(r.Context(), l.claims.TenantID, l.claims.ChannelID); err == nil {
				actor.UserID = uid
			}
		}
		acked, err := h.alerts.AcknowledgeAlert(r.Context(), l.claims.TenantID, l.claims.AlertID, actor)
		switch {
		case err == nil:
			l.alert, l.state = acked, StateAcknowledged
			if h.hub != nil {
				h.hub.BroadcastAlertAcknowledged(l.claims.TenantID, acked)
			}
		case strings.Contains(err.Error(), "not found or not active"):
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
	if l.state != StateAcknowledged {
		reason = l.state
	}
	status := statusFor(l.state, true)
	h.record(r, l, status, reason)
	writeState(w, l, status)
}

// resolve verifies the token and loads the alert it names. Only an
// unexpected failure (database) is an error; every refusal is a state.
func (h *Handlers) resolve(r *http.Request) (lookup, error) {
	claims, err := h.links.Verify(chi.URLParam(r, "token"), actionlink.ActionAcknowledge)
	switch {
	case errors.Is(err, actionlink.ErrExpired):
		return lookup{claims: claims, authentic: true, state: StateExpired}, nil
	case err != nil:
		return lookup{state: StateInvalid}, nil
	}
	l := lookup{claims: claims, authentic: true}

	live, err := h.scope.Live(r.Context(), claims.TenantID, claims.ChannelID)
	if err != nil {
		return l, err
	}
	if !live {
		l.state = StateInvalid
		return l, nil
	}
	return h.reload(r.Context(), l)
}

// reload reads the alert in the token's tenant and derives the state.
func (h *Handlers) reload(ctx context.Context, l lookup) (lookup, error) {
	alert, err := h.alerts.GetAlert(ctx, l.claims.TenantID, l.claims.AlertID)
	if err != nil {
		if err.Error() == "alert not found" {
			l.alert, l.state = nil, StateInvalid
			return l, nil
		}
		return l, err
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

// statusFor maps a state to its HTTP status. GET reports every alert state
// with 200; POST answers 200 only for the acknowledgement it made.
func statusFor(state string, post bool) int {
	switch state {
	case StateInvalid:
		return http.StatusNotFound
	case StateExpired:
		return http.StatusGone
	case StateAlready, StateResolved:
		if post {
			return http.StatusConflict
		}
	}
	return http.StatusOK
}

func writeState(w http.ResponseWriter, l lookup, status int) {
	resp := Response{State: l.state}
	if l.alert != nil {
		resp.Alert = summarize(l.alert)
	}
	if l.authentic && l.state != StateInvalid {
		exp := l.claims.ExpiresAt
		resp.ExpiresAt = &exp
	}
	noStore(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

// noStore keeps the token-bearing exchange out of caches and referrers.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex")
}

func summarize(a *models.AlertWithDetails) *AlertSummary {
	s := &AlertSummary{
		Kind:           a.Kind,
		Status:         string(a.Status),
		TriggeredAt:    a.TriggeredAt,
		AcknowledgedAt: a.AcknowledgedAt,
	}
	switch {
	case a.MonitorName != nil && strings.TrimSpace(*a.MonitorName) != "":
		s.Subject = strings.TrimSpace(*a.MonitorName)
	case a.SourceLocationName != nil && a.TargetLocationName != nil:
		s.Subject = *a.SourceLocationName + " → " + *a.TargetLocationName
	default:
		s.Subject = "Alert"
	}
	return s
}

// record audits an acknowledge attempt carrying an authentic token. Forged
// tokens name no trustworthy tenant, so they are only rate limited.
func (h *Handlers) record(r *http.Request, l lookup, status int, reason string) {
	if h.audit == nil || !l.authentic {
		return
	}
	tenantID := l.claims.TenantID
	event := audit.FromRequest(r)
	event.TenantID = &tenantID
	event.ActorLabel = "magic link"
	event.Action = "alert.acknowledge"
	event.ResourceType = "alert"
	event.ResourceID = l.claims.AlertID.String()
	event.StatusCode = status
	event.Outcome = audit.OutcomeSuccess
	details := map[string]any{
		"via":         alertservice.ViaMagicLink,
		"channel_id":  l.claims.ChannelID.String(),
		"key_version": l.claims.KeyVersion,
	}
	if status >= 400 {
		event.Outcome = audit.OutcomeFailure
		details["reason"] = reason
	}
	event.MergeDetails(details)
	h.audit.Record(event)
}

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
		h.record(r, l, http.StatusInternalServerError, "internal error")
	}
	noStore(w)
	apierrors.WriteInternalError(w, "alert action failed")
}
