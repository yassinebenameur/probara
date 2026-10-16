package models

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// SIPCallConfig is the test call of a `sip` monitor with method "invite": the
// worker places a real call (INVITE → 18x → 200 → ACK → hold → BYE, or CANCEL
// once it rings in ring mode) over the monitor's transport and digest
// credentials, and measures post-dial delay and answer time. Calls can be
// billed, so the API keeps them to tenant admins and bounds the schedule
// (ValidateSchedule), and each worker can refuse them (SIP_CALL_ENABLED,
// SIP_CALL_ALLOWED_DESTINATIONS).
type SIPCallConfig struct {
	// Number is the called user part: sip:<number>@<domain or host>, with
	// ;user=phone when it is a telephone number (digits, optional leading +).
	// Exclusive with TargetURI.
	Number string `json:"number,omitempty"`
	// TargetURI is a full sip: or sips: Request-URI. Exclusive with Number.
	// Only the message carries it: the worker always sends to the monitor's
	// host, never to this URI's host.
	TargetURI string `json:"target_uri,omitempty"`
	// FromUser, FromDisplay and FromDomain make the caller ID; the user
	// defaults to the digest username, else "probara", and the domain to
	// the monitor's domain, else its host.
	FromUser    string `json:"from_user,omitempty"`
	FromDisplay string `json:"from_display,omitempty"`
	FromDomain  string `json:"from_domain,omitempty"`
	// Mode is "answer" (default: wait for 200, hold, hang up) or "ring"
	// (success on the first 18x, then CANCEL: no answered call to pay for).
	Mode string `json:"mode,omitempty"`
	// HoldMs keeps an answered call up before the BYE (answer mode only).
	HoldMs int `json:"hold_ms,omitempty"`
	// Post-dial delay (INVITE → first 18x) and answer time (INVITE → 200)
	// thresholds: warn adds a warning, max fails the check.
	WarnPDDMs    *int `json:"warn_pdd_ms,omitempty"`
	MaxPDDMs     *int `json:"max_pdd_ms,omitempty"`
	WarnAnswerMs *int `json:"warn_answer_ms,omitempty"`
	MaxAnswerMs  *int `json:"max_answer_ms,omitempty"`
	// SDPAddress replaces the offer's o=/c= address (a NATed worker's public
	// address). The worker never sends media.
	SDPAddress string `json:"sdp_address,omitempty"`
	// ProbeHeader adds X-Probara-Probe: 1 so the far end can recognise
	// probe calls.
	ProbeHeader bool `json:"probe_header,omitempty"`
	// Media is reserved for RTP quality analysis and must be absent.
	Media json.RawMessage `json:"media,omitempty"`
}

// Test-call modes.
const (
	SIPCallModeAnswer = "answer"
	SIPCallModeRing   = "ring"
)

// Test-call bounds. The worst case is SIPCallMaxTimeoutSeconds plus the
// worker's fixed teardown reserve (4 s), inside the check-job AckWait (30 s).
const (
	SIPCallMinIntervalSeconds = 60
	SIPCallMaxTimeoutSeconds  = 20
	SIPCallMaxHoldMs          = 10000
	// SIPCallMinAnswerBudgetMs is what the timeout must leave for ringing and
	// answering once the hold is taken out of it.
	SIPCallMinAnswerBudgetMs = 2000
	sipCallMaxURIBytes       = 512
	sipCallMaxDisplayChars   = 64
)

// sipCallUserPattern is the user-part charset for number and from_user: what
// dial strings use (digits, letters, + * #) plus RFC 3261 unreserved marks.
// Nothing that needs quoting or could end the URI.
var sipCallUserPattern = regexp.MustCompile(`^[0-9A-Za-z+*#._~-]{1,64}$`)

// sipCallURIUserRaw is how a target_uri user part may be written: the
// unescaped characters of sipCallUserPattern ('#' must be escaped in a SIP
// user part, RFC 3261 §25.1) or percent-escapes. No ';' (user parameters
// such as phone-context, which change what the far end dials) and no ':'
// (a password): the user part is exactly the dialled string, so the worker's
// SIP_CALL_ALLOWED_DESTINATIONS check sees everything the proxy acts on.
var sipCallURIUserRaw = regexp.MustCompile(`^(?:[0-9A-Za-z+*._~-]|%[0-9A-Fa-f]{2})+$`)

// sipTelephoneNumber is a number that gets ;user=phone.
var sipTelephoneNumber = regexp.MustCompile(`^\+?[0-9]+$`)

// IsValidSIPCallUser reports a user part number and from_user accept.
func IsValidSIPCallUser(s string) bool {
	return sipCallUserPattern.MatchString(s)
}

// EffectiveMode is the call mode, "answer" when unset.
func (c SIPCallConfig) EffectiveMode() string {
	if strings.EqualFold(strings.TrimSpace(c.Mode), SIPCallModeRing) {
		return SIPCallModeRing
	}
	return SIPCallModeAnswer
}

