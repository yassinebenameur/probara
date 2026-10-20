package models

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
)

// SIPMediaConfig is a test call's audio measurement (call.media). When set,
// the worker exchanges G.711 RTP with the address the far end's SDP answer
// names (vetted by netguard.Policy.AllowsServerChosen) for the hold and
// measures RFC 3550 loss and jitter, an E-model MOS-CQE estimate per evidence
// basis, the round trip of an echo, and whether audio flowed
// (worker/internal/worker/sip_media.go).
//
// Keys are strict: anything but an exact field name (a field of a later
// release, a case variant) makes Validate refuse the config, so a worker
// never silently skips an assertion it does not know. The decode itself never
// fails (UnmarshalJSON), so other readers of a SIP config, such as
// ValidateSIPSchedule and IsSIPCallConfig, always see the rest of it.
type SIPMediaConfig struct {
	// Expect is what the call must prove about audio (required):
	//   "none"  measure and report only; media never fails the check
	//   "audio" far-end audio reached the probe
	//   "echo"  the probe's own audio came back (an echo test line)
	Expect string `json:"expect"`

	// Thresholds apply to every measured basis (the far end's stream, and
	// the round trip of an echo). warn adds a warning; max and min fail the
	// check. All optional, and refused with expect "none".
	WarnLossPct  *float64 `json:"warn_loss_pct,omitempty"` // effective loss, (0, 100)
	MaxLossPct   *float64 `json:"max_loss_pct,omitempty"`
	WarnJitterMs *int     `json:"warn_jitter_ms,omitempty"` // mean RFC 3550 jitter, 1-1000
	MaxJitterMs  *int     `json:"max_jitter_ms,omitempty"`
	WarnMOS      *float64 `json:"warn_mos,omitempty"` // MOS-CQE estimate, [1, 4.3]
	MinMOS       *float64 `json:"min_mos,omitempty"`
	WarnRTTMs    *int     `json:"warn_rtt_ms,omitempty"` // echo round trip, 1-5000
	MaxRTTMs     *int     `json:"max_rtt_ms,omitempty"`

	// unknownKeys are the object's keys that are not an exact field name;
	// invalid is why the value could not be decoded (not an object, a field
	// of the wrong type). Validate refuses either.
	unknownKeys []string
	invalid     string
}

// What a test call's media must prove (SIPMediaConfig.Expect).
const (
	SIPMediaExpectNone  = "none"
	SIPMediaExpectAudio = "audio"
	SIPMediaExpectEcho  = "echo"
)

const (
	// SIPMediaMinHoldMs is the shortest hold a measured call may have: after
	// the 500 ms warm-up it leaves 2.5 s, past the 2 s and 50-packet gate a
	// quality estimate needs.
	SIPMediaMinHoldMs = 3000
	// SIPMediaMaxMOS stays below the model's ceiling: G.711 with the
	// reference 60 ms jitter buffer is MOS 4.37 at 20 ms ptime (4.35 at 60
	// ms) before any network delay or loss, so a higher threshold could never
	// pass.
	SIPMediaMaxMOS = 4.3
	// SIPMediaMinMOS is the bottom of the MOS scale.
	SIPMediaMinMOS = 1.0
)

// sipMediaKeys are SIPMediaConfig's exact json keys, read from its tags.
var sipMediaKeys = jsonFieldNames(reflect.TypeOf(SIPMediaConfig{}))

// UnmarshalJSON decodes the media object and records, rather than returns,
// what is wrong with it: a key that is not an exact field name, a value that
// is not an object, a field of the wrong type. It never fails, because an
// error here would abort the decode of the whole SIP config, and a reader
// that ignores the error (ValidateSIPSchedule) would then miss the keys
// after "media". Validate refuses whatever was recorded.
func (m *SIPMediaConfig) UnmarshalJSON(b []byte) error {
	if isJSONNull(b) {
		return nil // as encoding/json does for a pointer field
	}
	*m = SIPMediaConfig{}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil || fields == nil {
		m.invalid = "call.media: must be an object"
		return nil
	}
	for key := range fields {
		if !slices.Contains(sipMediaKeys, key) {
			m.unknownKeys = append(m.unknownKeys, key)
		}
	}
	slices.Sort(m.unknownKeys)

	// The alias drops this method; case variants were recorded above, so
	// encoding/json's case-insensitive matching changes nothing Validate
	// lets through.
	type plain SIPMediaConfig
	var decoded plain
	err := json.Unmarshal(b, &decoded)
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &typeErr) && typeErr.Field != "":
		m.invalid = fmt.Sprintf("call.media.%s must be a %s", typeErr.Field, sipMediaTypeName(typeErr.Type.String()))
	case err != nil:
		m.invalid = "call.media: must be an object"
	}
	unknown, invalid := m.unknownKeys, m.invalid
	*m = SIPMediaConfig(decoded)
	m.unknownKeys, m.invalid = unknown, invalid
	return nil
}

