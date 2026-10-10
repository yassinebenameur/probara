package scheduler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/shared/config"
)

func TestRetentionCleanup_OnlyCompletesDayAfterDraining(t *testing.T) {
	s := &Scheduler{config: &config.SchedulerConfig{
		RetentionCleanupEnabled: true,
		RetentionCleanupHourUTC: 2,
	}}
	now := time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC)
	day := now.Format("2006-01-02")
	require.False(t, s.beginRetentionCleanup(now.Add(-time.Minute)), "wait for the configured hour")
	require.True(t, s.beginRetentionCleanup(now))
	require.False(t, s.beginRetentionCleanup(now.Add(time.Minute)), "never overlap an active pass")
	s.finishRetentionCleanup(day, false)
	require.True(t, s.beginRetentionCleanup(now.Add(time.Minute)), "backlog or failure must retry on the next tick")
	s.finishRetentionCleanup(day, true)
	require.False(t, s.beginRetentionCleanup(now.Add(2*time.Minute)), "a drained day stays complete")
	require.True(t, s.beginRetentionCleanup(now.AddDate(0, 0, 1)), "the next day starts normally")
	s.finishRetentionCleanup(now.AddDate(0, 0, 1).Format("2006-01-02"), true)
	s.config.RetentionCleanupEnabled = false
	require.False(t, s.beginRetentionCleanup(now.AddDate(0, 0, 2)))
}
