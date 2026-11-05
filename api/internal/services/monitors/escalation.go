package monitors

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// Escalation policies (migration 000113): notification_mode 'policy' routes
// a monitor through monitors.escalation_policy_id. The database requires the
// policy whenever the mode is 'policy' (monitors_escalation_policy_mode_check)
// and clears it whenever the mode leaves 'policy' (a trigger), so every
// write that enters the mode sets both columns in one statement.

// ErrEscalationPolicyNotFound is returned for an escalation_policy_id that is
// not one of the tenant's policies, or a 'policy' mode without one.
var ErrEscalationPolicyNotFound = errors.New("escalation policy not found in this workspace")

// EscalationPolicyStore is implemented by repositories that know escalation
// policies (PostgresRepository). It is optional so test doubles of
// Repository need not implement it; without it 'policy' mode is refused.
type EscalationPolicyStore interface {
	VerifyEscalationPolicy(ctx context.Context, tenantID, policyID uuid.UUID) error
	GetEscalationPolicyIDs(ctx context.Context, monitorIDs []uuid.UUID) (map[uuid.UUID]uuid.UUID, error)
}

// VerifyEscalationPolicy returns ErrEscalationPolicyNotFound unless policyID
// is tenantID's.
func (r *PostgresRepository) VerifyEscalationPolicy(ctx context.Context, tenantID, policyID uuid.UUID) error {
	var one int
	err := r.db.QueryRowContext(ctx, `SELECT 1 FROM escalation_policies WHERE id = $1 AND tenant_id = $2`, policyID, tenantID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrEscalationPolicyNotFound
	}
	if err != nil {
		return fmt.Errorf("verify escalation policy: %w", err)
	}
	return nil
}

// GetEscalationPolicyIDs returns the escalation_policy_id of the monitors
// that have one.
func (r *PostgresRepository) GetEscalationPolicyIDs(ctx context.Context, monitorIDs []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	out := map[uuid.UUID]uuid.UUID{}
	if len(monitorIDs) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, escalation_policy_id FROM monitors
		WHERE id = ANY($1) AND escalation_policy_id IS NOT NULL`, pq.Array(monitorIDs))
	if err != nil {
		return nil, fmt.Errorf("load escalation policies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, policyID uuid.UUID
		if err := rows.Scan(&id, &policyID); err != nil {
			return nil, err
		}
		out[id] = policyID
	}
	return out, rows.Err()
}

// requestedEscalationPolicy validates the policy a create or update asks for
// (nil when none) against the tenant.
func (s *Service) requestedEscalationPolicy(ctx context.Context, tenantID uuid.UUID, raw *string) (*uuid.UUID, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(*raw)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid escalation_policy_id", ErrEscalationPolicyNotFound)
	}
	store, ok := s.repo.(EscalationPolicyStore)
	if !ok {
		return nil, ErrEscalationPolicyNotFound
	}
	if err := store.VerifyEscalationPolicy(ctx, tenantID, id); err != nil {
		return nil, err
	}
	return &id, nil
}

// prepareEscalationCreate is CreateMonitor's half: a monitor created in
// 'policy' mode is inserted in 'default' mode, then moved to its policy
// (applyEscalationCreate), since the insert does not carry the column. The
// policy is verified before anything is written.
func (s *Service) prepareEscalationCreate(ctx context.Context, tenantID uuid.UUID, req *models.CreateMonitorRequest, mode *string) (*uuid.UUID, error) {
	if *mode != "policy" {
		return nil, nil
	}
	id, err := s.requestedEscalationPolicy(ctx, tenantID, req.EscalationPolicyID)
	if err != nil {
		return nil, err
	}
	if id == nil {
		return nil, ErrEscalationPolicyNotFound
	}
	*mode = "default"
	return id, nil
}

// applyEscalationCreate moves a just-created monitor to its policy.
func (s *Service) applyEscalationCreate(ctx context.Context, monitor *models.Monitor, policyID *uuid.UUID) error {
	if policyID == nil {
		return nil
	}
	if err := s.repo.Update(ctx, monitor, []string{"notification_mode = $1", "escalation_policy_id = $2"},
		[]interface{}{"policy", *policyID}); err != nil {
		return err
	}
	monitor.EscalationPolicyID = policyID
	return nil
}

// escalationUpdateParts returns the SET fragments an update needs for its
// escalation policy, numbered from argIndex: entering 'policy' mode (the
// request's notification_mode, already in setParts) sets the policy in the
// same statement; a monitor already in 'policy' mode may switch policy
// alone. Leaving 'policy' needs nothing (the trigger clears it).
func (s *Service) escalationUpdateParts(ctx context.Context, tenantID uuid.UUID, existing *models.Monitor, req *models.UpdateMonitorRequest, argIndex int) ([]string, []interface{}, error) {
	mode := existing.NotificationMode
	if req.NotificationMode != nil {
		mode = *req.NotificationMode
	}
	if mode != "policy" {
		if req.EscalationPolicyID != nil && *req.EscalationPolicyID != "" {
			return nil, nil, fmt.Errorf("%w: escalation_policy_id applies to notification_mode 'policy' only", ErrEscalationPolicyNotFound)
		}
		return nil, nil, nil
	}
	id, err := s.requestedEscalationPolicy(ctx, tenantID, req.EscalationPolicyID)
	if err != nil {
		return nil, nil, err
	}
	if id == nil {
		if existing.NotificationMode == "policy" {
			return nil, nil, nil // unchanged
		}
		return nil, nil, fmt.Errorf("%w: notification_mode 'policy' needs an escalation_policy_id", ErrEscalationPolicyNotFound)
	}
	return []string{fmt.Sprintf("escalation_policy_id = $%d", argIndex)}, []interface{}{*id}, nil
}

// attachEscalationPolicies fills EscalationPolicyID on monitors read back.
func (s *Service) attachEscalationPolicies(ctx context.Context, monitors []*models.Monitor) {
	store, ok := s.repo.(EscalationPolicyStore)
	if !ok || len(monitors) == 0 {
		return
	}
	ids := make([]uuid.UUID, len(monitors))
	for i, m := range monitors {
		ids[i] = m.ID
	}
	policies, err := store.GetEscalationPolicyIDs(ctx, ids)
	if err != nil {
		return
	}
	for _, m := range monitors {
		if id, ok := policies[m.ID]; ok {
			m.EscalationPolicyID = &id
		} else {
			m.EscalationPolicyID = nil
		}
	}
}
