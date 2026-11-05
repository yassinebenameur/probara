// Package escalation stores escalation policies (migration 000113): CRUD,
// validation against the workspace, and the "convert routes to policy"
// action. The rules a policy follows (shape, limits, step timing) live in
// shared/escalation; how a policy routes and whether it reaches anyone live
// in shared/alertrouting; the alerter pages person targets.
package escalation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/services/monitors"
	"github.com/yassinebenameur/probara/shared/alertrouting"
	shareddb "github.com/yassinebenameur/probara/shared/db"
	shared "github.com/yassinebenameur/probara/shared/escalation"
)

// MaxPoliciesPerTenant bounds a workspace's policies.
const MaxPoliciesPerTenant = 100

// Service implements the policy store.
type Service struct {
	db *shareddb.Client
}

// NewService builds the service.
func NewService(db *shareddb.Client) *Service { return &Service{db: db} }

// ---- reads ----

// List returns the workspace's policies, by name.
func (s *Service) List(ctx context.Context, tenantID uuid.UUID) ([]Policy, error) {
	return s.load(ctx, tenantID, nil)
}

// Get returns one policy.
func (s *Service) Get(ctx context.Context, tenantID, id uuid.UUID) (*Policy, error) {
	all, err := s.load(ctx, tenantID, &id)
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, ErrNotFound
	}
	return &all[0], nil
}

