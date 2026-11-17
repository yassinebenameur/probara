package reportschedules

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/services/audit"
	"github.com/yassinebenameur/probara/shared/actionlink"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/tenancy"
)

// addressKey identifies one recipient of one schedule: the first 16 bytes
// of SHA-256(schedule ID, lower-cased address), hex. Unsubscribes and
// deliveries are keyed by it, and the unsubscribe link carries it (as a
// UUID-shaped ID) instead of the address, so the link names nobody and an
// opt-out outlives the address's removal from the list. Salting with the
// schedule keeps keys of one address unrelated across schedules.
func addressKey(scheduleID uuid.UUID, address string) string {
	h := sha256.New()
	h.Write(scheduleID[:])
	h.Write([]byte(strings.ToLower(strings.TrimSpace(address))))
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// keyID is an address key as the UUID-shaped ID a token carries.
func keyID(key string) (uuid.UUID, error) {
	raw, err := hex.DecodeString(key)
	if err != nil || len(raw) != 16 {
		return uuid.Nil, fmt.Errorf("malformed address key")
	}
	var id uuid.UUID
	copy(id[:], raw)
	return id, nil
}

// unsubscribeToken mints one recipient's unsubscribe token.
func (s *Service) unsubscribeToken(tenantID, scheduleID uuid.UUID, address string) (string, error) {
	if s.links == nil {
		return "", actionlink.ErrNotConfigured
	}
	id, err := keyID(addressKey(scheduleID, address))
	if err != nil {
		return "", err
	}
	return s.links.ActionToken(actionlink.ActionUnsubscribeReport, tenantID.String(), scheduleID.String(), id.String(), s.now())
}

// UnsubscribePagePath is the operator UI's public confirm page; the token
// rides in the URL fragment, which browsers never send to a server.
const UnsubscribePagePath = "/report-unsubscribe"

// UnsubscribeAPIPath is the public API route. The one-click POST of RFC
// 8058 cannot set a header, so List-Unsubscribe names it with the token as
// a query parameter, which the request log never records (it logs the path
// only).
const UnsubscribeAPIPath = "/api/v1/report-unsubscribe"

// unsubscribeLinks returns the recipient's confirm-page URL (for the body)
// and one-click URL (for List-Unsubscribe).
func (s *Service) unsubscribeLinks(tenantID, scheduleID uuid.UUID, address string) (page, oneClick string, err error) {
	token, err := s.unsubscribeToken(tenantID, scheduleID, address)
	if err != nil {
		return "", "", err
	}
	return s.appBaseURL + UnsubscribePagePath + "#" + token, s.appBaseURL + UnsubscribeAPIPath + "?token=" + token, nil
}

// unsubscribeLookup is a verified token and what it names.
type unsubscribeLookup struct {
	claims     actionlink.Claims
	authentic  bool
	addressKey string
	slaID      uuid.UUID
	slaName    string
	state      string
}

// resolveUnsubscribe verifies token and loads the schedule it names. Every
// refusal is a state; only a database failure is an error. A schedule
// outside the token's tenant reads as invalid, so a token never reaches
// another workspace.
func (s *Service) resolveUnsubscribe(ctx context.Context, token string) (unsubscribeLookup, error) {
	token = strings.TrimSpace(token)
	if s.links == nil || token == "" {
		return unsubscribeLookup{state: models.ReportUnsubscribeInvalid}, nil
	}
	claims, err := s.links.Verify(token, actionlink.ActionUnsubscribeReport)
	switch {
	case errors.Is(err, actionlink.ErrExpired):
		return unsubscribeLookup{claims: claims, authentic: true, state: models.ReportUnsubscribeExpired}, nil
	case err != nil:
		return unsubscribeLookup{state: models.ReportUnsubscribeInvalid}, nil
	}
	l := unsubscribeLookup{claims: claims, authentic: true, addressKey: hex.EncodeToString(claims.ChannelID[:])}
	var unsubscribed bool
	err = s.db.QueryRowContext(ctx, `
		SELECT sl.id, sl.name, EXISTS (SELECT 1 FROM sla_report_unsubscribes u WHERE u.schedule_id = sc.id AND u.address_key = $3)
		FROM sla_report_schedules sc
		JOIN slas sl ON sl.id = sc.sla_id AND sl.tenant_id = sc.tenant_id
		WHERE sc.tenant_id = $1 AND sc.id = $2
		  AND `+tenancy.LiveTenantSQL("sc.tenant_id")+`
	`, claims.TenantID, claims.AlertID, l.addressKey).Scan(&l.slaID, &l.slaName, &unsubscribed)
	if errors.Is(err, sql.ErrNoRows) {
		// A deleted schedule, or workspace, revokes every link it mailed.
		l.state = models.ReportUnsubscribeInvalid
		return l, nil
	}
	if err != nil {
		return l, fmt.Errorf("resolve unsubscribe link: %w", err)
	}
	l.state = models.ReportUnsubscribeSubscribed
	if unsubscribed {
		l.state = models.ReportUnsubscribeUnsubscribed
	}
	return l, nil
}

// DescribeUnsubscribe reports what an unsubscribe link would do, and never
// acts: mail scanners and link previewers fetch every link they see.
func (s *Service) DescribeUnsubscribe(ctx context.Context, token string) (models.ReportUnsubscribeResponse, error) {
	l, err := s.resolveUnsubscribe(ctx, token)
	if err != nil {
		return models.ReportUnsubscribeResponse{}, err
	}
	return responseOf(l), nil
}

// Unsubscribe removes the link's recipient from the schedule: stored by
// address key, and whatever was still waiting to be sent to them is
// skipped. Repeating it is a no-op that reports "unsubscribed".
func (s *Service) Unsubscribe(ctx context.Context, token string, event audit.Event) (models.ReportUnsubscribeResponse, error) {
	l, err := s.resolveUnsubscribe(ctx, token)
	if err != nil {
		return models.ReportUnsubscribeResponse{}, err
	}
	if l.state != models.ReportUnsubscribeSubscribed {
		return responseOf(l), nil
	}
	err = withTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sla_report_unsubscribes (schedule_id, address_key) VALUES ($1, $2) ON CONFLICT DO NOTHING
		`, l.claims.AlertID, l.addressKey); err != nil {
			return fmt.Errorf("store unsubscribe: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE sla_report_deliveries SET status = 'skipped', last_error = 'unsubscribed', updated_at = NOW()
			WHERE schedule_id = $1 AND address_key = $2 AND status = 'pending'
		`, l.claims.AlertID, l.addressKey); err != nil {
			return fmt.Errorf("skip unsubscribed deliveries: %w", err)
		}
		return nil
	})
	if err != nil {
		return models.ReportUnsubscribeResponse{}, err
	}
	l.state = models.ReportUnsubscribeUnsubscribed
	s.recordUnsubscribe(l, event)
	return responseOf(l), nil
}

