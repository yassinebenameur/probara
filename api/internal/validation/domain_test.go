package validation

import (
	"encoding/json"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
)

func TestDomainValidatorRegistered(t *testing.T) {
	if !DefaultRegistry.Has(models.MonitorTypeDomain) || !IsActiveCheckType(models.MonitorTypeDomain) {
		t.Fatal("domain must be a registered active type")
	}
	if err := DefaultRegistry.Validate(models.MonitorTypeDomain, json.RawMessage(`{"domain":"example.com","min_days_valid":30}`)); err != nil {
		t.Fatalf("valid config refused: %v", err)
	}
	if err := DefaultRegistry.Validate(models.MonitorTypeDomain, json.RawMessage(`{"domain":"www"}`)); err == nil {
		t.Fatal("a single-label name must be refused")
	}
	if err := DefaultRegistry.ValidateSchedule(models.MonitorTypeDomain, json.RawMessage(`{"domain":"example.com"}`), 600, 30); err == nil {
		t.Fatal("a 10-minute interval must be refused")
	}
	if err := DefaultRegistry.ValidateSchedule(models.MonitorTypeDomain, json.RawMessage(`{"domain":"example.com"}`), 43200, 30); err != nil {
		t.Fatalf("12h interval refused: %v", err)
	}
}
