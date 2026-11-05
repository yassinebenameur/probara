package escalation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/shared/alertrouting"
	"github.com/yassinebenameur/probara/shared/contacts"
	"github.com/yassinebenameur/probara/shared/db"
	"github.com/yassinebenameur/probara/shared/oncall"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// ErrNotFound is returned for a policy that is not the tenant's.
var ErrNotFound = errors.New("escalation policy not found")

// LoadMany reads the policies ids (any tenant) with their steps and targets,
// keyed by ID. Missing IDs are absent from the map.
func LoadMany(ctx context.Context, q db.Querier, ids []uuid.UUID) (map[uuid.UUID]*Policy, error) {
	out := map[uuid.UUID]*Policy{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `
		SELECT id, tenant_id, name, repeat_count, repeat_after_seconds
		FROM escalation_policies WHERE id = ANY($1)`, pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("load escalation policies: %w", err)
	}
	for rows.Next() {
		p := &Policy{}
		var repeatAfter int
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &p.RepeatCount, &repeatAfter); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan escalation policy: %w", err)
		}
		p.RepeatAfter = time.Duration(repeatAfter) * time.Second
		out[p.ID] = p
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	rows, err = q.QueryContext(ctx, `
		SELECT s.policy_id, s.position, s.delay_seconds,
			t.kind, t.user_id, t.schedule_id, t.channel_id, t.notify_after_ack
		FROM escalation_policy_steps s
		LEFT JOIN escalation_policy_targets t ON t.step_id = s.id
		WHERE s.policy_id = ANY($1)
		ORDER BY s.policy_id, s.position, t.position, t.id`, pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("load escalation steps: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var policyID uuid.UUID
		var position, delay int
		var kind sql.NullString
		var userID, scheduleID, channelID uuid.NullUUID
		var notifyAfterAck sql.NullBool
		if err := rows.Scan(&policyID, &position, &delay, &kind, &userID, &scheduleID, &channelID, &notifyAfterAck); err != nil {
			return nil, fmt.Errorf("scan escalation step: %w", err)
		}
		p := out[policyID]
		if n := len(p.Steps); n == 0 || p.Steps[n-1].Position != position {
			p.Steps = append(p.Steps, Step{Position: position, Delay: time.Duration(delay) * time.Second})
		}
		if !kind.Valid {
			continue
		}
		t := Target{Kind: TargetKind(kind.String), NotifyAfterAck: notifyAfterAck.Bool}
		if userID.Valid {
			t.UserID = &userID.UUID
		}
		if scheduleID.Valid {
			t.ScheduleID = &scheduleID.UUID
		}
		if channelID.Valid {
			t.ChannelID = &channelID.UUID
		}
		step := &p.Steps[len(p.Steps)-1]
		step.Targets = append(step.Targets, t)
	}
	return out, rows.Err()
}

// Load reads one of tenantID's policies.
func Load(ctx context.Context, q db.Querier, tenantID, policyID uuid.UUID) (*Policy, error) {
	all, err := LoadMany(ctx, q, []uuid.UUID{policyID})
	if err != nil {
		return nil, err
	}
	p, ok := all[policyID]
	if !ok || p.TenantID != tenantID {
		return nil, ErrNotFound
	}
	return p, nil
}

// ResolveUsers returns the distinct users step's person targets page at
// instant at, in target order: the user; whoever schedule has on call
// (shared/oncall, overrides first); every workspace admin. Only users who can
// be paged in tenantID are kept (alertrouting.PageableUserPredicate: enabled,
// a member or superadmin, with a deliverable verified contact), the same
// rule reachability counts on. A schedule with nobody on call adds nobody.
func ResolveUsers(ctx context.Context, q db.Querier, tenantID uuid.UUID, step Step, at time.Time) ([]uuid.UUID, error) {
	var candidates []uuid.UUID
	for _, t := range step.Targets {
		switch t.Kind {
		case TargetUser:
			candidates = append(candidates, *t.UserID)
		case TargetSchedule:
			sched, err := oncall.LoadAt(ctx, q, tenantID, *t.ScheduleID, at)
			if errors.Is(err, oncall.ErrScheduleNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if a, ok := sched.OnCallAt(at); ok {
				candidates = append(candidates, a.UserID)
			}
		case TargetAllAdmins:
			rows, err := q.QueryContext(ctx, `
				SELECT admin_user_id FROM tenant_memberships
				WHERE tenant_id = $1 AND role = 'admin'
				ORDER BY created_at, admin_user_id`, tenantID)
			if err != nil {
				return nil, fmt.Errorf("load workspace admins: %w", err)
			}
			for rows.Next() {
				var id uuid.UUID
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return nil, err
				}
				candidates = append(candidates, id)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return nil, err
			}
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	rows, err := q.QueryContext(ctx, `
		SELECT u.id FROM admin_users u
		WHERE u.id = ANY($2) AND `+alertrouting.PageableUserPredicate("u", "$1::uuid"), tenantID, pq.Array(candidates))
	if err != nil {
		return nil, fmt.Errorf("check pageable users: %w", err)
	}
	defer rows.Close()
	pageable := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		pageable[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, id := range candidates {
		if pageable[id] && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// DeliverableContacts returns the contacts of userIDs that can be paged in
// tenantID (alertrouting.DeliverableContactPredicate), with their addresses
// decrypted, ordered by user (as given) then creation.
func DeliverableContacts(ctx context.Context, q db.Querier, enc secrets.Encryptor, tenantID uuid.UUID, userIDs []uuid.UUID) ([]contacts.Contact, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	if enc == nil {
		enc = secrets.NoOpEncryptor{}
	}
	rows, err := q.QueryContext(ctx, `
		SELECT c.id, c.user_id, c.kind, c.address
		FROM user_contacts c
		WHERE c.user_id = ANY($2) AND `+alertrouting.DeliverableContactPredicate("c", "$1::uuid")+`
		ORDER BY array_position($2::uuid[], c.user_id), c.created_at, c.id`, tenantID, pq.Array(userIDs))
	if err != nil {
		return nil, fmt.Errorf("load contacts: %w", err)
	}
	defer rows.Close()
	var out []contacts.Contact
	for rows.Next() {
		c := contacts.Contact{Verified: true}
		var kind, stored string
		if err := rows.Scan(&c.ID, &c.UserID, &kind, &stored); err != nil {
			return nil, err
		}
		c.Kind = contacts.Kind(kind)
		addr, err := enc.Decrypt(stored)
		if err != nil {
			return nil, fmt.Errorf("decrypt contact %s: %w", c.ID, err)
		}
		c.Address = addr
		out = append(out, c)
	}
	return out, rows.Err()
}
