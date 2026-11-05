package validation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
)

func TestValidateMonitor_TCPTLSFields(t *testing.T) {
	req := func(config string) *models.CreateMonitorRequest {
		return &models.CreateMonitorRequest{Name: "smtps", Type: models.MonitorTypeTCP, Config: json.RawMessage(config), IntervalSeconds: 60, TimeoutSeconds: 10}
	}
	if err := ValidateMonitor(req(`{"host":"mail.example.com","port":465,"use_tls":true,"tls_min_days_valid":14,"tls_check_chain":true}`)); err != nil {
		t.Fatalf("tls tcp monitor rejected: %v", err)
	}
	if err := ValidateMonitor(req(`{"host":"mail.example.com","port":25,"tls_min_days_valid":14}`)); err == nil || !strings.Contains(err.Error(), "requires use_tls") {
		t.Fatalf("tls_min_days_valid without use_tls: %v", err)
	}
}
