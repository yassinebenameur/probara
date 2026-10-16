package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/shared/config"
	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/queue"
)

// A dispatch retried for the same due slot (a publish that errored after
// JetStream stored it, or a reschedule rolled back) carries the same
// Nats-Msg-Id, so the stream drops the copy; the next slot gets a new one.
func TestPublishMonitorJobs_SlotMsgID(t *testing.T) {
	s := NewScheduler(&config.SchedulerConfig{CheckJobSubject: "check.jobs"}, logger.New("msgid-test", "error"), metrics.NewRegistry("msgid_test"), nil, nil)
	var ids []string
	s.publish = func(_ context.Context, _ string, _ *models.Job, headers map[string][]string) error {
		ids = append(ids, headers[queue.MsgIDHeader][0])
		return errors.New("PubAck lost") // stored by the stream, but the caller saw an error
	}
	due := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	monitor := Monitor{
		ID: uuid.New(), TenantID: uuid.New(), Type: "sip",
		Config:          []byte(`{"host":"trunk.example.com","method":"invite","call":{"number":"100"}}`),
		IntervalSeconds: 60, TimeoutSeconds: 15,
		DueAt:           sql.NullTime{Time: due, Valid: true},
	}

	for range 2 { // the failed tick, then its retry
		if published, failed := s.publishMonitorJobs(context.Background(), monitor); published != 0 || failed != 1 {
			t.Fatalf("publishMonitorJobs() = %d published, %d failed; want 0, 1", published, failed)
		}
	}
	if len(ids) != 2 || ids[0] != ids[1] || ids[0] == "" {
		t.Fatalf("Nats-Msg-Ids = %q, want the same id for the retried slot", ids)
	}

	monitor.DueAt.Time = due.Add(time.Minute)
	s.publishMonitorJobs(context.Background(), monitor)
	if ids[2] == ids[0] {
		t.Fatalf("next slot reused id %q", ids[2])
	}

	monitor.DueAt = sql.NullTime{}
	s.publishMonitorJobs(context.Background(), monitor)
	if want := "check:" + monitor.ID.String() + "::initial"; ids[3] != want {
		t.Fatalf("id without next_run_at = %q, want %q", ids[3], want)
	}
}
