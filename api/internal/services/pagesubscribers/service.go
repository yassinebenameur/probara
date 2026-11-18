// Package pagesubscribers emails status-page subscribers.
//
// Visitors subscribe on the public page. The status-page service, read-only
// except for push, only forwards the form here (after its visibility gate
// and per-client throttle), presenting STATUS_PAGE_INTERNAL_TOKEN; the
// subscribe and confirm routes refuse any other caller, so the gate and the
// throttle cannot be bypassed by calling the API directly. The API alone
// writes subscribers. Subscribing is double opt-in: the address gets a
// confirmation mail with a signed link (shared/actionlink,
// ActionConfirmSubscription, 48 hours), and nothing else until it is
// followed. Every subscribe request answers the same whatever the address's
// state, and confirmation mail goes out from the Runner, never the request,
// so neither the answer nor its timing tells whether an address is
// subscribed.
//
// Subscriber mail is driven only by the notification outbox (pagenotify):
// the Runner plans each pending row once (one delivery per confirmed
// subscriber, committed before anyone is mailed) and sends with the
// delivery states of SLA report emails (claimed, then sending committed
// right before the send; uncertain only after a send started). Every message
// goes to one subscriber, in the page's name, and carries their signed
// unsubscribe link (ActionUnsubscribePage) in the body and as
// List-Unsubscribe with RFC 8058 one-click POST. Unsubscribing deletes the
// address, whatever the page's visibility has become.
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
	"crypto/subtle"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/shared/actionlink"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/mail"
	"github.com/yassinebenameur/probara/shared/pagedomains"
	"github.com/yassinebenameur/probara/shared/statusupdates"
	"github.com/yassinebenameur/probara/shared/tenancy"
)

