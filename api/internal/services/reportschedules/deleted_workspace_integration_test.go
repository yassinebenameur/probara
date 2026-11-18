//go:build !unit

package reportschedules

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// A deleted workspace's schedules send nothing more, its deliveries already
// queued are never claimed, and the unsubscribe links it mailed stop working.
func TestDeletedWorkspaceStopsReportsAndLinks(t *testing.T) {
	f := setup(t)
	now := sep15
	mailer := &fakeMailer{}
	s := f.service(mailer, &now)
	f.put(t, s, recipient...)
	now = afterDue
	require.NoError(t, s.RunOnce(f.ctx))
	require.Len(t, mailer.sent, 2)
	u, err := url.Parse(strings.Trim(mailer.sent[0].Headers["List-Unsubscribe"], "<>"))
	require.NoError(t, err)
	token := u.Query().Get("token")
	require.NotEmpty(t, token)

	// A queued delivery left from before the delete.
	_, err = f.db.ExecContext(f.ctx, `
		INSERT INTO sla_report_deliveries (schedule_id, tenant_id, period_start, period_key, address_key, address, next_attempt_at)
		SELECT id, tenant_id, '2026-08-01', '2026-08', 'late', 'late@example.com', NOW() - INTERVAL '1 minute'
		FROM sla_report_schedules WHERE sla_id = $1`, f.sla.ID)
	require.NoError(t, err)
	_, err = f.db.ExecContext(f.ctx, `UPDATE tenants SET deleted_at = NOW() WHERE id = $1`, f.tenantID)
	require.NoError(t, err)

	got, err := s.DescribeUnsubscribe(f.ctx, token)
	require.NoError(t, err)
	require.Equal(t, models.ReportUnsubscribeResponse{State: models.ReportUnsubscribeInvalid}, got)

	now = time.Date(2026, 11, 1, 1, 30, 0, 0, time.UTC) // October has closed
	require.NoError(t, s.RunOnce(f.ctx))
	require.Len(t, mailer.sent, 2, "nothing is mailed for a deleted workspace")
	var pending int
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM sla_report_deliveries WHERE address_key = 'late' AND status = 'pending'`).Scan(&pending))
	require.Equal(t, 1, pending, "the queued delivery is left for the purge")
}