func responseOf(l unsubscribeLookup) models.ReportUnsubscribeResponse {
	resp := models.ReportUnsubscribeResponse{State: l.state}
	if l.state == models.ReportUnsubscribeSubscribed || l.state == models.ReportUnsubscribeUnsubscribed {
		resp.SLAName = l.slaName
	}
	return resp
}

// recordUnsubscribe audits an unsubscribe on the SLA (the resource every
// schedule event names), attributed to the link. The address key stands in
// for the address.
func (s *Service) recordUnsubscribe(l unsubscribeLookup, event audit.Event) {
	if s.audit == nil {
		return
	}
	tenantID := l.claims.TenantID
	event.TenantID = &tenantID
	event.ActorType = audit.ActorAnonymous
	event.ActorLabel = "unsubscribe link"
	event.Action = "sla-report-schedule.unsubscribe"
	event.ResourceType = "sla-report-schedule"
	event.ResourceID = l.slaID.String()
	event.Outcome = audit.OutcomeSuccess
	event.MergeDetails(map[string]any{"address_key": l.addressKey, "key_version": l.claims.KeyVersion})
	s.audit.Record(event)
}

// unsubscribedKeys returns the schedule's unsubscribed address keys.
func unsubscribedKeys(ctx context.Context, q db.Querier, scheduleID uuid.UUID) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT address_key FROM sla_report_unsubscribes WHERE schedule_id = $1`, scheduleID)
	if err != nil {
		return nil, fmt.Errorf("list unsubscribes: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("scan unsubscribe: %w", err)
		}
		out[k] = true
	}
	return out, rows.Err()
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
