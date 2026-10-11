package ai

import (
	"context"
	"database/sql"
	"github.com/google/uuid"
)

type SettingsQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// EffectiveEnabled is shared by API and worker. Installation disabling wins;
// an explicit workspace row wins next, including a legacy disabled row.
func EffectiveEnabled(ctx context.Context, q SettingsQuerier, tenantID uuid.UUID, disabled bool) (bool, error) {
	if disabled {
		return false, nil
	}
	var enabled bool
	err := q.QueryRowContext(ctx, `SELECT enabled FROM ai_settings WHERE tenant_id = $1`, tenantID).Scan(&enabled)
	if err == sql.ErrNoRows {
		return true, nil
	}
	return enabled, err
}
