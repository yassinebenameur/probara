package ai

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// TriageLookback bounds which open alerts the triage relay claims
// (worker/internal/airca relayClaimSQL): turning auto-triage on in a
// workspace with days-old open alerts must not spend the hourly cap on them.
// The API's SLA cause breakdown uses the same bound to tell an open alert
// that may still be triaged from one that never will be.
const TriageLookback = 6 * time.Hour

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
