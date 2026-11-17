// Package statuspagedomains manages status pages' custom domains: the
// operator adds a hostname, creates the two DNS records the API names, and
// the API verifies them, on demand and in a background runner that every
// replica runs (rows are claimed with FOR UPDATE SKIP LOCKED). Only a
// verified domain is served by the status-page service, which reads this
// table (shared/pagedomains is the contract).
package statuspagedomains

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/apperr"
	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/db/pgerr"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/pagedomains"
	"github.com/yassinebenameur/probara/shared/statusupdates"
)

// MaxDomainsPerPage bounds a page's domains (and the DNS work each one
// costs the runner).
const MaxDomainsPerPage = 10

// manualCheckInterval is the shortest wait between two checks of one domain
// that an operator may ask for. It reads last_checked_at, so it holds across
// API replicas.
const manualCheckInterval = 30 * time.Second

// Errors the handlers map to a status with errors.Is. Their text reaches API
// clients.
var (
	ErrStatusPageNotFound = apperr.ErrStatusPageNotFound
	ErrNotFound           = errors.New("custom domain not found")
	ErrDisabled           = errors.New("custom domains are off on this installation: STATUS_PAGE_CNAME_TARGET is not set on the API")
	ErrHostnameTaken      = errors.New("that hostname is already used by a status page")
	ErrHostnameReserved   = errors.New("that hostname is one of this installation's own hosts")
	ErrTooManyDomains     = fmt.Errorf("a status page can have at most %d custom domains", MaxDomainsPerPage)
)

// CheckedRecentlyError refuses a manual check asked for too soon after the
// last one.
type CheckedRecentlyError struct {
	RetryAfter time.Duration
}

func (e *CheckedRecentlyError) Error() string {
	return "this domain was checked moments ago; try again shortly"
}

// Publisher tells the status-page service a page's served domains changed.
// *statusupdates.Publisher implements it; nil publishes nothing (the
// service's host table then catches up within its TTL).
type Publisher interface {
	Publish(event statusupdates.Event) error
}

// Config is the service's wiring.
type Config struct {
	// CNAMETarget is the normalized STATUS_PAGE_CNAME_TARGET; empty turns the
	// feature off.
	CNAMETarget string
	// ReservedHosts are the installation's own hosts (ReservedHosts), never
	// accepted as a custom domain.
	ReservedHosts []string
	Resolver      Resolver
	Publisher     Publisher
	Log           *logger.Logger
}

// ReservedHosts collects the hosts of the installation's own URLs (operator
// UI, API, status-page base, NATS websocket) and the CNAME target. A page
// claiming one of them would take over that host on the status-page service.
func ReservedHosts(urls ...string) []string {
	var out []string
	for _, u := range urls {
		if h := pagedomains.HostOfURL(u); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// Service manages custom domains.
type Service struct {
	db  *db.Client
	cfg Config
	now func() time.Time
}

// NewService returns the custom-domain service.
func NewService(dbClient *db.Client, cfg Config) *Service {
	if cfg.Resolver == nil {
		cfg.Resolver = NewDNSResolver()
	}
	return &Service{db: dbClient, cfg: cfg, now: time.Now}
}

// Enabled reports whether custom domains can be added and verified.
func (s *Service) Enabled() bool { return s.cfg.CNAMETarget != "" }

var domainColumns = `
	d.id, d.status_page_id, d.hostname, d.verification_token, d.status,
	d.txt_ok, d.cname_ok, d.last_error, d.last_checked_at, d.verified_at,
	d.failing_since, d.consecutive_failures, d.next_check_at, d.created_at,
	COALESCE(d.hostname = ` + pagedomains.PrimaryHostSQL("d.status_page_id") + `, FALSE)`

// row is one status_page_domains row.
type row struct {
	ID                  uuid.UUID
	PageID              uuid.UUID
	Hostname            string
	Token               string
	Status              string
	TXTOK, CNAMEOK      sql.NullBool
	LastError           sql.NullString
	LastCheckedAt       sql.NullTime
	VerifiedAt          sql.NullTime
	FailingSince        sql.NullTime
	ConsecutiveFailures int
	NextCheckAt         time.Time
	CreatedAt           time.Time
	Primary             bool
}

func scanRow(sc interface{ Scan(...interface{}) error }) (*row, error) {
	var r row
	if err := sc.Scan(&r.ID, &r.PageID, &r.Hostname, &r.Token, &r.Status,
		&r.TXTOK, &r.CNAMEOK, &r.LastError, &r.LastCheckedAt, &r.VerifiedAt,
		&r.FailingSince, &r.ConsecutiveFailures, &r.NextCheckAt, &r.CreatedAt, &r.Primary); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Service) toAPI(r *row) models.StatusPageDomain {
	out := models.StatusPageDomain{
		ID:           r.ID,
		StatusPageID: r.PageID,
		Hostname:     r.Hostname,
		Status:       r.Status,
		URL:          pagedomains.PageURL(r.Hostname),
		Primary:      r.Primary,
		Records: []models.StatusPageDomainRecord{
			{Type: "CNAME", Name: r.Hostname, Value: s.cfg.CNAMETarget, Found: nullBool(r.CNAMEOK)},
			{Type: "TXT", Name: pagedomains.VerificationRecordName(r.Hostname), Value: r.Token, Found: nullBool(r.TXTOK)},
		},
		NextCheckAt: r.NextCheckAt,
		CreatedAt:   r.CreatedAt,
	}
	if r.LastError.Valid && r.LastError.String != "" {
		v := r.LastError.String
		out.LastError = &v
	}
	out.LastCheckedAt = nullTime(r.LastCheckedAt)
	out.VerifiedAt = nullTime(r.VerifiedAt)
	out.FailingSince = nullTime(r.FailingSince)
	return out
}

// List returns the tenant's custom domains, of one page when pageID is set,
// oldest first.
func (s *Service) List(ctx context.Context, tenantID uuid.UUID, pageID *uuid.UUID) (*models.StatusPageDomainListResponse, error) {
	query := `SELECT ` + domainColumns + `
		FROM status_page_domains d
		JOIN status_pages sp ON sp.id = d.status_page_id
		WHERE sp.tenant_id = $1`
	args := []interface{}{tenantID}
	if pageID != nil {
		if err := s.ensurePage(ctx, tenantID, *pageID); err != nil {
			return nil, err
		}
		query += ` AND d.status_page_id = $2`
		args = append(args, *pageID)
	}
	query += ` ORDER BY d.status_page_id, d.created_at, d.hostname`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list custom domains: %w", err)
	}
	defer rows.Close()
	out := &models.StatusPageDomainListResponse{
		Items:       []models.StatusPageDomain{},
		Enabled:     s.Enabled(),
		CNAMETarget: s.cfg.CNAMETarget,
	}
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan custom domain: %w", err)
		}
		out.Items = append(out.Items, s.toAPI(r))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list custom domains: %w", err)
	}
	return out, nil
}