func (s *Service) load(ctx context.Context, tenantID uuid.UUID, id *uuid.UUID) ([]Policy, error) {
	query := `
		SELECT p.id, p.name, p.description, p.repeat_count, p.repeat_after_seconds, p.created_at, p.updated_at,
			(SELECT COUNT(*) FROM monitors m
			 WHERE m.escalation_policy_id = p.id AND m.notification_mode = 'policy' AND m.deleted_at IS NULL),
			EXISTS (SELECT 1 FROM tenants t WHERE t.id = p.tenant_id AND t.default_escalation_policy_id = p.id)
		FROM escalation_policies p
		WHERE p.tenant_id = $1`
	args := []any{tenantID}
	if id != nil {
		query += ` AND p.id = $2`
		args = append(args, *id)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY lower(p.name), p.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list escalation policies: %w", err)
	}
	var out []Policy
	index := map[uuid.UUID]int{}
	var ids []uuid.UUID
	for rows.Next() {
		var p Policy
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.RepeatCount, &p.RepeatAfterSeconds,
			&p.CreatedAt, &p.UpdatedAt, &p.MonitorCount, &p.TenantDefault); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan escalation policy: %w", err)
		}
		p.Steps = []Step{}
		index[p.ID] = len(out)
		ids = append(ids, p.ID)
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return []Policy{}, nil
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT st.policy_id, st.position, st.delay_seconds,
			t.kind, t.user_id, t.schedule_id, t.channel_id, t.notify_after_ack,
			COALESCE(u.username, sc.name, ac.name, ''), COALESCE(ac.type, ''),
			CASE WHEN t.kind = 'channel' THEN COALESCE(ac.is_active, FALSE)
				ELSE `+alertrouting.PersonTargetReachablePredicate("t", "$2::uuid")+` END
		FROM escalation_policy_steps st
		LEFT JOIN escalation_policy_targets t ON t.step_id = st.id
		LEFT JOIN admin_users u ON u.id = t.user_id
		LEFT JOIN oncall_schedules sc ON sc.id = t.schedule_id
		LEFT JOIN alert_channels ac ON ac.id = t.channel_id
		WHERE st.policy_id = ANY($1)
		ORDER BY st.policy_id, st.position, t.position, t.id`, pq.Array(ids), tenantID)
	if err != nil {
		return nil, fmt.Errorf("load escalation steps: %w", err)
	}
	defer rows.Close()
	lastPos := map[uuid.UUID]int{}
	for rows.Next() {
		var policyID uuid.UUID
		var position, delay int
		var kind, label, channelType sql.NullString
		var userID, scheduleID, channelID uuid.NullUUID
		var notifyAfterAck, reachable sql.NullBool
		if err := rows.Scan(&policyID, &position, &delay, &kind, &userID, &scheduleID, &channelID, &notifyAfterAck,
			&label, &channelType, &reachable); err != nil {
			return nil, fmt.Errorf("scan escalation step: %w", err)
		}
		p := &out[index[policyID]]
		if prev, ok := lastPos[policyID]; !ok || prev != position {
			p.Steps = append(p.Steps, Step{DelaySeconds: delay, Targets: []Target{}})
			lastPos[policyID] = position
		}
		if !kind.Valid {
			continue
		}
		t := Target{Kind: shared.TargetKind(kind.String), NotifyAfterAck: notifyAfterAck.Bool,
			Label: label.String, ChannelType: channelType.String, Reachable: reachable.Bool}
		if userID.Valid {
			t.UserID = &userID.UUID
		}
		if scheduleID.Valid {
			t.ScheduleID = &scheduleID.UUID
		}
		if channelID.Valid {
			t.ChannelID = &channelID.UUID
		}
		if t.Kind == shared.TargetAllAdmins {
			t.Label = "All workspace admins"
		}
		step := &p.Steps[len(p.Steps)-1]
		step.Targets = append(step.Targets, t)
	}
	return out, rows.Err()
}

// ---- writes ----

// Create stores a new policy.
func (s *Service) Create(ctx context.Context, tenantID uuid.UUID, req WriteRequest) (*Policy, error) {
	id := uuid.New()
	if err := s.write(ctx, tenantID, id, req, true); err != nil {
		return nil, err
	}
	return s.Get(ctx, tenantID, id)
}

// Replace overwrites a policy's fields and steps.
func (s *Service) Replace(ctx context.Context, tenantID, id uuid.UUID, req WriteRequest) (*Policy, error) {
	if err := s.write(ctx, tenantID, id, req, false); err != nil {
		return nil, err
	}
	return s.Get(ctx, tenantID, id)
}

// toShared validates req's shape through shared/escalation.
func toShared(req WriteRequest) (shared.Policy, string, string, error) {
	name, err := cleanText("name", req.Name, shared.MaxNameLength, false)
	if err != nil {
		return shared.Policy{}, "", "", err
	}
	desc, err := cleanText("description", req.Description, shared.MaxDescription, true)
	if err != nil {
		return shared.Policy{}, "", "", err
	}
	if req.RepeatAfterSeconds == 0 {
		req.RepeatAfterSeconds = int(shared.DefaultRepeatAfter.Seconds())
	}
	p := shared.Policy{Name: name, RepeatCount: req.RepeatCount, RepeatAfter: time.Duration(req.RepeatAfterSeconds) * time.Second}
	for i, st := range req.Steps {
		step := shared.Step{Position: i, Delay: time.Duration(st.DelaySeconds) * time.Second}
		for _, t := range st.Targets {
			step.Targets = append(step.Targets, shared.Target{Kind: t.Kind, UserID: t.UserID, ScheduleID: t.ScheduleID,
				ChannelID: t.ChannelID, NotifyAfterAck: t.NotifyAfterAck})
		}
		p.Steps = append(p.Steps, step)
	}
	if err := p.Validate(); err != nil {
		return shared.Policy{}, "", "", invalid("%s", err.Error())
	}
	return p, name, desc, nil
}

func (s *Service) write(ctx context.Context, tenantID, id uuid.UUID, req WriteRequest, create bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := writeTx(ctx, tx, tenantID, id, req, create); err != nil {
		return err
	}
	return tx.Commit()
}

// writeTx validates req and stores it as policy id inside tx.
func writeTx(ctx context.Context, tx *sql.Tx, tenantID, id uuid.UUID, req WriteRequest, create bool) error {
	p, name, desc, err := toShared(req)
	if err != nil {
		return err
	}
	if err := verifyTargets(ctx, tx, tenantID, p); err != nil {
		return err
	}
	if create {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM escalation_policies WHERE tenant_id = $1`, tenantID).Scan(&n); err != nil {
			return fmt.Errorf("count policies: %w", err)
		}
		if n >= MaxPoliciesPerTenant {
			return invalid("a workspace has at most %d escalation policies", MaxPoliciesPerTenant)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO escalation_policies (id, tenant_id, name, description, repeat_count, repeat_after_seconds)
			VALUES ($1, $2, $3, $4, $5, $6)`, id, tenantID, name, desc, p.RepeatCount, int(p.RepeatAfter.Seconds()))
	} else {
		var res sql.Result
		res, err = tx.ExecContext(ctx, `
			UPDATE escalation_policies
			SET name = $3, description = $4, repeat_count = $5, repeat_after_seconds = $6, updated_at = NOW()
			WHERE id = $1 AND tenant_id = $2`, id, tenantID, name, desc, p.RepeatCount, int(p.RepeatAfter.Seconds()))
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				return ErrNotFound
			}
		}
	}
	if isUniqueViolation(err) {
		return Conflict{Message: fmt.Sprintf("an escalation policy named %q already exists", name)}
	}
	if err != nil {
		return fmt.Errorf("save escalation policy: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM escalation_policy_steps WHERE policy_id = $1`, id); err != nil {
		return fmt.Errorf("clear steps: %w", err)
	}
	for _, st := range p.Steps {
		var stepID uuid.UUID
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO escalation_policy_steps (policy_id, position, delay_seconds) VALUES ($1, $2, $3) RETURNING id`,
			id, st.Position, int(st.Delay.Seconds())).Scan(&stepID); err != nil {
			return fmt.Errorf("insert step: %w", err)
		}
		for i, t := range st.Targets {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO escalation_policy_targets (step_id, position, kind, user_id, schedule_id, channel_id, notify_after_ack)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				stepID, i, string(t.Kind), t.UserID, t.ScheduleID, t.ChannelID, t.NotifyAfterAck); err != nil {
				return fmt.Errorf("insert target: %w", err)
			}
		}
	}
	return nil
}

// verifyTargets checks that every referenced user, schedule and channel
// belongs to the workspace: users must be enabled members (or superadmins),
// the same eligibility as an on-call schedule member.
func verifyTargets(ctx context.Context, q shareddb.Querier, tenantID uuid.UUID, p shared.Policy) error {
	for _, st := range p.Steps {
		for _, t := range st.Targets {
			var ok bool
			var err error
			switch t.Kind {
			case shared.TargetUser:
				err = q.QueryRowContext(ctx, `
					SELECT EXISTS (SELECT 1 FROM admin_users u
						WHERE u.id = $1 AND u.disabled_at IS NULL
						  AND (u.platform_role = 'superadmin' OR EXISTS (
							SELECT 1 FROM tenant_memberships m WHERE m.admin_user_id = u.id AND m.tenant_id = $2)))`,
					*t.UserID, tenantID).Scan(&ok)
				if err == nil && !ok {
					return invalid("user %s is not an active member of this workspace", *t.UserID)
				}
			case shared.TargetSchedule:
				err = q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM oncall_schedules WHERE id = $1 AND tenant_id = $2)`,
					*t.ScheduleID, tenantID).Scan(&ok)
				if err == nil && !ok {
					return invalid("on-call schedule %s not found in this workspace", *t.ScheduleID)
				}
			case shared.TargetChannel:
				err = q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM alert_channels WHERE id = $1 AND tenant_id = $2)`,
					*t.ChannelID, tenantID).Scan(&ok)
				if err == nil && !ok {
					return invalid("alert channel %s not found in this workspace", *t.ChannelID)
				}
			}
			if err != nil {
				return fmt.Errorf("verify target: %w", err)
			}
		}
	}
	return nil
}

// Delete removes a policy that no live monitor and not the workspace default
// uses. Soft-deleted monitors still pointing at it are moved off it first.
func (s *Service) Delete(ctx context.Context, tenantID, id uuid.UUID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var exists, isDefault bool
	var monitors int
	if err := tx.QueryRowContext(ctx, `
		SELECT TRUE,
			EXISTS (SELECT 1 FROM tenants t WHERE t.id = p.tenant_id AND t.default_escalation_policy_id = p.id),
			(SELECT COUNT(*) FROM monitors m WHERE m.escalation_policy_id = p.id AND m.deleted_at IS NULL)
		FROM escalation_policies p WHERE p.id = $1 AND p.tenant_id = $2
		FOR UPDATE OF p`, id, tenantID).Scan(&exists, &isDefault, &monitors); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("load policy: %w", err)
	}
	if isDefault {
		return Conflict{Message: "this is the workspace default escalation policy; choose another default in notification settings first"}
	}
	if monitors > 0 {
		return Conflict{Message: fmt.Sprintf("%d monitor(s) use this escalation policy; move them to another routing first", monitors)}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE monitors SET notification_mode = 'default'
		WHERE escalation_policy_id = $1 AND deleted_at IS NOT NULL`, id); err != nil {
		return fmt.Errorf("release deleted monitors: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM escalation_policies WHERE id = $1 AND tenant_id = $2`, id, tenantID); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23503" {
			return Conflict{Message: "this escalation policy is still in use"}
		}
		return fmt.Errorf("delete policy: %w", err)
	}
	return tx.Commit()
}

