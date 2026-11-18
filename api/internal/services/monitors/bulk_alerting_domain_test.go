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
