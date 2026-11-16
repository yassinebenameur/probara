//go:build !unit

package slas

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedanalytics "github.com/yassinebenameur/probara/shared/analytics"
	"github.com/yassinebenameur/probara/shared/testutil"
)

// Time to acknowledge counts from an alert's first acknowledgement, which an
// unacknowledge never clears: ack -> unack -> resolve still counts as
// acknowledged, ack -> unack -> a later re-ack is not stretched to the
// re-ack, and an alert never acknowledged does not count.
func TestMTTAFromFirstAcknowledgement(t *testing.T) {
	ctx := context.Background()
	db, cleanup := testutil.SetupPostgresDB(ctx, t)
	defer cleanup()
	tenantID := testutil.InsertTenant(ctx, t, db, "mtta")
	svc := NewService(db, sharedanalytics.NewRepository(db), false)
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	minute := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Minute) }

	var monitors []uuid.UUID
	insert := func(name string, firstAck, ack *time.Time) {
		monitorID := testutil.InsertHTTPMonitor(ctx, t, db, tenantID, name)
		monitors = append(monitors, monitorID)
		_, err := db.ExecContext(ctx, `
			INSERT INTO alerts (id, tenant_id, monitor_id, kind, status, triggered_at, first_acknowledged_at,
				acknowledged_at, resolved_at, failure_count, created_at, updated_at)
			VALUES ($1, $2, $3, 'availability', 'resolved', $4, $5, $6, $7, 1, NOW(), NOW())
		`, uuid.New(), tenantID, monitorID, t0, firstAck, ack, minute(60))
		require.NoError(t, err)
	}
	insert("ack-unack-resolve", at(minute(4)), nil)          // 4 minutes
	insert("ack-unack-reack", at(minute(6)), at(minute(50))) // 6 minutes, not 50
	insert("never", nil, nil)
	insert("before-first-ack-column", nil, at(minute(8))) // an older API's acknowledgement: 8 minutes

	var out models.SLAReportResponse
	window := sharedanalytics.TimeRange{Start: minute(-60), End: minute(120)}
	require.NoError(t, svc.alertResponse(ctx, tenantID, monitors, window, &out))
	require.Equal(t, 4, out.AlertCount)
	require.Equal(t, 3, out.AcknowledgedCount)
	require.NotNil(t, out.MTTASeconds)
	require.InDelta(t, (4+6+8)*60/3.0, *out.MTTASeconds, 1e-6)
}
