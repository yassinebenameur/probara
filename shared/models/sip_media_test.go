package models

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSIPMediaConfigValidate(t *testing.T) {
	tests := []struct {
		name        string
		media       string
		holdMs      int    // 0: 3000
		errContains string // empty: must be valid
	}{
		{name: "measure only", media: `{"expect":"none"}`},
		{name: "audio", media: `{"expect":"audio"}`},
		{name: "echo with every threshold", media: `{"expect":"echo","warn_loss_pct":1,"max_loss_pct":5,"warn_jitter_ms":20,"max_jitter_ms":40,"warn_mos":4,"min_mos":3.6,"warn_rtt_ms":200,"max_rtt_ms":400}`},
		{name: "fractional loss", media: `{"expect":"audio","max_loss_pct":0.5}`},
		{name: "mos at its bounds", media: `{"expect":"audio","warn_mos":4.3,"min_mos":1}`},
		{name: "rtt with audio", media: `{"expect":"audio","max_rtt_ms":5000}`},
		{name: "two way with thresholds", media: `{"expect":"two_way","max_loss_pct":2,"min_mos":3.6,"max_rtt_ms":300}`, holdMs: 6000},

		{name: "expect missing", media: `{}`, errContains: "call.media.expect is required (none, audio, two_way or echo)"},
		{name: "expect empty", media: `{"expect":""}`, errContains: "call.media.expect is required"},
		{name: "expect unknown", media: `{"expect":"video"}`, errContains: "call.media.expect must be none, audio, two_way or echo"},
		{name: "expect in another case", media: `{"expect":"Two_Way"}`, errContains: "call.media.expect must be none, audio, two_way or echo"},
		// The far end's first RTCP report can take most of a 3 s hold.
		{name: "two way with a short hold", media: `{"expect":"two_way"}`, holdMs: 5999, errContains: "call.hold_ms must be at least 6000 when call.media.expect is two_way"},
		{name: "threshold with none", media: `{"expect":"none","max_loss_pct":2}`, errContains: "call.media.max_loss_pct requires expect audio, two_way or echo"},
		{name: "rtt threshold with none", media: `{"expect":"none","warn_rtt_ms":100}`, errContains: "call.media.warn_rtt_ms requires expect audio, two_way or echo"},
		{name: "zero loss", media: `{"expect":"audio","max_loss_pct":0}`, errContains: "call.media.max_loss_pct must be greater than 0 and less than 100"},
		{name: "full loss", media: `{"expect":"audio","warn_loss_pct":100}`, errContains: "call.media.warn_loss_pct must be greater than 0"},
		{name: "loss warn not below max", media: `{"expect":"audio","warn_loss_pct":2,"max_loss_pct":2}`, errContains: "call.media.warn_loss_pct must be less than call.media.max_loss_pct"},
		{name: "jitter zero", media: `{"expect":"audio","warn_jitter_ms":0}`, errContains: "call.media.warn_jitter_ms must be between 1 and 1000"},
		{name: "jitter too high", media: `{"expect":"audio","max_jitter_ms":1001}`, errContains: "call.media.max_jitter_ms must be between 1 and 1000"},
		{name: "jitter warn not below max", media: `{"expect":"audio","warn_jitter_ms":30,"max_jitter_ms":20}`, errContains: "call.media.warn_jitter_ms must be less than call.media.max_jitter_ms"},
		{name: "mos above the model's ceiling", media: `{"expect":"audio","min_mos":4.4}`, errContains: "call.media.min_mos must be between 1.0 and 4.3"},
		{name: "mos below the scale", media: `{"expect":"audio","warn_mos":0.9}`, errContains: "call.media.warn_mos must be between 1.0 and 4.3"},
		{name: "mos warn not above min", media: `{"expect":"audio","warn_mos":3.6,"min_mos":3.6}`, errContains: "call.media.warn_mos must be greater than call.media.min_mos"},
		{name: "rtt too high", media: `{"expect":"echo","warn_rtt_ms":5001}`, errContains: "call.media.warn_rtt_ms must be between 1 and 5000"},
		{name: "rtt warn not below max", media: `{"expect":"echo","warn_rtt_ms":400,"max_rtt_ms":300}`, errContains: "call.media.warn_rtt_ms must be less than call.media.max_rtt_ms"},

		// Strict keys: a later release's field, or a case variant, fails
		// closed instead of being skipped.
		{name: "unknown key", media: `{"expect":"audio","dtmf":"123"}`, errContains: `call.media: unknown key "dtmf"`},
		{name: "first unknown key in order", media: `{"expect":"audio","zz":1,"aa":2}`, errContains: `call.media: unknown key "aa"`},
		{name: "case variant", media: `{"Expect":"audio"}`, errContains: `call.media: unknown key "Expect"`},
		{name: "case variant beside the field", media: `{"expect":"audio","MAX_LOSS_PCT":2}`, errContains: `call.media: unknown key "MAX_LOSS_PCT"`},

		// Wrong types are recorded by the decode and refused here.
		{name: "string", media: `"on"`, errContains: "call.media: must be an object"},
		{name: "number", media: `5`, errContains: "call.media: must be an object"},
		{name: "array", media: `[]`, errContains: "call.media: must be an object"},
		{name: "bool", media: `true`, errContains: "call.media: must be an object"},
		{name: "expect not a string", media: `{"expect":5}`, errContains: "call.media.expect must be a string"},
		{name: "threshold not a number", media: `{"expect":"audio","max_loss_pct":"2"}`, errContains: "call.media.max_loss_pct must be a number"},
		{name: "fractional jitter", media: `{"expect":"audio","max_jitter_ms":2.5}`, errContains: "call.media.max_jitter_ms must be a whole number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hold := tt.holdMs
			if hold == 0 {
				hold = 3000
			}
			raw := fmt.Sprintf(`{"host":"trunk.example.com","method":"invite","call":{"number":"100","hold_ms":%d,"media":%s}}`, hold, tt.media)
			var cfg SIPMonitorConfig
			if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
				t.Fatalf("unmarshal: %v (the media decode must never fail)", err)
			}
			if cfg.Call.Media == nil {
				t.Fatal("media decoded as absent")
			}
			err := cfg.Validate()
			if tt.errContains == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.errContains) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.errContains)
			}
		})
	}
}