// IsTelephoneNumber reports whether Number gets ;user=phone.
func (c SIPCallConfig) IsTelephoneNumber() bool {
	return sipTelephoneNumber.MatchString(c.Number)
}

func (c *SIPCallConfig) validate(expectedStatus *int) error {
	for _, f := range []struct{ name, value string }{
		{"call.number", c.Number},
		{"call.target_uri", c.TargetURI},
		{"call.from_user", c.FromUser},
		{"call.from_display", c.FromDisplay},
		{"call.from_domain", c.FromDomain},
		{"call.mode", c.Mode},
		{"call.sdp_address", c.SDPAddress},
	} {
		if strings.ContainsAny(f.value, "\r\n\x00") {
			return fmt.Errorf("%s must not contain CR, LF or NUL characters", f.name)
		}
	}

	switch {
	case c.Number == "" && c.TargetURI == "":
		return fmt.Errorf("call.number or call.target_uri is required")
	case c.Number != "" && c.TargetURI != "":
		return fmt.Errorf("call.number and call.target_uri are mutually exclusive")
	case c.Number != "" && !sipCallUserPattern.MatchString(c.Number):
		return fmt.Errorf("call.number must be 1-64 characters of digits, letters and + * # . _ ~ -")
	case c.TargetURI != "":
		if _, err := ParseSIPCallURI(c.TargetURI); err != nil {
			return fmt.Errorf("call.target_uri: %w", err)
		}
	}

	if c.FromUser != "" && !sipCallUserPattern.MatchString(c.FromUser) {
		return fmt.Errorf("call.from_user must be 1-64 characters of digits, letters and + * # . _ ~ -")
	}
	if c.FromDisplay != "" {
		if len([]rune(c.FromDisplay)) > sipCallMaxDisplayChars {
			return fmt.Errorf("call.from_display must be at most %d characters", sipCallMaxDisplayChars)
		}
		for _, r := range c.FromDisplay {
			// The display name is sent as a quoted string: a quote or a
			// backslash would end or escape it, a control character has no
			// place in a header.
			if r == '"' || r == '\\' || r < 0x20 || r == 0x7f {
				return fmt.Errorf("call.from_display must be printable characters without \" or \\")
			}
		}
	}
	if c.FromDomain != "" && !isHostOrIP(c.FromDomain) {
		return fmt.Errorf("call.from_domain must be a valid hostname or IP address")
	}
	if c.SDPAddress != "" && !isHostOrIP(c.SDPAddress) {
		return fmt.Errorf("call.sdp_address must be a valid hostname or IP address")
	}

	switch strings.ToLower(c.Mode) {
	case "", SIPCallModeAnswer, SIPCallModeRing:
	default:
		return fmt.Errorf("call.mode must be 'answer' or 'ring'")
	}
	ring := c.EffectiveMode() == SIPCallModeRing

	if c.HoldMs < 0 || c.HoldMs > SIPCallMaxHoldMs {
		return fmt.Errorf("call.hold_ms must be between 0 and %d", SIPCallMaxHoldMs)
	}
	if ring && c.HoldMs != 0 {
		return fmt.Errorf("call.hold_ms is not allowed in ring mode (the call is never answered)")
	}

	if err := validateSIPCallThresholds("pdd", c.WarnPDDMs, c.MaxPDDMs); err != nil {
		return err
	}
	if err := validateSIPCallThresholds("answer", c.WarnAnswerMs, c.MaxAnswerMs); err != nil {
		return err
	}
	if ring && (c.WarnAnswerMs != nil || c.MaxAnswerMs != nil) {
		return fmt.Errorf("call.warn_answer_ms and call.max_answer_ms are not allowed in ring mode")
	}

	if expectedStatus != nil {
		if ring {
			// Ring mode succeeds on ringing, never on a final response.
			return fmt.Errorf("expected_status is not allowed in ring mode")
		}
		if *expectedStatus < 200 {
			return fmt.Errorf("expected_status must be a final response (200-699) for a test call")
		}
	}

	if len(c.Media) > 0 && strings.TrimSpace(string(c.Media)) != "null" {
		return fmt.Errorf("call.media: media analysis is not available yet")
	}
	return nil
}

// validateSIPCallThresholds applies the latency-threshold rule every monitor
// uses: each one positive, warn below max when both are set.
func validateSIPCallThresholds(name string, warn, max *int) error {
	if warn != nil && *warn <= 0 {
		return fmt.Errorf("call.warn_%s_ms must be greater than 0", name)
	}
	if max != nil && *max <= 0 {
		return fmt.Errorf("call.max_%s_ms must be greater than 0", name)
	}
	if warn != nil && max != nil && *warn >= *max {
		return fmt.Errorf("call.warn_%s_ms must be less than call.max_%s_ms", name, name)
	}
	return nil
}

