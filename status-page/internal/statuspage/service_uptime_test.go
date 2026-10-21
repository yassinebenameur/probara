package statuspage

import (
	"context"
	"regexp"
	"testing"
	"time"

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

// The 1h strip is 60 clock-aligned minutes ending with the current one, read
// in one raw query with Go-computed bounds and platform results excluded. The
// page-level series sums every monitor's checks per minute.
func TestGetGlobalMinuteUptime_SumsMinuteBucketsAcrossMonitors(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	tenantID := uuid.New()
	monitorA := uuid.New()
	monitorB := uuid.New()
	now := time.Date(2026, 6, 2, 10, 30, 42, 0, time.UTC)
	wantStart := time.Date(2026, 6, 2, 9, 31, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 6, 2, 10, 31, 0, 0, time.UTC)

	mock.ExpectQuery(`(?s)date_trunc\('minute', cr.created_at\).*result_source <> 'platform'.*created_at >= \$3.*created_at < \$4`).
		WithArgs(tenantID, sqlmock.AnyArg(), wantStart, wantEnd).
		WillReturnRows(sqlmock.NewRows([]string{"monitor_id", "bucket", "total", "successful"}).
			AddRow(monitorA, wantStart, int64(1), int64(1)).
			AddRow(monitorB, wantStart, int64(1), int64(0)).
			AddRow(monitorA, time.Date(2026, 6, 2, 10, 30, 0, 0, time.UTC), int64(2), int64(2)))

	svc := NewService(&shareddb.Client{DB: sqlDB}, nil)
	svc.now = func() time.Time { return now }
	got, err := svc.GetGlobalMinuteUptime(context.Background(), []uuid.UUID{monitorA, monitorB}, tenantID)
	if err != nil {
		t.Fatalf("GetGlobalMinuteUptime() error = %v", err)
	}
	if len(got) != 60 {
		t.Fatalf("buckets = %d, want 60", len(got))
	}
	if got[0] != (MinuteUptime{Time: "09:31", Uptime: 50}) {
		t.Fatalf("first bucket = %+v, want 09:31 at 50%%", got[0])
	}
	if got[59] != (MinuteUptime{Time: "10:30", Uptime: 100}) {
		t.Fatalf("last bucket = %+v, want the current minute 10:30 at 100%%", got[59])
	}
	if got[30].Uptime != -1 {
		t.Fatalf("empty minute = %+v, want -1 (no data)", got[30])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMinuteUptime_EmptyCountsSpanTheHour(t *testing.T) {
	start := time.Date(2026, 6, 2, 9, 31, 0, 0, time.UTC)
	got := minuteUptime(nil, start)
	if len(got) != 60 || got[0].Time != "09:31" || got[59].Time != "10:30" {
		t.Fatalf("minuteUptime(nil) = %d buckets %q..%q, want 60 from 09:31 to 10:30", len(got), got[0].Time, got[len(got)-1].Time)
	}
	for _, b := range got {
		if b.Uptime != -1 {
			t.Fatalf("bucket %+v, want -1", b)
		}
	}
}