// A media value the decode cannot use never aborts the decode of the config
// around it: ValidateSIPSchedule ignores decode errors, so an aborted decode
// would read hold_ms (placed after media) as 0 and pass a schedule the hold
// does not fit.
func TestSIPMediaConfigDecodeNeverFails(t *testing.T) {
	for _, media := range []string{`"on"`, `5`, `[]`, `{"expect":5}`, `{"expect":"audio","max_jitter_ms":"x"}`, `{"dtmf":{}}`} {
		raw := json.RawMessage(`{"host":"trunk.example.com","method":"invite","call":{"number":"100","media":` + media + `,"hold_ms":9000}}`)
		var cfg SIPMonitorConfig
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("media %s: unmarshal = %v, want nil", media, err)
		}
		if cfg.Call.HoldMs != 9000 {
			t.Fatalf("media %s: hold_ms = %d after media, want 9000", media, cfg.Call.HoldMs)
		}
		if err := ValidateSIPSchedule(raw, 60, 10); err == nil || !strings.Contains(err.Error(), "must fit in timeout_seconds") {
			t.Fatalf("media %s: ValidateSIPSchedule() = %v, want the hold to count", media, err)
		}
		if !IsSIPCallConfig("sip", raw) {
			t.Fatalf("media %s: IsSIPCallConfig = false", media)
		}
	}
}

func TestSIPMediaConfigNullAndRoundTrip(t *testing.T) {
	var cfg SIPCallConfig
	if err := json.Unmarshal([]byte(`{"number":"100","media":null}`), &cfg); err != nil || cfg.Media != nil {
		t.Fatalf("null media = %+v, %v; want absent", cfg.Media, err)
	}
	in := `{"number":"100","hold_ms":5000,"media":{"expect":"echo","max_loss_pct":2.5,"min_mos":3.6}}`
	if err := json.Unmarshal([]byte(in), &cfg); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"media":{"expect":"echo","max_loss_pct":2.5,"min_mos":3.6}`) {
		t.Fatalf("marshal = %s, want the media object as given", out)
	}
	// An unknown key survives neither the struct nor its marshal; the API
	// refuses it on save, so nothing stored depends on it.
	var strict SIPCallConfig
	_ = json.Unmarshal([]byte(`{"number":"100","media":{"expect":"audio","dtmf":"1"}}`), &strict)
	if out, _ := json.Marshal(strict); strings.Contains(string(out), "dtmf") {
		t.Fatalf("marshal = %s, want the unknown key dropped", out)
	}
}

// A media payload with a key this release does not know still reads as a
// call everywhere a call is gated (the tenant-admin gate, the schedule floor,
// the worker's at-most-once delivery).
func TestIsSIPCallConfigWithStrictMediaKeys(t *testing.T) {
	for _, raw := range []string{
		`{"host":"x","method":"invite","call":{"number":"1","hold_ms":3000,"media":{"expect":"audio","dtmf":"1"}}}`,
		`{"host":"x","method":"invite","call":{"number":"1","media":"on"}}`,
		`{"host":"x","call":{"number":"1","media":{"Expect":"audio"}}}`,
	} {
		if !IsSIPCallConfig("sip", json.RawMessage(raw)) {
			t.Errorf("IsSIPCallConfig(%s) = false, want true", raw)
		}
	}
}