func sipMediaTypeName(goType string) string {
	switch goType {
	case "string":
		return "string"
	case "int":
		return "whole number"
	case "float64":
		return "number"
	}
	return "value of its documented type"
}

// Validate checks the media object's own fields. The rules that involve the
// rest of the call (answer mode, the hold, expected_status) are
// SIPCallConfig.validate's.
func (m SIPMediaConfig) Validate() error {
	if m.invalid != "" {
		return errors.New(m.invalid)
	}
	if len(m.unknownKeys) > 0 {
		return fmt.Errorf("call.media: unknown key %q", m.unknownKeys[0])
	}
	switch m.Expect {
	case SIPMediaExpectNone, SIPMediaExpectAudio, SIPMediaExpectEcho:
	case "":
		return fmt.Errorf("call.media.expect is required (none, audio or echo)")
	default:
		return fmt.Errorf("call.media.expect must be none, audio or echo")
	}

	if m.Expect == SIPMediaExpectNone {
		// A threshold on a call that asserts nothing about audio would pass
		// silently on a call that had none.
		for _, f := range []struct {
			name string
			set  bool
		}{
			{"warn_loss_pct", m.WarnLossPct != nil}, {"max_loss_pct", m.MaxLossPct != nil},
			{"warn_jitter_ms", m.WarnJitterMs != nil}, {"max_jitter_ms", m.MaxJitterMs != nil},
			{"warn_mos", m.WarnMOS != nil}, {"min_mos", m.MinMOS != nil},
			{"warn_rtt_ms", m.WarnRTTMs != nil}, {"max_rtt_ms", m.MaxRTTMs != nil},
		} {
			if f.set {
				return fmt.Errorf("call.media.%s requires expect audio or echo", f.name)
			}
		}
		return nil
	}

	for _, f := range []struct {
		name string
		v    *float64
	}{{"warn_loss_pct", m.WarnLossPct}, {"max_loss_pct", m.MaxLossPct}} {
		if f.v != nil && !(*f.v > 0 && *f.v < 100) {
			return fmt.Errorf("call.media.%s must be greater than 0 and less than 100", f.name)
		}
	}
	if m.WarnLossPct != nil && m.MaxLossPct != nil && *m.WarnLossPct >= *m.MaxLossPct {
		return fmt.Errorf("call.media.warn_loss_pct must be less than call.media.max_loss_pct")
	}
	if err := validateSIPMediaInts("jitter_ms", m.WarnJitterMs, m.MaxJitterMs, 1000); err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		v    *float64
	}{{"warn_mos", m.WarnMOS}, {"min_mos", m.MinMOS}} {
		if f.v != nil && (math.IsNaN(*f.v) || *f.v < SIPMediaMinMOS || *f.v > SIPMediaMaxMOS) {
			return fmt.Errorf("call.media.%s must be between %.1f and %.1f", f.name, SIPMediaMinMOS, SIPMediaMaxMOS)
		}
	}
	// A lower MOS is worse, so the warning sits above the failure.
	if m.WarnMOS != nil && m.MinMOS != nil && *m.WarnMOS <= *m.MinMOS {
		return fmt.Errorf("call.media.warn_mos must be greater than call.media.min_mos")
	}
	return validateSIPMediaInts("rtt_ms", m.WarnRTTMs, m.MaxRTTMs, 5000)
}

// validateSIPMediaInts bounds an integer warn/max pair to 1..limit, warn
// below max.
func validateSIPMediaInts(name string, warn, max *int, limit int) error {
	if warn != nil && (*warn < 1 || *warn > limit) {
		return fmt.Errorf("call.media.warn_%s must be between 1 and %d", name, limit)
	}
	if max != nil && (*max < 1 || *max > limit) {
		return fmt.Errorf("call.media.max_%s must be between 1 and %d", name, limit)
	}
	if warn != nil && max != nil && *warn >= *max {
		return fmt.Errorf("call.media.warn_%s must be less than call.media.max_%s", name, name)
	}
	return nil
}

// isJSONNull reports a JSON null literal.
func isJSONNull(b []byte) bool { return bytes.Equal(bytes.TrimSpace(b), []byte("null")) }
