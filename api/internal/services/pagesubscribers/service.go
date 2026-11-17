// Package pagesubscribers emails status-page subscribers.
//
// Visitors subscribe on the public page. The status-page service, read-only
// except for push, only forwards the form here (after its visibility gate
// and per-client throttle); the API alone writes subscribers. Subscribing is
// double opt-in: the address gets a confirmation mail with a signed link
// (shared/actionlink, ActionConfirmSubscription, 48 hours), and nothing else
// until it is followed. Every subscribe request answers the same whatever
// the address's state, and confirmation mail goes out from the Runner, never
// the request, so neither the answer nor its timing tells whether an
// address is subscribed.
//
// Subscriber mail is driven only by the notification outbox (pagenotify):
// the Runner plans each pending row once (one delivery per confirmed
// subscriber, committed before anyone is mailed) and sends with the
// delivery states of SLA report emails (claimed, then sending committed
// right before the send; uncertain only after a send started). Every message
// goes to one subscriber and carries their signed unsubscribe link
// (ActionUnsubscribePage) in the body and as List-Unsubscribe with RFC 8058
// one-click POST. Unsubscribing deletes the address.
//
// Subscriptions exist on public and unlisted pages only: a mail would carry
// a gated page's incident text to an address that once had access, past
// the page's own session and password checks.
//
// The Runner also publishes monitor outages to pages that opted in
// (autopublish.go) and announces the start of announced one-off maintenance.
package pagesubscribers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/shared/actionlink"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/mail"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

// Limits on the anonymous subscribe endpoint. The status-page service
// throttles each client before forwarding; these hold whoever calls.
const (
	// MaxSubscribersPerPage bounds one page's subscribers, pending and
	// confirmed: an anonymous endpoint must not become unbounded storage.
	MaxSubscribersPerPage = 10000
	// MaxNewPendingPerPagePerHour bounds new addresses a page takes in an
	// hour, so a flood cannot mail thousands of strangers in one go.
	MaxNewPendingPerPagePerHour = 100
	// MaxConfirmationsPerDay is how many confirmation mails one address gets
	// for one page in a day, however often it is submitted.
	MaxConfirmationsPerDay = 3
	// PendingRetention is how long an unconfirmed address is kept after its
	// last subscribe request.
	PendingRetention = 7 * 24 * time.Hour
	// maxAddressLen is the RFC 5321 path limit.
	maxAddressLen = 254
)

// Errors the handlers map to statuses.
var (
	// ErrNotFound: no such page (or subscriber) for the caller, or the page
	// takes no subscriptions. A private page answers exactly like this.
	ErrNotFound = errors.New("not found")
	// ErrLimit: the page is at a subscriber limit. Same for every address.
	ErrLimit = errors.New("this status page is not accepting new subscriptions right now")
)

// Invalid is a validation failure shown to the caller verbatim.
type Invalid struct{ Message string }

func (e Invalid) Error() string { return e.Message }

// Links mints and verifies confirmation and unsubscribe tokens (an
// *actionlink.Signer).
type Links interface {
	ActionToken(action, tenantID, alertID, channelID string, issuedAt time.Time) (string, error)
	Verify(token, action string) (actionlink.Claims, error)
}

// AuditRecorder records what happens outside an audited request.
type AuditRecorder interface {
	Record(event audit.Event)
}

// Publisher tells the status-page service a page changed (re-render it, and
// wake its push sender). *statusupdates.Publisher implements it.
type Publisher interface {
	Publish(event statusupdates.Event) error
}

// Config is what the API process provides. Mailer may be nil (no SMTP),
// Links nil (no PROBARA_SECRETS_KEY) and the base URLs empty: subscriptions
// then cannot be turned on, and automatic publishing still runs.
type Config struct {
	Mailer mail.Sender
	Links  *actionlink.Signer
	// AppBaseURL is the API's public origin as the operator UI proxies it
	// (APP_BASE_URL); List-Unsubscribe one-click POSTs go there.
	AppBaseURL string
	// StatusPageBaseURL is the public origin of status pages
	// (STATUS_PAGE_BASE_URL); every page link a mail carries is built from
	// it by pageURL.
	StatusPageBaseURL string
	Audit             AuditRecorder
	Publisher         Publisher
	Log               *logger.Logger
}

// Service stores subscribers and mails them.
type Service struct {
	db                *db.Client
	mailer            mail.Sender
	links             Links
	appBaseURL        string
	statusPageBaseURL string
	audit             AuditRecorder
	publisher         Publisher
	log               *logger.Logger
	now               func() time.Time
	// wake asks the Runner for an early pass (a confirmation is owed).
	wake chan struct{}
	// sendConcurrency bounds simultaneous sends in one delivery batch.
	sendConcurrency int
	// lastPrune is when the Runner last pruned; prunes run hourly.
	lastPrune time.Time
}

