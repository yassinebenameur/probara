package validation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/yassinebenameur/probara/api/internal/models"
)

func TestDomainConfigValidator(t *testing.T) {
	v := &DomainConfigValidator{}
	for _, raw := range []string{
		`{"domain":"example.com"}`,
		`{"domain":"www.example.co.uk","min_days_valid":14}`,
		`{"domain":"bücher.de","min_days_valid":0}`,
	} {
		if err := v.ValidateConfig(json.RawMessage(raw)); err != nil {
			t.Errorf("ValidateConfig(%s) = %v", raw, err)
		}
	}
	for raw, want := range map[string]string{
		`{}`:                          "domain is required",
		`{"domain":"10.0.0.1"}`:       "not an IP address",
		`{"domain":"com"}`:            "public suffix",
		`{"domain":"example.com:43"}`: "bare domain name",
		`{"domain":"example.com","min_days_valid":-5}`: "min_days_valid",
		`[]`: "invalid domain config",
	} {
		if err := v.ValidateConfig(json.RawMessage(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ValidateConfig(%s) = %v, want %q", raw, err, want)
		}
	}
}

func TestValidateMonitor_DomainSchedule(t *testing.T) {
	create := func(interval, timeout int) *models.CreateMonitorRequest {
		return &models.CreateMonitorRequest{
			Name: "example.com registration", Type: models.MonitorTypeDomain,
			Config: json.RawMessage(`{"domain":"example.com"}`), IntervalSeconds: interval, TimeoutSeconds: timeout,
		}
	}
	if err := ValidateMonitor(create(86400, 30)); err != nil {
		t.Fatalf("daily: %v", err)
	}
	if err := ValidateMonitor(create(3600, 30)); err != nil {
		t.Fatalf("hourly: %v", err)
	}
	if err := ValidateMonitor(create(1800, 30)); err == nil || !strings.Contains(err.Error(), "at least 3600") {
		t.Fatalf("30 min: %v", err)
	}
	// An active type: the timeout rules apply.
	if err := ValidateMonitor(create(3600, 0)); err == nil {
		t.Fatal("zero timeout accepted")
	}
	if !IsActiveCheckType(models.MonitorTypeDomain) {
		t.Fatal("domain must be an active check type")
	}

	existing := &models.Monitor{
		Name: "example.com registration", Type: models.MonitorTypeDomain,
		Config: json.RawMessage(`{"domain":"example.com"}`), IntervalSeconds: 86400, TimeoutSeconds: 30,
		ConsecutiveFailuresThreshold: 2,
	}
	short := 600
	if err := ValidateMonitorUpdate(&models.UpdateMonitorRequest{IntervalSeconds: &short}, existing); err == nil ||
		!strings.Contains(err.Error(), "at least 3600") {
		t.Fatalf("update to 10 min: %v", err)
	}
	httpType := models.MonitorTypeHTTP
	toDomain := models.MonitorTypeDomain
	// Switching another type to domain applies the floor to the kept
	// interval.
	httpMonitor := &models.Monitor{Name: "site", Type: httpType, Config: json.RawMessage(`{"url":"https://example.com","method":"GET"}`), IntervalSeconds: 60, TimeoutSeconds: 10, ConsecutiveFailuresThreshold: 2}
	if err := ValidateMonitorUpdate(&models.UpdateMonitorRequest{Type: &toDomain, Config: json.RawMessage(`{"domain":"example.com"}`)}, httpMonitor); err == nil ||
		!strings.Contains(err.Error(), "at least 3600") {
		t.Fatalf("type switch: %v", err)
	}
}

// One registry answer must not take a domain down, and a registry answers
// the same from every location.
func TestValidateMonitor_DomainThresholdAndLocations(t *testing.T) {
	one, three := 1, 3
	create := func(threshold *int, locations []string) error {
		return ValidateMonitor(&models.CreateMonitorRequest{
			Name: "d", Type: models.MonitorTypeDomain, Config: json.RawMessage(`{"domain":"example.com"}`),
			IntervalSeconds: 86400, TimeoutSeconds: 30, ConsecutiveFailuresThreshold: threshold, LocationIDs: locations,
		})
	}
	loc := func() string { return uuid.NewString() }
	if err := create(nil, nil); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if err := create(&three, []string{loc()}); err != nil {
		t.Fatalf("one location: %v", err)
	}
	if err := create(&one, nil); err == nil || !strings.Contains(err.Error(), "at least 2") {
		t.Fatalf("threshold 1: %v", err)
	}
	if err := create(nil, []string{loc(), loc()}); err == nil || !strings.Contains(err.Error(), "at most 1 location") {
		t.Fatalf("two locations: %v", err)
	}

	existing := &models.Monitor{
		Name: "d", Type: models.MonitorTypeDomain, Config: json.RawMessage(`{"domain":"example.com"}`),
		IntervalSeconds: 86400, TimeoutSeconds: 30, ConsecutiveFailuresThreshold: 2,
	}
	if err := ValidateMonitorUpdate(&models.UpdateMonitorRequest{ConsecutiveFailuresThreshold: &one}, existing); err == nil {
		t.Fatal("update to threshold 1 accepted")
	}
	two := []string{loc(), loc()}
	if err := ValidateMonitorUpdate(&models.UpdateMonitorRequest{LocationIDs: &two}, existing); err == nil {
		t.Fatal("update to two locations accepted")
	}
	// Retyping a monitor checks the effective threshold and locations.
	domain := models.MonitorTypeDomain
	httpMonitor := &models.Monitor{
		Name: "site", Type: models.MonitorTypeHTTP, Config: json.RawMessage(`{"url":"https://example.com","method":"GET"}`),
		IntervalSeconds: 86400, TimeoutSeconds: 30, ConsecutiveFailuresThreshold: 1,
		LocationIDs: []uuid.UUID{uuid.New()},
	}
	if err := ValidateMonitorUpdate(&models.UpdateMonitorRequest{Type: &domain, Config: json.RawMessage(`{"domain":"example.com"}`)}, httpMonitor); err == nil ||
		!strings.Contains(err.Error(), "at least 2") {
		t.Fatalf("retype keeping threshold 1: %v", err)
	}
}