// get loads one of the tenant's domains.
func (s *Service) get(ctx context.Context, tenantID, id uuid.UUID) (*row, error) {
	r, err := scanRow(s.db.QueryRowContext(ctx, `SELECT `+domainColumns+`
		FROM status_page_domains d
		JOIN status_pages sp ON sp.id = d.status_page_id
		WHERE d.id = $1 AND sp.tenant_id = $2`, id, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load custom domain: %w", err)
	}
	return r, nil
}

func (s *Service) ensurePage(ctx context.Context, tenantID, pageID uuid.UUID) error {
	var found bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM status_pages WHERE id = $1 AND tenant_id = $2)`, pageID, tenantID).Scan(&found); err != nil {
		return fmt.Errorf("load status page: %w", err)
	}
	if !found {
		return ErrStatusPageNotFound
	}
	return nil
}

// Create adds a custom domain to a page, pending until its records verify.
// hostname must already be normalized (pagedomains.NormalizeHostname).
func (s *Service) Create(ctx context.Context, tenantID, pageID uuid.UUID, hostname string) (*models.StatusPageDomain, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	for _, reserved := range s.cfg.ReservedHosts {
		if hostname == reserved {
			return nil, ErrHostnameReserved
		}
	}
	token, err := newToken()
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	// The page row lock serializes concurrent adds, so two cannot both pass
	// the cap.
	var locked uuid.UUID
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM status_pages WHERE id = $1 AND tenant_id = $2 FOR UPDATE`, pageID, tenantID).Scan(&locked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrStatusPageNotFound
		}
		return nil, fmt.Errorf("lock status page: %w", err)
	}
	var count int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM status_page_domains WHERE status_page_id = $1`, pageID).Scan(&count); err != nil {
		return nil, fmt.Errorf("count custom domains: %w", err)
	}
	if count >= MaxDomainsPerPage {
		return nil, ErrTooManyDomains
	}
	var id uuid.UUID
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO status_page_domains (status_page_id, hostname, verification_token)
		VALUES ($1, $2, $3)
		RETURNING id`, pageID, hostname, token).Scan(&id); err != nil {
		if pgerr.IsUniqueViolation(err) {
			return nil, ErrHostnameTaken
		}
		return nil, fmt.Errorf("add custom domain: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	r, err := s.get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	out := s.toAPI(r)
	return &out, nil
}

// Delete removes a domain. It stops being served at once.
func (s *Service) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	var pageID uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		DELETE FROM status_page_domains d
		USING status_pages sp
		WHERE d.id = $1 AND sp.id = d.status_page_id AND sp.tenant_id = $2
		RETURNING d.status_page_id`, id, tenantID).Scan(&pageID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("delete custom domain: %w", err)
	}
	s.notify(tenantID, pageID)
	return nil
}

// Verify checks one domain now, for the operator's "verify now". It is
// refused within manualCheckInterval of the domain's last check.
func (s *Service) Verify(ctx context.Context, tenantID, id uuid.UUID) (*models.StatusPageDomain, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}
	now := s.now()
	var claimed claim
	err := s.db.QueryRowContext(ctx, `
		UPDATE status_page_domains d
		SET last_checked_at = $3, updated_at = NOW()
		FROM status_pages sp
		WHERE d.id = $1 AND sp.id = d.status_page_id AND sp.tenant_id = $2
		  AND (d.last_checked_at IS NULL OR d.last_checked_at <= $4)
		RETURNING d.id, d.hostname, d.verification_token, d.created_at, sp.tenant_id, d.status_page_id`,
		id, tenantID, now, now.Add(-manualCheckInterval)).Scan(&claimed.ID, &claimed.Hostname, &claimed.Token, &claimed.CreatedAt, &claimed.TenantID, &claimed.PageID)
	if errors.Is(err, sql.ErrNoRows) {
		r, gerr := s.get(ctx, tenantID, id)
		if gerr != nil {
			return nil, gerr
		}
		wait := manualCheckInterval
		if r.LastCheckedAt.Valid {
			wait = r.LastCheckedAt.Time.Add(manualCheckInterval).Sub(now)
		}
		if wait < time.Second {
			wait = time.Second
		}
		return nil, &CheckedRecentlyError{RetryAfter: wait}
	}
	if err != nil {
		return nil, fmt.Errorf("claim custom domain: %w", err)
	}
	res := checkDomain(ctx, s.cfg.Resolver, claimed.Hostname, claimed.Token, s.cfg.CNAMETarget)
	if err := s.record(context.WithoutCancel(ctx), claimed, res); err != nil {
		return nil, err
	}
	r, err := s.get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	out := s.toAPI(r)
	return &out, nil
}

// claim is a domain taken for one check.
type claim struct {
	ID        uuid.UUID
	Hostname  string
	Token     string
	CreatedAt time.Time
	TenantID  uuid.UUID
	PageID    uuid.UUID
}

// record stores a check's outcome. The row is locked for the read and the
// write only (never across the DNS lookups), so a manual check and the
// runner checking the same domain each count once, in turn.
func (s *Service) record(ctx context.Context, c claim, res checkResult) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	var cur domainState
	var verifiedAt, failingSince sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT status, verified_at, failing_since, consecutive_failures, next_check_at
		FROM status_page_domains WHERE id = $1 FOR UPDATE`, c.ID).
		Scan(&cur.Status, &verifiedAt, &failingSince, &cur.ConsecutiveFailures, &cur.NextCheckAt)
	if errors.Is(err, sql.ErrNoRows) {
		// Deleted while it was being checked.
		return nil
	}
	if err != nil {
		return fmt.Errorf("load custom domain state: %w", err)
	}
	cur.VerifiedAt = nullTime(verifiedAt)
	cur.FailingSince = nullTime(failingSince)

	now := s.now()
	next, servedChanged := advance(cur, c.CreatedAt, res, now)
	var lastError interface{}
	if res.Problem != "" {
		lastError = truncate(res.Problem, 500)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE status_page_domains
		SET status = $2, verified_at = $3, failing_since = $4, consecutive_failures = $5,
		    next_check_at = $6, txt_ok = $7, cname_ok = $8, last_error = $9,
		    last_checked_at = $10, updated_at = NOW()
		WHERE id = $1`,
		c.ID, next.Status, next.VerifiedAt, next.FailingSince, next.ConsecutiveFailures,
		next.NextCheckAt, res.TXTFound, res.CNAMEFound, lastError, now); err != nil {
		return fmt.Errorf("record custom domain check: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	if servedChanged {
		if s.cfg.Log != nil {
			s.cfg.Log.WithFields(map[string]interface{}{
				"domain_id": c.ID.String(),
				"page_id":   c.PageID.String(),
				"status":    next.Status,
			}).Info("Status page custom domain changed status")
		}
		s.notify(c.TenantID, c.PageID)
	}
	return nil
}

func (s *Service) notify(tenantID, pageID uuid.UUID) {
	if s.cfg.Publisher == nil {
		return
	}
	_ = s.cfg.Publisher.Publish(statusupdates.Event{
		Type:         pagedomains.EventDomainsChanged,
		StatusPageID: pageID.String(),
		TenantID:     tenantID.String(),
		Timestamp:    time.Now().UTC(),
	})
}

// newToken is a domain's verification value: random, and prefixed so an
// operator recognizes it among other TXT records.
func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate verification token: %w", err)
	}
	return "probara-verify=" + hex.EncodeToString(b), nil
}

func nullBool(v sql.NullBool) *bool {
	if !v.Valid {
		return nil
	}
	b := v.Bool
	return &b
}

func nullTime(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time
	return &t
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n])
}