// NewService wires the service.
func NewService(dbClient *db.Client, cfg Config) *Service {
	s := &Service{
		db:                dbClient,
		mailer:            cfg.Mailer,
		appBaseURL:        strings.TrimRight(strings.TrimSpace(cfg.AppBaseURL), "/"),
		statusPageBaseURL: strings.TrimRight(strings.TrimSpace(cfg.StatusPageBaseURL), "/"),
		audit:             cfg.Audit,
		publisher:         cfg.Publisher,
		log:               cfg.Log,
		now:               func() time.Time { return time.Now().UTC() },
		wake:              make(chan struct{}, 1),
		sendConcurrency:   4,
	}
	// A nil *Signer in the interface would read as configured.
	if cfg.Links != nil {
		s.links = cfg.Links
	}
	return s
}

// MailProblems says why this API cannot mail status-page subscribers; empty
// when it can. Turning email subscriptions on is refused while it is not.
func (s *Service) MailProblems() []string {
	problems := []string{}
	if s.mailer == nil {
		problems = append(problems, "SMTP is not configured on the API: set SMTP_HOST and SMTP_FROM.")
	}
	if s.appBaseURL == "" {
		problems = append(problems, "APP_BASE_URL is not set on the API: one-click unsubscribe links point there.")
	}
	if s.statusPageBaseURL == "" {
		problems = append(problems, "STATUS_PAGE_BASE_URL is not set on the API: confirmation and unsubscribe links point to the page there.")
	}
	if s.links == nil {
		problems = append(problems, "PROBARA_SECRETS_KEY is not set on the API: confirmation and unsubscribe links are signed with it.")
	}
	return problems
}

// Ready reports whether subscriber mail can be sent.
func (s *Service) Ready() bool { return len(s.MailProblems()) == 0 }

// pageURL is the one place a subscriber mail or link builds a page's public
// URL: everything a subscriber is sent to goes through it.
func (s *Service) pageURL(slug string) string {
	if s.statusPageBaseURL == "" || slug == "" {
		return ""
	}
	return s.statusPageBaseURL + "/public/status/" + slug
}

// SubscriptionPath is the status-page route that renders confirmation and
// unsubscribe pages (status-page/internal/statuspage/subscribe.go). Tokens
// ride in the fragment, which browsers never send to a server.
const SubscriptionPath = "/subscription"

// OneClickUnsubscribePath is the API route List-Unsubscribe names. The RFC
// 8058 one-click POST cannot set a header, so the token is a query
// parameter, which the request log never records (it logs the path only).
// It is not under the page: it must keep working whatever the page's
// visibility becomes.
const OneClickUnsubscribePath = "/api/v1/public/status-page-subscriptions/unsubscribe"

// AddressHash is the SHA-256 of a normalized address, hex: the lookup key,
// and what audit events record instead of the address.
func AddressHash(address string) string {
	sum := sha256.Sum256([]byte(address))
	return hex.EncodeToString(sum[:])
}

// NormalizeAddress trims and lower-cases a submitted address and accepts
// only a bare plain address (mail.ParseRecipient's dot-atom rules, no
// display name, no quoted local part), at most 254 bytes.
func NormalizeAddress(raw string) (string, error) {
	addr := strings.ToLower(strings.TrimSpace(raw))
	if addr == "" {
		return "", Invalid{Message: "enter an email address"}
	}
	if len(addr) > maxAddressLen {
		return "", Invalid{Message: "that email address is too long"}
	}
	parsed, err := mail.ParseRecipient(addr)
	if err != nil || parsed.Address != addr || parsed.Name != "" || !strings.Contains(addr[strings.LastIndexByte(addr, '@')+1:], ".") {
		return "", Invalid{Message: "enter a plain email address, like name@example.com"}
	}
	return addr, nil
}

// subscribablePage is a page as the public endpoints see it.
type subscribablePage struct {
	id, tenantID uuid.UUID
	slug, title  string
	open         bool // public or unlisted, with email subscriptions on
}

const pageColumns = `sp.id, sp.tenant_id, sp.slug, sp.title,
	(sp.visibility IN ('public', 'unlisted') AND COALESCE(sp.settings->>'enable_email_subscriptions', 'false') = 'true')`

func scanPage(row interface{ Scan(...any) error }) (*subscribablePage, error) {
	var p subscribablePage
	if err := row.Scan(&p.id, &p.tenantID, &p.slug, &p.title, &p.open); err != nil {
		return nil, err
	}
	return &p, nil
}

