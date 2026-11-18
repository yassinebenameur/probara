package monitors

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// A bulk alerting edit holds a domain monitor to the same failure-threshold
// floor a save does: a threshold of 1 refuses the whole request before any
// monitor is written (the http monitor listed first included), and 2 passes.
func TestBulkUpdateAlerting_DomainThresholdFloor(t *testing.T) {
	repo := &updateCountingRepo{MockRepository: NewMockRepository()}
	svc := &Service{repo: repo, encryptor: secrets.NoOpEncryptor{}}
	tenantID, httpID, domainID := uuid.New(), uuid.New(), uuid.New()
	repo.monitors[httpID] = &models.Monitor{ID: httpID, TenantID: tenantID, Name: "site", Type: models.MonitorTypeHTTP,
		Config: json.RawMessage(`{"url":"https://example.com"}`), IntervalSeconds: 60, TimeoutSeconds: 10,
		ConsecutiveFailuresThreshold: 3, Enabled: true}
	repo.monitors[domainID] = &models.Monitor{ID: domainID, TenantID: tenantID, Name: "registration", Type: models.MonitorTypeDomain,
		Config: json.RawMessage(`{"domain":"example.com"}`), IntervalSeconds: 3600, TimeoutSeconds: 30,
		ConsecutiveFailuresThreshold: 3, Enabled: true}
	ctx := context.Background()

	one := 1
	_, err := svc.BulkUpdateAlerting(ctx, tenantID, []uuid.UUID{httpID, domainID}, &one, nil, nil, nil)
	if !errors.Is(err, ErrBulkAlertingRefused) {
		t.Fatalf("BulkUpdateAlerting(threshold 1) = %v, want ErrBulkAlertingRefused", err)
	}
	if !strings.Contains(err.Error(), `"registration"`) || !strings.Contains(err.Error(), "domain monitors") {
		t.Fatalf("error %q should name the monitor and the domain rule", err)
	}
	if len(repo.updated) != 0 {
		t.Fatalf("a refused bulk edit wrote monitors %v, want none", repo.updated)
	}
	for id, m := range repo.monitors {
		if m.ConsecutiveFailuresThreshold != 3 {
			t.Fatalf("monitor %s threshold = %d after a refused edit, want 3", id, m.ConsecutiveFailuresThreshold)
		}
	}

	// Without a threshold in the request the domain monitor is not in the way.
	recovery := 2
	if n, err := svc.BulkUpdateAlerting(ctx, tenantID, []uuid.UUID{httpID, domainID}, nil, &recovery, nil, nil); err != nil || n != 2 {
		t.Fatalf("BulkUpdateAlerting(recovery only) = %d, %v, want 2, nil", n, err)
	}

	two := 2
	if n, err := svc.BulkUpdateAlerting(ctx, tenantID, []uuid.UUID{httpID, domainID}, &two, nil, nil, nil); err != nil || n != 2 {
		t.Fatalf("BulkUpdateAlerting(threshold 2) = %d, %v, want 2, nil", n, err)
	}
}

// flippingRepo turns one monitor into a domain monitor right after the bulk
// edit's up-front check has read every monitor: a type change committed
// between that check and the batch's row locks.
type flippingRepo struct {
	*updateCountingRepo
	reads, flipAfter int
	target           uuid.UUID
}

func (r *flippingRepo) GetByID(ctx context.Context, tenantID, monitorID uuid.UUID) (*models.Monitor, error) {
	r.reads++
	if r.reads == r.flipAfter+1 {
		r.monitors[r.target].Type = models.MonitorTypeDomain
		r.monitors[r.target].Config = json.RawMessage(`{"domain":"example.com"}`)
	}
	return r.MockRepository.GetByID(ctx, tenantID, monitorID)
}

// The rule is asked again on the locked rows: a monitor that became a domain
// monitor after the up-front check refuses its batch, and the monitors of
// earlier batches, already committed, are counted.
func TestBulkUpdateAlerting_DomainThresholdRecheckedInBatch(t *testing.T) {
	for _, tc := range []struct {
		name        string
		monitors    int
		wantUpdated int
	}{
		{"single batch", 1, 0},
		{"second batch", BulkAlertingBatchSize + 1, BulkAlertingBatchSize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := &updateCountingRepo{MockRepository: NewMockRepository()}
			tenantID := uuid.New()
			ids := make([]uuid.UUID, 0, tc.monitors)
			for i := 0; i < tc.monitors; i++ {
				id := uuid.New()
				base.monitors[id] = &models.Monitor{ID: id, TenantID: tenantID, Name: "site", Type: models.MonitorTypeHTTP,
					Config: json.RawMessage(`{"url":"https://example.com"}`), IntervalSeconds: 3600, TimeoutSeconds: 10,
					ConsecutiveFailuresThreshold: 3, Enabled: true}
				ids = append(ids, id)
			}
			ordered := sortedIDs(ids)
			repo := &flippingRepo{updateCountingRepo: base, flipAfter: tc.monitors, target: ordered[len(ordered)-1]}
			svc := &Service{repo: repo, encryptor: secrets.NoOpEncryptor{}}

			one := 1
			updated, err := svc.BulkUpdateAlerting(context.Background(), tenantID, ids, &one, nil, nil, nil)
			if !errors.Is(err, ErrBulkAlertingRefused) {
				t.Fatalf("BulkUpdateAlerting() = %d, %v, want ErrBulkAlertingRefused", updated, err)
			}
			if updated != tc.wantUpdated || len(base.updated) != tc.wantUpdated {
				t.Fatalf("updated = %d (wrote %d), want %d", updated, len(base.updated), tc.wantUpdated)
			}
			for _, id := range base.updated {
				if id == repo.target {
					t.Fatal("the monitor that became a domain monitor was written")
				}
			}
		})
	}
}