// ConvertRoutes builds a policy equivalent to existing channel routes: a
// monitor's custom routes, or the workspace default routes. Routes with the
// same delay become one step, in delay order, keeping notify_after_ack.
// With Assign, the monitor moves to notification_mode 'policy' with it in
// the same transaction (the workspace default is set in notification
// settings).
func (s *Service) ConvertRoutes(ctx context.Context, tenantID uuid.UUID, req ConvertRequest) (*Policy, error) {
	if req.MonitorID == nil && req.Assign {
		return nil, invalid("make a converted workspace policy the default in notification settings")
	}
	var routes *sql.Rows
	var err error
	name := strings.TrimSpace(req.Name)
	if req.MonitorID != nil {
		var monitorName, mode string
		err = s.db.QueryRowContext(ctx, `
			SELECT name, notification_mode FROM monitors WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`,
			*req.MonitorID, tenantID).Scan(&monitorName, &mode)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("load monitor: %w", err)
		}
		if mode != "custom" {
			return nil, invalid("only a monitor with its own routes (notification_mode 'custom') can be converted")
		}
		if name == "" {
			name = truncate(monitorName, shared.MaxNameLength-7) + " routes"
		}
		routes, err = s.db.QueryContext(ctx, `
			SELECT channel_id, delay_seconds, notify_after_ack FROM monitor_channels
			WHERE monitor_id = $1 ORDER BY delay_seconds, channel_id`, *req.MonitorID)
	} else {
		if name == "" {
			name = "Workspace default routes"
		}
		routes, err = s.db.QueryContext(ctx, `
			SELECT channel_id, delay_seconds, notify_after_ack FROM tenant_default_channels
			WHERE tenant_id = $1 ORDER BY delay_seconds, position, channel_id`, tenantID)
	}
	if err != nil {
		return nil, fmt.Errorf("load routes: %w", err)
	}
	var write WriteRequest
	write.Name = name
	for routes.Next() {
		var channelID uuid.UUID
		var delay int
		var after bool
		if err := routes.Scan(&channelID, &delay, &after); err != nil {
			routes.Close()
			return nil, err
		}
		if n := len(write.Steps); n == 0 || write.Steps[n-1].DelaySeconds != delay {
			write.Steps = append(write.Steps, Step{DelaySeconds: delay})
		}
		id := channelID
		step := &write.Steps[len(write.Steps)-1]
		step.Targets = append(step.Targets, Target{Kind: shared.TargetChannel, ChannelID: &id, NotifyAfterAck: after})
	}
	routes.Close()
	if err := routes.Err(); err != nil {
		return nil, err
	}
	if len(write.Steps) == 0 {
		return nil, invalid("there are no routes to convert")
	}
	if len(write.Steps) > shared.MaxSteps {
		return nil, invalid("these routes use more than %d different delays; a policy has at most %d steps", shared.MaxSteps, shared.MaxSteps)
	}
	for _, st := range write.Steps {
		if len(st.Targets) > shared.MaxTargetsPerStep {
			return nil, invalid("more than %d routes share one delay; a step has at most %d targets", shared.MaxTargetsPerStep, shared.MaxTargetsPerStep)
		}
	}

	id := uuid.New()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := writeTx(ctx, tx, tenantID, id, write, true); err != nil {
		return nil, err
	}
	if req.Assign {
		res, err := tx.ExecContext(ctx, `
			UPDATE monitors SET notification_mode = 'policy', escalation_policy_id = $3, updated_at = NOW()
			WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, *req.MonitorID, tenantID, id)
		if err != nil {
			return nil, fmt.Errorf("assign policy: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, ErrNotFound
		}
		// The switch to policy routing bypasses the monitors service, so it
		// records its config version here, in the same transaction, under
		// the caller's source and actor ("" follows the credential).
		if err := monitors.RecordVersionTx(ctx, tx, tenantID, *req.MonitorID, ""); err != nil {
			return nil, fmt.Errorf("record monitor version: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return s.Get(ctx, tenantID, id)
}

// ---- helpers ----

func cleanText(field, s string, max int, multiline bool) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		return "", invalid("%s must be at most %d characters", field, max)
	}
	if strings.ContainsFunc(s, func(r rune) bool {
		return (r < 0x20 && !(multiline && (r == '\n' || r == '\t'))) || r == 0x7f
	}) {
		return "", invalid("%s must not contain control characters", field)
	}
	return s, nil
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n])
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}
