package validation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/api/internal/models"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
)

// The SIP rules live on sharedmodels.SIPMonitorConfig (rule table in
// shared/models/sip_test.go). This pins that the API validator and the
// registry entry delegate to them: for every config, the registry's verdict
// is exactly the shared Validate verdict.
func TestSIPConfigValidatorDelegatesToSharedConfig(t *testing.T) {
	configs := []string{
		`{"host":"sip.example.com"}`,
		`{"host":"sip.example.com","method":"register","username":"agent","password":"***"}`,
		`{"host":"sip.example.com","transport":"sctp"}`,
		`{"host":"sip.example.com","method":"invite"}`,
		`{"host":"sip.example.com","password":"secret"}`,
		`{"host":"sip.example.com","expected_status":42}`,
		`{"host":"sip.example.com","domain":"example.com\r\nX-Injected: 1"}`,
		`{"transport":"udp"}`,
	}
	for _, raw := range configs {
		var cfg sharedmodels.SIPMonitorConfig
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
		want := cfg.Validate()
		got := DefaultRegistry.Validate("sip", json.RawMessage(raw))
		if (got == nil) != (want == nil) || (got != nil && got.Error() != want.Error()) {
			t.Errorf("%s: registry = %v, shared Validate = %v", raw, got, want)
		}
	}
}

// A key spelled in another case would be validated as one config and
// stored as another (sharedmodels.ParseSIPMonitorConfig): refused, at the
// top level and inside call, and the schedule floor applies whatever the
// struct decode says.
func TestSIPConfigValidatorRejectsCaseVariantKeys(t *testing.T) {
	for _, raw := range []string{
		`{"host":"trunk.example.com","method":"invite","call":{"number":"+33123"},"Method":"options","Call":null}`,
		`{"host":"trunk.example.com","method":"invite","call":{"number":"+33123","Number":"+44999"}}`,
	} {
		err := DefaultRegistry.Validate("sip", json.RawMessage(raw))
		if err == nil || !strings.Contains(err.Error(), "must be spelled") {
			t.Errorf("%s: Validate() = %v, want a case-variant key refused", raw, err)
		}
	}
	err := (&SIPConfigValidator{}).ValidateSchedule(json.RawMessage(`{"host":"trunk.example.com","method":"invite","call":{"number":"1"},"Method":"options","Call":null}`), 30, 10)
	if err == nil || !strings.Contains(err.Error(), "at least 60") {
		t.Errorf("ValidateSchedule() = %v, want the call floor", err)
	}
}

// tls_server_name's syntax is checked on save only (the worker leaves it to
// the handshake, so monitors stored before the rule keep running); an
// absolute name with a trailing dot is fine.
func TestSIPConfigValidatorTLSServerName(t *testing.T) {
	for raw, ok := range map[string]bool{
		`{"host":"sip.example.com","transport":"tls","tls_server_name":"bad_name"}`:         false,
		`{"host":"sip.example.com","transport":"tls","tls_server_name":"bad name"}`:         false,
		`{"host":"sip.example.com","transport":"tls","tls_server_name":"sbc.example.com."}`: true,
	} {
		if err := DefaultRegistry.Validate("sip", json.RawMessage(raw)); (err == nil) != ok {
			t.Errorf("%s: Validate() = %v, want ok=%v", raw, err, ok)
		}
	}
}

func TestSIPConfigValidatorRejectsMalformedJSON(t *testing.T) {
	err := (&SIPConfigValidator{}).ValidateConfig(json.RawMessage(`{"host":42}`))
	if err == nil {
		t.Fatal("ValidateConfig() = nil, want an invalid SIP config error")
	}
}

const (
	sipCallConfig    = `{"host":"trunk.example.com","method":"invite","call":{"number":"+33123456789","hold_ms":2000}}`
	sipOptionsConfig = `{"host":"trunk.example.com","method":"options"}`
)

func intPtr(v int) *int { return &v }

func sipMonitor(config string, interval, timeout int) *models.Monitor {
	return &models.Monitor{Type: models.MonitorTypeSIP, Config: json.RawMessage(config), IntervalSeconds: interval, TimeoutSeconds: timeout}
}

// A test call's schedule bounds hold on create.
func TestValidateMonitor_SIPCallSchedule(t *testing.T) {
	base := func(interval, timeout int) *models.CreateMonitorRequest {
		return &models.CreateMonitorRequest{Name: "call", Type: models.MonitorTypeSIP, Config: json.RawMessage(sipCallConfig), IntervalSeconds: interval, TimeoutSeconds: timeout}
	}
	if err := ValidateMonitor(base(60, 20)); err != nil {
		t.Fatalf("ValidateMonitor(60/20) = %v, want nil", err)
	}
	if err := ValidateMonitor(base(30, 10)); err == nil || !strings.Contains(err.Error(), "interval_seconds must be at least 60") {
		t.Fatalf("ValidateMonitor(30/10) = %v, want interval floor", err)
	}
	if err := ValidateMonitor(base(120, 30)); err == nil || !strings.Contains(err.Error(), "timeout_seconds must be at most 20") {
		t.Fatalf("ValidateMonitor(120/30) = %v, want timeout cap", err)
	}
	if err := ValidateMonitor(base(60, 3)); err == nil || !strings.Contains(err.Error(), "must fit in timeout_seconds") {
		t.Fatalf("ValidateMonitor(60/3, hold 2s) = %v, want hold budget", err)
	}
	// OPTIONS keeps the generic schedule.
	options := base(10, 5)
	options.Config = json.RawMessage(sipOptionsConfig)
	if err := ValidateMonitor(options); err != nil {
		t.Fatalf("ValidateMonitor(options 10/5) = %v, want nil", err)
	}
}