// Limits on the subscribe endpoint. The status-page service throttles each
// client before forwarding (and only it may call); these bound what any
// number of clients can do together.
const (
	// MaxSubscribersPerPage bounds one page's subscribers, pending and
	// confirmed: an anonymous endpoint must not become unbounded storage.
	MaxSubscribersPerPage = 10000
	// MaxNewPerPagePerHour bounds the addresses a page takes in an hour,
	// whatever became of them since (confirmed, unsubscribed or pending), so
	// confirming addresses as fast as they arrive cannot fill a page.
	MaxNewPerPagePerHour = 100
	// MaxPendingPerPage bounds the addresses waiting for confirmation.
	MaxPendingPerPage = 500
	// MaxConfirmationsPerDay is how many confirmation mails one address gets
	// for one page in a day, however often it is submitted.
	MaxConfirmationsPerDay = 3
	// MaxConfirmationsPerInboxPerDay bounds the confirmation mails one inbox
	// gets in a day across every page, its +tag aliases counted together.
	MaxConfirmationsPerInboxPerDay = 5
	// MaxConfirmationsPerHour bounds all confirmation mail of the
	// installation in an hour; past it, confirmations wait for the next
	// hour (notices are never held back by it).
	MaxConfirmationsPerHour = 500
	// PendingRetention is how long an unconfirmed address is kept after its
	// last subscribe request.
	PendingRetention = 7 * 24 * time.Hour
	// maxAddressLen is the RFC 5321 path limit.
	maxAddressLen = 254
	// wakeEvery bounds how often a subscribe may wake the Runner early.
	wakeEvery = 5 * time.Second
	// maxBulkDomainLen bounds a bulk-remove domain.
	maxBulkDomainLen = 253
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
// Links nil (no PROBARA_SECRETS_KEY) and the base URLs and token empty:
// subscriptions then cannot be turned on, and automatic publishing still
// runs.
type Config struct {
	Mailer mail.Sender
	Links  *actionlink.Signer
	// AppBaseURL is the API's public origin as the operator UI proxies it
	// (APP_BASE_URL); List-Unsubscribe one-click POSTs go there.
	AppBaseURL string
	// StatusPageBaseURL is the public origin of status pages
	// (STATUS_PAGE_BASE_URL); every page link a mail carries is built from
	// it by publicPageURL (links.go).
	StatusPageBaseURL string
	// InternalToken is STATUS_PAGE_INTERNAL_TOKEN, which the status-page
	// service presents on the requests it forwards.
	InternalToken string
	Audit         AuditRecorder
	Publisher     Publisher
	Log           *logger.Logger
}

// Service stores subscribers and mails them.
type Service struct {
	db                *db.Client
	mailer            mail.Sender
	links             Links
	appBaseURL        string
	statusPageBaseURL string
	internalToken     string
	audit             AuditRecorder
	publisher         Publisher
	log               *logger.Logger
	now               func() time.Time
	// wake asks the Runner for an early pass (a confirmation is owed);
	// lastWake rate-limits it.
	wake     chan struct{}
	wakeMu   sync.Mutex
	lastWake time.Time
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
		internalToken:     strings.TrimSpace(cfg.InternalToken),
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
// when it can. The Runner sends only when it is empty.
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

// SubscriptionProblems is MailProblems plus what visitors need to reach the
// subscribe route at all; turning email subscriptions on is refused while
// it is not empty.
func (s *Service) SubscriptionProblems() []string {
	problems := s.MailProblems()
	if s.internalToken == "" {
		problems = append(problems, "STATUS_PAGE_INTERNAL_TOKEN is not set on the API: the status-page service presents it when it forwards a visitor's subscription.")
	}
	return problems
}

// Ready reports whether subscriber mail can be sent.
func (s *Service) Ready() bool { return len(s.MailProblems()) == 0 }

// ForwardedByStatusPage reports whether a request carries the status-page
// service's internal token. Without a configured token nothing does.
func (s *Service) ForwardedByStatusPage(presented string) bool {
	presented = strings.TrimSpace(presented)
	return s.internalToken != "" && presented != "" &&
		subtle.ConstantTimeCompare([]byte(presented), []byte(s.internalToken)) == 1
}

// AddressHash is the SHA-256 of a normalized address, hex: the lookup key,
// and what audit events record instead of the address.
func AddressHash(address string) string {
	sum := sha256.Sum256([]byte(address))
	return hex.EncodeToString(sum[:])
}

// InboxHash is the SHA-256 of the inbox a normalized address reaches: its
// local part without a +tag. Confirmation mail is capped per inbox, so
// name+1@, name+2@ … cannot flood one mailbox. Provider-specific rules
// (dots ignored by some) are not applied: they are not universal.
func InboxHash(address string) string {
	at := strings.LastIndexByte(address, '@')
	local, domain := address[:at], address[at+1:]
	if i := strings.IndexByte(local, '+'); i > 0 {
		local = local[:i]
	}
	return AddressHash(local + "@" + domain)
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
	pageRef
	title string
	open  bool // public or unlisted, with email subscriptions on
}

// pageColumns are a page as the public endpoints read it; pageLive limits
// them to pages of a workspace that is not deleted (a deleted workspace's
// pages take, confirm and unsubscribe nothing: they are purged).
var pageColumns = `sp.id, sp.tenant_id, sp.slug, sp.title,
	(sp.visibility IN ('public', 'unlisted') AND COALESCE(sp.settings->>'enable_email_subscriptions', 'false') = 'true'),
	COALESCE(` + pagedomains.PrimaryHostSQL("sp.id") + `, '')`

var pageLive = tenancy.LiveTenantSQL("sp.tenant_id")

func scanPage(row interface{ Scan(...any) error }) (*subscribablePage, error) {
	var p subscribablePage
	if err := row.Scan(&p.id, &p.tenantID, &p.slug, &p.title, &p.open, &p.primaryHost); err != nil {
		return nil, err
	}
	return &p, nil
}

// Subscribe takes a visitor's address for the page at slug. Every accepted
// request returns nil whatever the address's state (new, pending, confirmed,
// or unsubscribed before): a new or pending address is owed a confirmation
// mail (at most MaxConfirmationsPerDay a day for the page and
// MaxConfirmationsPerInboxPerDay for its inbox), which the Runner sends; a
// confirmed one gets nothing. The page limits apply before the address is
// looked at, so they refuse every address alike.
func (s *Service) Subscribe(ctx context.Context, slug, rawAddress string) error {
	addr, err := NormalizeAddress(rawAddress)
	if err != nil {
		return err
	}
	page, err := scanPage(s.db.QueryRowContext(ctx, `SELECT `+pageColumns+` FROM status_pages sp WHERE sp.slug = $1 AND `+pageLive, slug))
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
	inbox := InboxHash(addr)
	queued := false
	err = withTx(ctx, s.db, func(tx *sql.Tx) error {
		// One subscribe at a time per page, so the limits below are exact.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "page-subscribe:"+page.id.String()); err != nil {
			return fmt.Errorf("lock page subscriptions: %w", err)
		}
		var total, recent, pending int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*), COUNT(*) FILTER (WHERE created_at > $2), COUNT(*) FILTER (WHERE state = 'pending')
			FROM status_page_subscribers WHERE status_page_id = $1
		`, page.id, now.Add(-time.Hour)).Scan(&total, &recent, &pending); err != nil {
			return fmt.Errorf("count subscribers: %w", err)
		}
		if total >= MaxSubscribersPerPage || recent >= MaxNewPerPagePerHour || pending >= MaxPendingPerPage {
			return ErrLimit
		}
		// The inbox's budget: confirmations sent to it in the last day
		// (any page), plus those still owed.
		var inboxUsed int
		if err := tx.QueryRowContext(ctx, `
			SELECT (SELECT COUNT(*) FROM status_page_confirmation_sends WHERE inbox_hash = $1 AND sent_at > $2)
			     + (SELECT COUNT(*) FROM status_page_subscribers WHERE inbox_hash = $1 AND confirm_due_at IS NOT NULL
			        AND `+tenancy.LiveTenantSQL("status_page_subscribers.tenant_id")+`)
		`, inbox, now.Add(-24*time.Hour)).Scan(&inboxUsed); err != nil {
			return fmt.Errorf("count inbox confirmations: %w", err)
		}
		mayQueue := inboxUsed < MaxConfirmationsPerInboxPerDay
		// New: pending, one confirmation owed (inbox budget allowing).
		// Pending: owed another one unless one is already waiting or a
		// budget is spent. Confirmed: nothing changes (no row returned).
		err := tx.QueryRowContext(ctx, `
			INSERT INTO status_page_subscribers AS s
				(tenant_id, status_page_id, email, email_hash, inbox_hash, state, requested_at, created_at,
				 confirm_due_at, confirm_window_start, confirm_window_sends)
			VALUES ($1, $2, $3, $4, $8, 'pending', $5, $5,
				CASE WHEN $7 THEN $5::timestamptz END, $5, CASE WHEN $7 THEN 1 ELSE 0 END)
			ON CONFLICT ON CONSTRAINT status_page_subscribers_page_email DO UPDATE SET
				requested_at = EXCLUDED.requested_at,
				confirm_window_start = CASE
					WHEN s.confirm_due_at IS NULL AND $7 AND (s.confirm_window_start IS NULL OR s.confirm_window_start <= $5 - INTERVAL '1 day')
					THEN $5 ELSE s.confirm_window_start END,
				confirm_window_sends = CASE
					WHEN s.confirm_due_at IS NOT NULL OR NOT $7 THEN s.confirm_window_sends
					WHEN s.confirm_window_start IS NULL OR s.confirm_window_start <= $5 - INTERVAL '1 day' THEN 1
					WHEN s.confirm_window_sends < $6 THEN s.confirm_window_sends + 1
					ELSE s.confirm_window_sends END,
				confirm_due_at = CASE
					WHEN s.confirm_due_at IS NOT NULL THEN s.confirm_due_at
					WHEN NOT $7 THEN NULL
					WHEN s.confirm_window_start IS NULL OR s.confirm_window_start <= $5 - INTERVAL '1 day' THEN $5
					WHEN s.confirm_window_sends < $6 THEN $5
					ELSE NULL END,
				confirm_attempts = CASE WHEN s.confirm_due_at IS NULL THEN 0 ELSE s.confirm_attempts END
			WHERE s.state = 'pending'
			RETURNING confirm_due_at IS NOT DISTINCT FROM $5::timestamptz
		`, page.tenantID, page.id, addr, AddressHash(addr), now, MaxConfirmationsPerDay, mayQueue, inbox).Scan(&queued)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // confirmed already
		}
		if err != nil {
			return fmt.Errorf("store subscriber: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if queued {
		s.Wake()
	}
	return nil
}

// Wake asks the Runner for an early pass (a confirmation is owed). It never
// blocks, wakes at most once per wakeEvery, and a dropped wake only waits
// for the next tick.
func (s *Service) Wake() {
	s.wakeMu.Lock()
	now := time.Now()
	if now.Sub(s.lastWake) < wakeEvery {
		s.wakeMu.Unlock()
		return
	}
	s.lastWake = now
	s.wakeMu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// resolveToken verifies a subscription token for action. Every refusal is a
// state; only a database failure is an error. The page's visibility is not
// checked: unsubscribing works whatever it became.
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
	page, err := scanPage(s.db.QueryRowContext(ctx, `SELECT `+pageColumns+` FROM status_pages sp WHERE sp.id = $1 AND sp.tenant_id = $2 AND `+pageLive,
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
// sent to them, whatever the page's visibility. Repeating it answers
// unsubscribed. The answer names the page only by the title the mail
// already carried.
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
	return models.StatusPageSubscriptionResponse{State: models.StatusPageSubscriptionUnsubscribed, PageTitle: page.title}, nil
}

// UnsubscribePageURL is where a browser that opened the one-click URL itself
// (GET) is sent: the page's unsubscribe confirmation, token in the
// fragment. "" when the token does not verify.
func (s *Service) UnsubscribePageURL(ctx context.Context, token string) (string, error) {
	_, page, state, err := s.resolveToken(ctx, token, actionlink.ActionUnsubscribePage)
	if err != nil || page == nil || state != "" {
		return "", err
	}
	return s.unsubscribeLandingURL(page.pageRef, strings.TrimSpace(token)), nil
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
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM status_pages sp WHERE sp.id = $1 AND sp.tenant_id = $2 AND `+pageLive+`)`, pageID, tenantID).Scan(&ok); err != nil {
		return fmt.Errorf("load status page: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// counts reads the page's subscriber counts.
func (s *Service) counts(ctx context.Context, tenantID, pageID uuid.UUID) (models.StatusPageSubscriberCounts, error) {
	var c models.StatusPageSubscriberCounts
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FILTER (WHERE state = 'confirmed'), COUNT(*) FILTER (WHERE state = 'pending')
		FROM status_page_subscribers WHERE tenant_id = $1 AND status_page_id = $2
	`, tenantID, pageID).Scan(&c.Confirmed, &c.Pending)
	if err != nil {
		return c, fmt.Errorf("count subscribers: %w", err)
	}
	return c, nil
}

// List returns a page of the page's subscribers (newest first) with its
// counts and whether mail is configured. It reveals addresses: the handler
// serves it to callers with write access only.
func (s *Service) List(ctx context.Context, tenantID, pageID uuid.UUID, page, pageSize int) (*models.StatusPageSubscriberListResponse, error) {
	if err := s.ensurePage(ctx, tenantID, pageID); err != nil {
		return nil, err
	}
	resp := &models.StatusPageSubscriberListResponse{
		Items: []models.StatusPageSubscriber{}, Page: page, PageSize: pageSize,
		Limit: MaxSubscribersPerPage, MailProblems: s.SubscriptionProblems(),
	}
	resp.MailReady = len(resp.MailProblems) == 0
	var err error
	if resp.Counts, err = s.counts(ctx, tenantID, pageID); err != nil {
		return nil, err
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

// RemoveMany deletes every subscriber of the page (req.All), or those whose
// address is at req.Domain, and reports how many.
func (s *Service) RemoveMany(ctx context.Context, tenantID, pageID uuid.UUID, req *models.RemoveStatusPageSubscribersRequest) (int, error) {
	if err := s.ensurePage(ctx, tenantID, pageID); err != nil {
		return 0, err
	}
	domain := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(req.Domain), "@")))
	switch {
	case req.All && domain != "":
		return 0, Invalid{Message: "give either all or a domain, not both"}
	case !req.All && domain == "":
		return 0, Invalid{Message: "give all: true, or the domain whose addresses to remove"}
	case domain != "" && (len(domain) > maxBulkDomainLen || !strings.Contains(domain, ".") || strings.ContainsAny(domain, "@ \t,;")):
		return 0, Invalid{Message: "enter a domain, like example.com"}
	}
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM status_page_subscribers
		WHERE tenant_id = $1 AND status_page_id = $2 AND ($3 = '' OR split_part(email, '@', 2) = $3)
	`, tenantID, pageID, domain)
	if err != nil {
		return 0, fmt.Errorf("remove subscribers: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
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
