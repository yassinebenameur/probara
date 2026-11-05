package maintenancewindows_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yassinebenameur/probara/api/internal/models"
	maintenancewindows "github.com/yassinebenameur/probara/api/internal/services/maintenancewindows"
)

// Preview is compute-only: no database.
func TestMaintenanceWindowPreview(t *testing.T) {
	ctx := context.Background()
	svc := maintenancewindows.NewService(nil)
	start := time.Date(2030, 3, 24, 2, 30, 0, 0, time.UTC)
	res, err := svc.Preview(ctx, &models.PreviewMaintenanceWindowRequest{
		StartsAt: start, EndsAt: start.Add(time.Hour), Count: 3,
		Recurrence: &models.MaintenanceRecurrence{Frequency: "weekly", Weekdays: []int{0, 3}, Timezone: "Europe/Paris"},
	})
	require.NoError(t, err)
	require.Equal(t, []int{0, 3}, res.Recurrence.Weekdays)
	require.Len(t, res.Occurrences, 3)
	paris, _ := time.LoadLocation("Europe/Paris")
	// 03:30 Paris wall time (start is 02:30 UTC in winter), Sundays and
	// Wednesdays; 2030-03-31 is the spring-forward Sunday.
	format := func(i int) string { return res.Occurrences[i].StartsAt.In(paris).Format("2006-01-02 15:04 MST") }
	require.Equal(t, "2030-03-24 03:30 CET", format(0))
	require.Equal(t, "2030-03-27 03:30 CET", format(1))
	require.Equal(t, "2030-03-31 03:30 CEST", format(2))

	_, err = svc.Preview(ctx, &models.PreviewMaintenanceWindowRequest{
		StartsAt: start, EndsAt: start.Add(time.Hour),
		Recurrence: &models.MaintenanceRecurrence{Frequency: "weekly", Timezone: "Nowhere/City"},
	})
	var invalid *maintenancewindows.InvalidError
	require.ErrorAs(t, err, &invalid)
}