// ValidateSchedule bounds a test call's schedule: at most one call a minute
// per location, a timeout the worker can finish (with teardown) inside the
// check-job AckWait, and enough of it left after the hold to ring and
// answer. Other methods have no extra schedule rules.
func (c SIPMonitorConfig) ValidateSchedule(intervalSeconds, timeoutSeconds int) error {
	if !c.IsCall() {
		return nil
	}
	if intervalSeconds < SIPCallMinIntervalSeconds {
		return fmt.Errorf("interval_seconds must be at least %d for a SIP test call (each check places a real call)", SIPCallMinIntervalSeconds)
	}
	if timeoutSeconds > SIPCallMaxTimeoutSeconds {
		return fmt.Errorf("timeout_seconds must be at most %d for a SIP test call", SIPCallMaxTimeoutSeconds)
	}
	hold := 0
	if c.Call != nil {
		hold = c.Call.HoldMs
	}
	if hold+SIPCallMinAnswerBudgetMs > timeoutSeconds*1000 {
		return fmt.Errorf("call.hold_ms plus %d ms to ring and answer must fit in timeout_seconds", SIPCallMinAnswerBudgetMs)
	}
	return nil
}

// SIPCallURI is a parsed test-call target URI.
type SIPCallURI struct {
	Scheme string // sip or sips, lowercase
	User   string // as written (may be percent-encoded)
	// DecodedUser is User percent-decoded: the dialled string, which the
	// worker's destination allow-list matches.
	DecodedUser string
	Host        string // IPv6 without brackets
	Port        int    // 0 when absent
	Params      string // the URI parameters, starting with ';' when present
}

// ParseSIPCallURI parses a call target URI: sip: or sips:, a user part, a
// valid host, an optional port and parameters. It refuses what would let the
// URI do more than name the callee: headers (?…) and maddr (which names
// another server to send to), anything over 512 bytes, and a user part that
// is more than the dialled string (user parameters, a password, or decoded
// characters outside the number/from_user charset).
func ParseSIPCallURI(raw string) (SIPCallURI, error) {
	var u SIPCallURI
	if len(raw) > sipCallMaxURIBytes {
		return u, fmt.Errorf("must be at most %d bytes", sipCallMaxURIBytes)
	}
	if strings.ContainsAny(raw, "?<>\" \t\r\n\x00") {
		return u, fmt.Errorf("must be a plain sip: or sips: URI without headers (?), spaces or quotes")
	}
	scheme, rest, ok := strings.Cut(raw, ":")
	scheme = strings.ToLower(scheme)
	if !ok || (scheme != "sip" && scheme != "sips") {
		return u, fmt.Errorf("must start with sip: or sips:")
	}
	u.Scheme = scheme

	user, hostPart, ok := strings.Cut(rest, "@")
	if !ok || user == "" {
		return u, fmt.Errorf("must have a user part (sip:user@host)")
	}
	if strings.Contains(hostPart, "@") {
		return u, fmt.Errorf("must have a single @")
	}
	// The whole userinfo is sent as written, so it must be nothing but the
	// dialled string: the allow-list cannot judge a ;phone-context or a
	// :password it never sees.
	if strings.ContainsAny(user, ";:") {
		return u, fmt.Errorf("user part must not carry ;parameters or a :password")
	}
	decoded, err := url.PathUnescape(user)
	if err != nil || !sipCallURIUserRaw.MatchString(user) || !sipCallUserPattern.MatchString(decoded) {
		return u, fmt.Errorf("user part must be 1-64 characters of digits, letters and + * # . _ ~ - (# written %%23)")
	}
	u.User = user
	u.DecodedUser = decoded

	hostPort, params, _ := strings.Cut(hostPart, ";")
	if params != "" {
		u.Params = ";" + params
		for _, p := range strings.Split(params, ";") {
			name, _, _ := strings.Cut(p, "=")
			if strings.EqualFold(strings.TrimSpace(name), "maddr") {
				return u, fmt.Errorf("must not carry a maddr parameter")
			}
		}
	}

	host, portStr := hostPort, ""
	if strings.HasPrefix(hostPort, "[") {
		end := strings.IndexByte(hostPort, ']')
		if end < 0 {
			return u, fmt.Errorf("has an unterminated IPv6 host")
		}
		host = hostPort[1:end]
		if after := hostPort[end+1:]; after != "" {
			if !strings.HasPrefix(after, ":") {
				return u, fmt.Errorf("has an invalid host")
			}
			portStr = after[1:]
		}
		if ip := net.ParseIP(host); ip == nil || ip.To4() != nil {
			return u, fmt.Errorf("has an invalid IPv6 host")
		}
	} else {
		if h, p, found := strings.Cut(hostPort, ":"); found {
			host, portStr = h, p
		}
		if !isHostOrIP(host) || strings.Contains(host, ":") {
			return u, fmt.Errorf("host must be a valid hostname or IP address")
		}
	}
	if portStr != "" {
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			return u, fmt.Errorf("port must be between 1 and 65535")
		}
		u.Port = port
	}
	u.Host = host
	return u, nil
}
