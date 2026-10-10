package statuspage

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"

	shareddb "github.com/yassinebenameur/probara/shared/db"
)

func TestBucketUptime(t *testing.T) {
	cases := []struct {
		total, successful int
		want              float64
	}{
		{0, 0, -1}, // no checks: -1 renders as an empty bucket, not 0%
		{4, 4, 100},
		{4, 3, 75},
		{4, 0, 0},
	}
	for _, tc := range cases {
		if got := bucketUptime(tc.total, tc.successful); got != tc.want {
			t.Errorf("bucketUptime(%d, %d) = %v, want %v", tc.total, tc.successful, got, tc.want)
		}
	}
}

// The window is bound as an interval parameter rather than spliced into the
// SQL, and a single monitor goes through the same ANY($1) query as a group.
func TestUptimeForMonitorsBindsWindowAsInterval(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorID := uuid.New()
	mock.ExpectQuery(regexp.QuoteMeta(`created_at >= NOW() - $3::interval`)).
		WithArgs(sqlmock.AnyArg(), tenantID, "24 hours").
		WillReturnRows(sqlmock.NewRows([]string{"total", "successful"}).AddRow(4, 3))

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil)
	got, err := svc.CalculateUptime24h(context.Background(), monitorID, tenantID)
	if err != nil {
		t.Fatalf("CalculateUptime24h() error = %v", err)
	}
	if got == nil || *got != 75 {
		t.Fatalf("CalculateUptime24h() = %v, want 75", got)
	}

	// An empty member set never reaches the database.
	if got, err := svc.CalculateGroupUptime24h(context.Background(), nil, tenantID); err != nil || got != nil {
		t.Fatalf("CalculateGroupUptime24h(empty) = (%v, %v), want (nil, nil)", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
