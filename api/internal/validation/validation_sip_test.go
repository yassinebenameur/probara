package validation

import (
	"encoding/json"
	"testing"

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
		`{"host":"sip.example.com","tls_server_name":"bad name"}`,
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

func TestSIPConfigValidatorRejectsMalformedJSON(t *testing.T) {
	err := (&SIPConfigValidator{}).ValidateConfig(json.RawMessage(`{"host":42}`))
	if err == nil {
		t.Fatal("ValidateConfig() = nil, want an invalid SIP config error")
	}
}
