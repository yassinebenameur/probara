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
// that may still be triaged from one that never will be. The docs state the
// same bound: "6 hours" in the AI triage section
// (website/lib/docs/pages/alerting.ts) and "6-hour" in the
// SLA suspected-cause bucket table (website/lib/docs/pages/slas.ts); change them
// together.
const TriageLookback = 6 * time.Hour

// TriageSettingsEnabledSQL is the workspace half of triage eligibility: the
// ai_settings row (alias settingsAlias) has AI and auto-triage on. The relay's
// settings CTE and the API's SLA "Triage pending" bucket
// (triageview.ClaimableExpr) both build from it, so they cannot disagree on
// which workspaces are triaged.
func TriageSettingsEnabledSQL(settingsAlias string) string {
	return settingsAlias + `.enabled AND ` + settingsAlias + `.auto_triage_enabled`
}

// TriageClaimableAlertSQL is the alert half of triage eligibility, shared by
// the relay's claim (worker/internal/airca relayClaimSQL) and the API's SLA
// "Triage pending" bucket (triageview.ClaimableExpr): the alert (alias
// alertAlias) is open (acknowledged counts), triggered at or after
// lookbackExpr (now - TriageLookback), and its monitor, if any, is not
// deleted. Mesh alerts (no monitor) are eligible; the consumer skips them as
// not modeled. Delays that only postpone a claim (the settle wait, the hourly
// cap, the backlog bound) are not part of eligibility and stay in the relay.
func TriageClaimableAlertSQL(alertAlias, lookbackExpr string) string {
	a := alertAlias
	return a + `.status IN ('active','acknowledged') AND ` + a + `.resolved_at IS NULL
      AND ` + a + `.triggered_at >= ` + lookbackExpr + `
      AND (` + a + `.monitor_id IS NULL OR EXISTS (SELECT 1 FROM monitors tcm WHERE tcm.id = ` + a + `.monitor_id
            AND tcm.tenant_id = ` + a + `.tenant_id AND tcm.deleted_at IS NULL))`
}

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