// Subscribe takes a visitor's address for the page at slug. Every accepted
// request returns nil whatever the address's state (new, pending, confirmed,
// or unsubscribed before): a new or pending address is owed a confirmation
// mail (at most MaxConfirmationsPerDay a day), which the Runner sends; a
// confirmed one gets nothing. The page limits apply before the address is
// looked at, so they refuse every address alike.
func (s *Service) Subscribe(ctx context.Context, slug, rawAddress string) error {
	addr, err := NormalizeAddress(rawAddress)
	if err != nil {
		return err
	}
	page, err := scanPage(s.db.QueryRowContext(ctx, `SELECT `+pageColumns+` FROM status_pages sp WHERE sp.slug = $1`, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load status page: %w", err)
	}
	if !page.open || !s.Ready() {
		return ErrNotFound
	}
	now := s.now()
	err = withTx(ctx, s.db, func(tx *sql.Tx) error {
		// One subscribe at a time per page, so the limits below are exact.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "page-subscribe:"+page.id.String()); err != nil {
			return fmt.Errorf("lock page subscriptions: %w", err)
		}
		var total, recent int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*), COUNT(*) FILTER (WHERE state = 'pending' AND created_at > $2)
			FROM status_page_subscribers WHERE status_page_id = $1
		`, page.id, now.Add(-time.Hour)).Scan(&total, &recent); err != nil {
			return fmt.Errorf("count subscribers: %w", err)
		}
		if total >= MaxSubscribersPerPage || recent >= MaxNewPendingPerPagePerHour {
			return ErrLimit
		}
		// New: pending, one confirmation owed. Pending: owed another one
		// unless one is already waiting or the day's budget is spent.
		// Confirmed: nothing changes.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO status_page_subscribers AS s
				(tenant_id, status_page_id, email, email_hash, state, requested_at, created_at,
				 confirm_due_at, confirm_window_start, confirm_window_sends)
			VALUES ($1, $2, $3, $4, 'pending', $5, $5, $5, $5, 1)
			ON CONFLICT ON CONSTRAINT status_page_subscribers_page_email DO UPDATE SET
				requested_at = EXCLUDED.requested_at,
				confirm_window_start = CASE
					WHEN s.confirm_due_at IS NULL AND (s.confirm_window_start IS NULL OR s.confirm_window_start <= $5 - INTERVAL '1 day')
					THEN $5 ELSE s.confirm_window_start END,
				confirm_window_sends = CASE
					WHEN s.confirm_due_at IS NOT NULL THEN s.confirm_window_sends
					WHEN s.confirm_window_start IS NULL OR s.confirm_window_start <= $5 - INTERVAL '1 day' THEN 1
					WHEN s.confirm_window_sends < $6 THEN s.confirm_window_sends + 1
					ELSE s.confirm_window_sends END,
				confirm_due_at = CASE
					WHEN s.confirm_due_at IS NOT NULL THEN s.confirm_due_at
					WHEN s.confirm_window_start IS NULL OR s.confirm_window_start <= $5 - INTERVAL '1 day' THEN $5
					WHEN s.confirm_window_sends < $6 THEN $5
					ELSE NULL END,
				confirm_attempts = CASE WHEN s.confirm_due_at IS NULL THEN 0 ELSE s.confirm_attempts END
			WHERE s.state = 'pending'
		`, page.tenantID, page.id, addr, AddressHash(addr), now, MaxConfirmationsPerDay); err != nil {
			return fmt.Errorf("store subscriber: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.Wake()
	return nil
}

// Wake asks the Runner for an early pass (a confirmation is owed). It never
// blocks, and a dropped wake only waits for the next tick.
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// resolveToken verifies a subscription token for action. Every refusal is a
// state; only a database failure is an error.
func (s *Service) resolveToken(ctx context.Context, token, action string) (actionlink.Claims, *subscribablePage, string, error) {
	token = strings.TrimSpace(token)
	if s.links == nil || token == "" {
		return actionlink.Claims{}, nil, models.StatusPageSubscriptionInvalid, nil
	}
	claims, err := s.links.Verify(token, action)
	switch {
	case errors.Is(err, actionlink.ErrExpired):
		return claims, nil, models.StatusPageSubscriptionExpired, nil
	case err != nil:
		return claims, nil, models.StatusPageSubscriptionInvalid, nil
	}
	page, err := scanPage(s.db.QueryRowContext(ctx, `SELECT `+pageColumns+` FROM status_pages sp WHERE sp.id = $1 AND sp.tenant_id = $2`,
		claims.AlertID, claims.TenantID))
	if errors.Is(err, sql.ErrNoRows) {
		// A deleted page took its subscribers with it.
		return claims, nil, models.StatusPageSubscriptionInvalid, nil
	}
	if err != nil {
		return claims, nil, "", fmt.Errorf("load status page: %w", err)
	}
	return claims, page, "", nil
}

// Confirm follows a confirmation link: the pending subscriber it names is
// confirmed. Following it again is a no-op that answers confirmed; a link
// whose subscriber is gone (unsubscribed, pruned, removed) is invalid.
func (s *Service) Confirm(ctx context.Context, token string, event audit.Event) (models.StatusPageSubscriptionResponse, error) {
	claims, page, state, err := s.resolveToken(ctx, token, actionlink.ActionConfirmSubscription)
	if err != nil || state != "" {
		return models.StatusPageSubscriptionResponse{State: state}, err
	}
	var current, hash string
	err = s.db.QueryRowContext(ctx, `
		WITH confirmed AS (
			UPDATE status_page_subscribers
			SET state = 'confirmed', confirmed_at = $4, confirm_due_at = NULL, confirm_claim_id = NULL, confirm_claimed_at = NULL
			WHERE id = $1 AND tenant_id = $2 AND status_page_id = $3 AND state = 'pending'
			RETURNING email_hash
		)
		SELECT 'changed', email_hash FROM confirmed
		UNION ALL
		SELECT state, email_hash FROM status_page_subscribers
		WHERE id = $1 AND tenant_id = $2 AND status_page_id = $3 AND NOT EXISTS (SELECT 1 FROM confirmed)
	`, claims.ChannelID, claims.TenantID, page.id, s.now()).Scan(&current, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return models.StatusPageSubscriptionResponse{State: models.StatusPageSubscriptionInvalid}, nil
	}
	if err != nil {
		return models.StatusPageSubscriptionResponse{}, fmt.Errorf("confirm subscriber: %w", err)
	}
	if current == "changed" {
		s.record(event, claims, "status-page.subscriber-confirm", "confirmation link", hash)
	}
	return models.StatusPageSubscriptionResponse{State: models.StatusPageSubscriptionConfirmed, PageTitle: page.title, PageSlug: page.slug}, nil
}

// Unsubscribe follows an unsubscribe link (or a mail client's one-click
// POST): the subscriber is deleted, with whatever was still waiting to be
// sent to them. Repeating it answers unsubscribed.
func (s *Service) Unsubscribe(ctx context.Context, token string, event audit.Event) (models.StatusPageSubscriptionResponse, error) {
	claims, page, state, err := s.resolveToken(ctx, token, actionlink.ActionUnsubscribePage)
	if err != nil || state != "" {
		return models.StatusPageSubscriptionResponse{State: state}, err
	}
	var hash string
	err = s.db.QueryRowContext(ctx, `
		DELETE FROM status_page_subscribers WHERE id = $1 AND tenant_id = $2 AND status_page_id = $3 RETURNING email_hash
	`, claims.ChannelID, claims.TenantID, page.id).Scan(&hash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return models.StatusPageSubscriptionResponse{}, fmt.Errorf("unsubscribe: %w", err)
	default:
		s.record(event, claims, "status-page.subscriber-unsubscribe", "unsubscribe link", hash)
	}
	return models.StatusPageSubscriptionResponse{State: models.StatusPageSubscriptionUnsubscribed, PageTitle: page.title, PageSlug: page.slug}, nil
}

// UnsubscribePageURL is where a browser that opened the one-click URL itself
// (GET) is sent: the page's unsubscribe confirmation, token in the
// fragment. "" when the token does not verify.
func (s *Service) UnsubscribePageURL(ctx context.Context, token string) (string, error) {
	_, page, state, err := s.resolveToken(ctx, token, actionlink.ActionUnsubscribePage)
	if err != nil || page == nil || state != "" {
		return "", err
	}
	base := s.pageURL(page.slug)
	if base == "" {
		return "", nil
	}
	return base + SubscriptionPath + "?action=unsubscribe#" + strings.TrimSpace(token), nil
}

// record audits a public subscriber change on the page, attributed to the
// link. The address hash stands in for the address.
func (s *Service) record(event audit.Event, claims actionlink.Claims, action, label, hash string) {
	if s.audit == nil {
		return
	}
	tenantID := claims.TenantID
	event.TenantID = &tenantID
	event.ActorType = audit.ActorAnonymous
	event.ActorLabel = label
	event.Action = action
	event.ResourceType = "status-page"
	event.ResourceID = claims.AlertID.String()
	event.Outcome = audit.OutcomeSuccess
	event.MergeDetails(map[string]any{"address_hash": hash, "key_version": claims.KeyVersion})
	s.audit.Record(event)
}

// ensurePage checks the page is the tenant's.
func (s *Service) ensurePage(ctx context.Context, tenantID, pageID uuid.UUID) error {
	var ok bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM status_pages WHERE id = $1 AND tenant_id = $2)`, pageID, tenantID).Scan(&ok); err != nil {
		return fmt.Errorf("load status page: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// List returns a page of the page's subscribers (newest first) with its
// counts and whether mail is configured.
func (s *Service) List(ctx context.Context, tenantID, pageID uuid.UUID, page, pageSize int) (*models.StatusPageSubscriberListResponse, error) {
	if err := s.ensurePage(ctx, tenantID, pageID); err != nil {
		return nil, err
	}
	resp := &models.StatusPageSubscriberListResponse{
		Items: []models.StatusPageSubscriber{}, Page: page, PageSize: pageSize,
		Limit: MaxSubscribersPerPage, MailProblems: s.MailProblems(),
	}
	resp.MailReady = len(resp.MailProblems) == 0
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FILTER (WHERE state = 'confirmed'), COUNT(*) FILTER (WHERE state = 'pending')
		FROM status_page_subscribers WHERE tenant_id = $1 AND status_page_id = $2
	`, tenantID, pageID).Scan(&resp.Counts.Confirmed, &resp.Counts.Pending); err != nil {
		return nil, fmt.Errorf("count subscribers: %w", err)
	}
	resp.Total = resp.Counts.Confirmed + resp.Counts.Pending
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, email, state, created_at, requested_at, confirmed_at
		FROM status_page_subscribers
		WHERE tenant_id = $1 AND status_page_id = $2
		ORDER BY created_at DESC, id
		LIMIT $3 OFFSET $4
	`, tenantID, pageID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, fmt.Errorf("list subscribers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sub models.StatusPageSubscriber
		var confirmed sql.NullTime
		if err := rows.Scan(&sub.ID, &sub.Email, &sub.State, &sub.CreatedAt, &sub.RequestedAt, &confirmed); err != nil {
			return nil, fmt.Errorf("scan subscriber: %w", err)
		}
		if confirmed.Valid {
			t := confirmed.Time
			sub.ConfirmedAt = &t
		}
		resp.Items = append(resp.Items, sub)
	}
	return resp, rows.Err()
}

// Remove deletes one subscriber of the page (and whatever was still waiting
// to be sent to them).
func (s *Service) Remove(ctx context.Context, tenantID, pageID, subscriberID uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM status_page_subscribers WHERE id = $1 AND tenant_id = $2 AND status_page_id = $3`,
		subscriberID, tenantID, pageID)
	if err != nil {
		return fmt.Errorf("remove subscriber: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ExportCSV writes the page's subscribers as CSV (email, state,
// subscribed_at, confirmed_at), oldest first, and returns how many.
func (s *Service) ExportCSV(ctx context.Context, tenantID, pageID uuid.UUID, w io.Writer) (int, error) {
	if err := s.ensurePage(ctx, tenantID, pageID); err != nil {
		return 0, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT email, state, created_at, confirmed_at
		FROM status_page_subscribers
		WHERE tenant_id = $1 AND status_page_id = $2
		ORDER BY created_at, id
	`, tenantID, pageID)
	if err != nil {
		return 0, fmt.Errorf("export subscribers: %w", err)
	}
	defer rows.Close()
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"email", "state", "subscribed_at", "confirmed_at"}); err != nil {
		return 0, err
	}
	n := 0
	for rows.Next() {
		var email, state string
		var created time.Time
		var confirmed sql.NullTime
		if err := rows.Scan(&email, &state, &created, &confirmed); err != nil {
			return n, fmt.Errorf("scan subscriber: %w", err)
		}
		confirmedAt := ""
		if confirmed.Valid {
			confirmedAt = confirmed.Time.UTC().Format(time.RFC3339)
		}
		if err := cw.Write([]string{csvCell(email), state, created.UTC().Format(time.RFC3339), confirmedAt}); err != nil {
			return n, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	cw.Flush()
	return n, cw.Error()
}

// csvCell defuses a cell a spreadsheet would run as a formula: a dot-atom
// address may begin with =, +, - or @.
func csvCell(v string) string {
	if v != "" && strings.ContainsRune("=+-@", rune(v[0])) {
		return "'" + v
	}
	return v
}

func withTx(ctx context.Context, d *db.Client, fn func(*sql.Tx) error) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