// An update is checked as the monitor will be after it, so a PATCH that
// carries only the interval, only the timeout or only the type still meets
// the config's schedule rules, which the config validator alone would skip.
func TestValidateMonitorUpdate_SIPCallEffectiveSchedule(t *testing.T) {
	existing := sipMonitor(sipCallConfig, 60, 10)
	tests := []struct {
		name        string
		req         models.UpdateMonitorRequest
		errContains string
	}{
		{name: "interval-only patch below the floor", req: models.UpdateMonitorRequest{IntervalSeconds: intPtr(30)}, errContains: "interval_seconds must be at least 60"},
		{name: "interval-only patch above the floor", req: models.UpdateMonitorRequest{IntervalSeconds: intPtr(300)}},
		{name: "timeout-only patch above the cap", req: models.UpdateMonitorRequest{TimeoutSeconds: intPtr(25)}, errContains: "timeout_seconds must be at most 20"},
		{name: "config-only patch whose hold no longer fits", req: models.UpdateMonitorRequest{Config: json.RawMessage(`{"host":"trunk.example.com","method":"invite","call":{"number":"1","hold_ms":9000}}`)}, errContains: "must fit in timeout_seconds"},
		{name: "unrelated patch", req: models.UpdateMonitorRequest{Name: strPtr("renamed")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMonitorUpdate(&tt.req, existing)
			if tt.errContains == "" {
				if err != nil {
					t.Fatalf("ValidateMonitorUpdate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.errContains) {
				t.Fatalf("ValidateMonitorUpdate() = %v, want error containing %q", err, tt.errContains)
			}
		})
	}
}

// Switching between a test call and a probe, or changing the type to or from
// a test call, is refused: create a new monitor instead.
func TestValidateMonitorUpdate_SIPMethodClassSwitch(t *testing.T) {
	sipType := models.MonitorTypeSIP
	httpType := models.MonitorTypeHTTP
	cases := []struct {
		name     string
		existing *models.Monitor
		req      models.UpdateMonitorRequest
		refused  bool
	}{
		{name: "options to invite", existing: sipMonitor(sipOptionsConfig, 60, 10), req: models.UpdateMonitorRequest{Config: json.RawMessage(sipCallConfig)}, refused: true},
		{name: "invite to options", existing: sipMonitor(sipCallConfig, 60, 10), req: models.UpdateMonitorRequest{Config: json.RawMessage(sipOptionsConfig)}, refused: true},
		{name: "invite to register", existing: sipMonitor(sipCallConfig, 60, 10), req: models.UpdateMonitorRequest{Config: json.RawMessage(`{"host":"trunk.example.com","method":"register"}`)}, refused: true},
		{name: "type-only patch away from invite", existing: sipMonitor(sipCallConfig, 60, 10), req: models.UpdateMonitorRequest{Type: &httpType}, refused: true},
		{name: "http to sip invite", existing: &models.Monitor{Type: models.MonitorTypeHTTP, Config: json.RawMessage(`{"url":"https://example.com"}`), IntervalSeconds: 60, TimeoutSeconds: 10}, req: models.UpdateMonitorRequest{Type: &sipType, Config: json.RawMessage(sipCallConfig)}, refused: true},
		{name: "invite stays invite", existing: sipMonitor(sipCallConfig, 60, 10), req: models.UpdateMonitorRequest{Config: json.RawMessage(`{"host":"trunk.example.com","method":"invite","call":{"number":"200","mode":"ring"}}`)}},
		{name: "options to register", existing: sipMonitor(sipOptionsConfig, 60, 10), req: models.UpdateMonitorRequest{Config: json.RawMessage(`{"host":"trunk.example.com","method":"register"}`)}},
		{name: "masked password does not matter", existing: sipMonitor(`{"host":"t.example.com","method":"invite","username":"u","password":"***","call":{"number":"1"}}`, 60, 10), req: models.UpdateMonitorRequest{IntervalSeconds: intPtr(120)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMonitorUpdate(&tc.req, tc.existing)
			if tc.refused {
				if err == nil || !strings.Contains(err.Error(), "create a new monitor") {
					t.Fatalf("ValidateMonitorUpdate() = %v, want the method-class refusal", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateMonitorUpdate() = %v, want nil", err)
			}
		})
	}
}

func TestRegistryValidateScheduleOnlyForScheduleAwareTypes(t *testing.T) {
	if err := DefaultRegistry.ValidateSchedule(models.MonitorTypeHTTP, json.RawMessage(`{}`), 10, 5); err != nil {
		t.Fatalf("http ValidateSchedule() = %v, want nil", err)
	}
	if err := DefaultRegistry.ValidateSchedule("nope", nil, 10, 5); err == nil {
		t.Fatal("unknown type ValidateSchedule() = nil, want error")
	}
	if _, ok := interface{}(&SIPConfigValidator{}).(ScheduleAwareValidator); !ok {
		t.Fatal("SIPConfigValidator does not implement ScheduleAwareValidator")
	}
}
