package reportschedules

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/shared/tenancy"
)

// Unsubscribe states (the public route's "state").
const (
	// StateSubscribed: the link is valid and the recipient still gets the
	// reports (GET only).
	StateSubscribed = "subscribed"
	// StateUnsubscribed: the recipient gets no more reports from this
	// schedule.
	StateUnsubscribed = "unsubscribed"
	// StateInvalid: a forged or malformed token, a deleted schedule or
	// workspace, or an address no longer on the schedule.
	StateInvalid = "invalid"
)

// tokenInfo domain-separates the unsubscribe MAC from any other use of the
// key.
const tokenInfo = "probara/report-unsubscribe/v1\x00"

// macSize is the truncated HMAC-SHA256 length in a token (128 bits).
const macSize = 16

// UnsubscribeToken is the recipient's unsubscribe credential: the schedule
// ID and an HMAC of the address under the schedule's key, base64url. It
// never expires; deleting the schedule revokes it.
func UnsubscribeToken(scheduleID uuid.UUID, key []byte, email string) string {
	buf := make([]byte, 0, 16+macSize)
	buf = append(buf, scheduleID[:]...)
	buf = append(buf, tokenMAC(key, email)...)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func tokenMAC(key []byte, email string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(tokenInfo))
	m.Write([]byte(strings.ToLower(email)))
	return m.Sum(nil)[:macSize]
}

// emailHash keys report_unsubscribes: SHA-256 of the lower-cased address.
func emailHash(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(email)))
	return hex.EncodeToString(sum[:])
}

// UnsubscribeStatus is what the public route answers.
type UnsubscribeStatus struct {
	State string `json:"state"`
	// SLAName and Recipient (masked) say what the link is for; empty for an
	// invalid token.
	SLAName   string `json:"sla_name,omitempty"`
	Recipient string `json:"recipient,omitempty"`

	tenantID   uuid.UUID
	scheduleID uuid.UUID
	email      string
}

// resolveToken verifies a token against the schedule it names. Only a
// database failure is an error; every refusal is StateInvalid.
func (s *Service) resolveToken(ctx context.Context, token string) (UnsubscribeStatus, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 16+macSize {
		return UnsubscribeStatus{State: StateInvalid}, nil
	}
	scheduleID, _ := uuid.FromBytes(raw[:16])
	mac := raw[16:]

	var st UnsubscribeStatus
	var key []byte
	var recipients pq.StringArray
	err = s.db.QueryRowContext(ctx, `
		SELECT s.tenant_id, s.unsubscribe_key, s.recipients, sl.name
		FROM sla_report_schedules s
		JOIN slas sl ON sl.id = s.sla_id AND sl.tenant_id = s.tenant_id
		WHERE s.id = $1 AND `+tenancy.LiveTenantSQL("s.tenant_id"), scheduleID).Scan(&st.tenantID, &key, &recipients, &st.SLAName)
	if errors.Is(err, sql.ErrNoRows) {
		return UnsubscribeStatus{State: StateInvalid}, nil
	}
	if err != nil {
		return UnsubscribeStatus{State: StateInvalid}, fmt.Errorf("load report schedule: %w", err)
	}
	for _, r := range recipients {
		if hmac.Equal(tokenMAC(key, r), mac) {
			st.email = r
			break
		}
	}
	if st.email == "" {
		return UnsubscribeStatus{State: StateInvalid}, nil
	}
	st.scheduleID = scheduleID
	st.Recipient = maskEmail(st.email)
	var out bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM report_unsubscribes WHERE schedule_id = $1 AND email_hash = $2)`,
		scheduleID, emailHash(st.email)).Scan(&out); err != nil {
		return UnsubscribeStatus{State: StateInvalid}, fmt.Errorf("load unsubscribe: %w", err)
	}
	st.State = StateSubscribed
	if out {
		st.State = StateUnsubscribed
	}
	return st, nil
}

// DescribeUnsubscribe answers GET: what the link is for. It never acts,
// since mail scanners prefetch every link in a message.
func (s *Service) DescribeUnsubscribe(ctx context.Context, token string) (UnsubscribeStatus, error) {
	return s.resolveToken(ctx, token)
}

// Unsubscribe answers POST (the confirm page's button and RFC 8058
// one-click): the recipient gets no more reports from the schedule. It is
// idempotent and audited.
func (s *Service) Unsubscribe(ctx context.Context, r *http.Request, token string) (UnsubscribeStatus, error) {
	st, err := s.resolveToken(ctx, token)
	if err != nil || st.State == StateInvalid {
		return st, err
	}
	if st.State == StateSubscribed {
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO report_unsubscribes (schedule_id, email_hash) VALUES ($1, $2)
			ON CONFLICT (schedule_id, email_hash) DO NOTHING
		`, st.scheduleID, emailHash(st.email)); err != nil {
			return st, fmt.Errorf("record unsubscribe: %w", err)
		}
		if s.audit != nil {
			tenantID := st.tenantID
			event := audit.FromRequest(r)
			event.TenantID = &tenantID
			event.ActorLabel = "unsubscribe link"
			event.Action = "report-schedule.unsubscribe"
			event.ResourceType = "report-schedule"
			event.ResourceID = st.scheduleID.String()
			event.StatusCode = http.StatusOK
			event.Outcome = audit.OutcomeSuccess
			event.MergeDetails(map[string]any{"recipient": st.Recipient})
			s.audit.Record(event)
		}
	}
	st.State = StateUnsubscribed
	return st, nil
}

// maskEmail keeps the first character of the local part and the domain:
// "j***@example.com".
func maskEmail(email string) string {
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" {
		return "***"
	}
	return local[:1] + "***@" + domain
}
