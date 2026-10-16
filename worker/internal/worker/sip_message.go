package worker

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// sipMaxBodyBytes caps a message body and sipMaxHeadBytes the start line plus
// headers. A datagram cannot exceed 64 KB anyway; on a stream the caps keep a
// hostile or broken peer from making the worker buffer without bound.
const (
	sipMaxBodyBytes = 64 << 10
	sipMaxHeadBytes = 64 << 10
)

// errSIPMalformed marks a message the parser refused. On a datagram the
// message is dropped (RFC 3261 §18.3); on a stream the framing is lost and the
// flow fails.
var errSIPMalformed = errors.New("malformed SIP message")

// sipHeader is one header field. Name is the canonical spelling (compact
// forms expanded); multi-value headers that carry comma-joined lists (Via,
// Route, Record-Route, Contact) are split into one sipHeader per value at
// parse time, so the first "Via" entry is always the top Via.
type sipHeader struct {
	Name  string
	Value string
}

// sipMessage is a parsed SIP request or response.
type sipMessage struct {
	IsRequest  bool
	Method     string // requests
	RequestURI string // requests
	StatusCode int    // responses
	Reason     string // responses
	Headers    []sipHeader
	Body       []byte
}

// sipCompactForms maps the RFC 3261 §7.3.3 (and later RFCs') single-letter
// header names to their full names.
var sipCompactForms = map[string]string{
	"a": "accept-contact",
	"b": "referred-by",
	"c": "content-type",
	"d": "request-disposition",
	"e": "content-encoding",
	"f": "from",
	"i": "call-id",
	"j": "reject-contact",
	"k": "supported",
	"l": "content-length",
	"m": "contact",
	"o": "event",
	"r": "refer-to",
	"s": "subject",
	"t": "to",
	"u": "allow-events",
	"v": "via",
	"x": "session-expires",
	"y": "identity",
}

// sipCanonicalNames is the spelling used when a header is written back out.
// Names not listed keep the spelling they arrived with.
var sipCanonicalNames = map[string]string{
	"accept":              "Accept",
	"accept-contact":      "Accept-Contact",
	"allow":               "Allow",
	"allow-events":        "Allow-Events",
	"authorization":       "Authorization",
	"call-id":             "Call-ID",
	"contact":             "Contact",
	"content-encoding":    "Content-Encoding",
	"content-length":      "Content-Length",
	"content-type":        "Content-Type",
	"cseq":                "CSeq",
	"event":               "Event",
	"expires":             "Expires",
	"from":                "From",
	"identity":            "Identity",
	"max-forwards":        "Max-Forwards",
	"proxy-authenticate":  "Proxy-Authenticate",
	"proxy-authorization": "Proxy-Authorization",
	"reason":              "Reason",
	"record-route":        "Record-Route",
	"refer-to":            "Refer-To",
	"referred-by":         "Referred-By",
	"reject-contact":      "Reject-Contact",
	"request-disposition": "Request-Disposition",
	"require":             "Require",
	"route":               "Route",
	"server":              "Server",
	"session-expires":     "Session-Expires",
	"subject":             "Subject",
	"supported":           "Supported",
	"to":                  "To",
	"unsupported":         "Unsupported",
	"user-agent":          "User-Agent",
	"via":                 "Via",
	"warning":             "Warning",
	"www-authenticate":    "WWW-Authenticate",
}

// sipListHeaders are split on top-level commas (RFC 3261 §7.3.1): each entry
// of these lists is addressed individually (top Via, route set, Contact).
var sipListHeaders = map[string]bool{
	"via":          true,
	"route":        true,
	"record-route": true,
	"contact":      true,
}

// sipHeaderKey is the lookup key for a header name: lowercase, compact form
// expanded.
func sipHeaderKey(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	if full, ok := sipCompactForms[key]; ok {
		return full
	}
	return key
}

func sipCanonicalName(name string) string {
	key := sipHeaderKey(name)
	if canonical, ok := sipCanonicalNames[key]; ok {
		return canonical
	}
	if _, compact := sipCompactForms[strings.ToLower(strings.TrimSpace(name))]; compact {
		return key
	}
	return strings.TrimSpace(name)
}

// Get returns the first value of a header, or "".
func (m *sipMessage) Get(name string) string {
	key := sipHeaderKey(name)
	for _, h := range m.Headers {
		if sipHeaderKey(h.Name) == key {
			return h.Value
		}
	}
	return ""
}

// Values returns every value of a header in message order.
func (m *sipMessage) Values(name string) []string {
	key := sipHeaderKey(name)
	var values []string
	for _, h := range m.Headers {
		if sipHeaderKey(h.Name) == key {
			values = append(values, h.Value)
		}
	}
	return values
}

// Add appends a header.
func (m *sipMessage) Add(name, value string) {
	m.Headers = append(m.Headers, sipHeader{Name: sipCanonicalName(name), Value: value})
}

// topViaBranch is the branch parameter of the top Via: the transaction key
// (RFC 3261 §17.1.3).
func (m *sipMessage) topViaBranch() string {
	vias := m.Values("Via")
	if len(vias) == 0 {
		return ""
	}
	_, params, _ := strings.Cut(vias[0], ";")
	branch, _ := sipParam(params, "branch")
	return branch
}

// cseq parses the CSeq header ("<number> <method>").
func (m *sipMessage) cseq() (int, string, error) {
	fields := strings.Fields(m.Get("CSeq"))
	if len(fields) != 2 {
		return 0, "", fmt.Errorf("invalid CSeq %q", m.Get("CSeq"))
	}
	n, err := strconv.ParseUint(fields[0], 10, 31)
	if err != nil {
		return 0, "", fmt.Errorf("invalid CSeq number %q", fields[0])
	}
	return int(n), strings.ToUpper(fields[1]), nil
}

// Bytes serializes the message. Content-Length is always computed from the
// body (any parsed Content-Length header is ignored), and is the last header.
func (m *sipMessage) Bytes() []byte {
	var b bytes.Buffer
	if m.IsRequest {
		fmt.Fprintf(&b, "%s %s SIP/2.0\r\n", m.Method, m.RequestURI)
	} else {
		fmt.Fprintf(&b, "SIP/2.0 %d %s\r\n", m.StatusCode, m.Reason)
	}
	for _, h := range m.Headers {
		if sipHeaderKey(h.Name) == "content-length" {
			continue
		}
		b.WriteString(h.Name)
		b.WriteString(": ")
		b.WriteString(h.Value)
		b.WriteString("\r\n")
	}
	fmt.Fprintf(&b, "Content-Length: %d\r\n\r\n", len(m.Body))
	b.Write(m.Body)
	return b.Bytes()
}

// parseSIPMessage parses one datagram. Leading CRLFs are skipped (RFC 3261
// §7.5). With a Content-Length, bytes past it are discarded and a body
// shorter than it is an error (§18.3); without one the body is the rest of
// the datagram.
func parseSIPMessage(data []byte) (*sipMessage, error) {
	data = bytes.TrimLeft(data, "\r\n")
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty message", errSIPMalformed)
	}
	// The head ends at the first blank line; bare-LF framing is tolerated.
	head, body := data, []byte(nil)
	if i := bytes.Index(data, []byte("\r\n\r\n")); i >= 0 {
		head, body = data[:i], data[i+4:]
	}
	if i := bytes.Index(data, []byte("\n\n")); i >= 0 && i < len(head) {
		head, body = data[:i], data[i+2:]
	}
	if len(head) > sipMaxHeadBytes {
		return nil, fmt.Errorf("%w: header block exceeds %d bytes", errSIPMalformed, sipMaxHeadBytes)
	}
	msg, err := parseSIPHead(string(head))
	if err != nil {
		return nil, err
	}
	length, present, err := sipMessageContentLength(msg)
	if err != nil {
		return nil, err
	}
	if present {
		if length > len(body) {
			return nil, fmt.Errorf("%w: Content-Length %d exceeds the %d-byte body", errSIPMalformed, length, len(body))
		}
		body = body[:length]
	}
	if len(body) > sipMaxBodyBytes {
		return nil, fmt.Errorf("%w: body exceeds %d bytes", errSIPMalformed, sipMaxBodyBytes)
	}
	if len(body) > 0 {
		msg.Body = append([]byte(nil), body...)
	}
	return msg, nil
}

// readSIPStreamMessage reads one message from a stream transport. Framing
// rests on Content-Length (mandatory on streams, RFC 3261 §18.3; absent reads
// as 0). Blank lines before the start line are keepalives and are skipped.
// The reader must be at least sipMaxHeadBytes large so one header line fits
// in its buffer; a longer line is an error rather than an unbounded read.
func readSIPStreamMessage(r *bufio.Reader) (*sipMessage, error) {
	var head strings.Builder
	started := false
	for {
		line, err := r.ReadSlice('\n')
		if err != nil {
			if errors.Is(err, bufio.ErrBufferFull) {
				return nil, fmt.Errorf("%w: header line too long", errSIPMalformed)
			}
			if err == io.EOF && (started || len(line) > 0) {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		trimmed := bytes.TrimRight(line, "\r\n")
		if !started {
			if len(trimmed) == 0 {
				continue // keepalive CRLF
			}
			started = true
		} else if len(trimmed) == 0 {
			break
		}
		if head.Len()+len(line) > sipMaxHeadBytes {
			return nil, fmt.Errorf("%w: header block exceeds %d bytes", errSIPMalformed, sipMaxHeadBytes)
		}
		head.Write(line)
	}

	msg, err := parseSIPHead(head.String())
	if err != nil {
		return nil, err
	}
	length, _, err := sipMessageContentLength(msg)
	if err != nil {
		return nil, err
	}
	if length > sipMaxBodyBytes {
		return nil, fmt.Errorf("%w: body exceeds %d bytes", errSIPMalformed, sipMaxBodyBytes)
	}
	if length > 0 {
		msg.Body = make([]byte, length)
		if _, err := io.ReadFull(r, msg.Body); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, fmt.Errorf("failed to read message body: %w", err)
		}
	}
	return msg, nil
}

// sipMessageContentLength returns the Content-Length, whether one was
// present, and an error for a malformed or conflicting value.
func sipMessageContentLength(m *sipMessage) (int, bool, error) {
	values := m.Values("Content-Length")
	if len(values) == 0 {
		return 0, false, nil
	}
	length := -1
	for _, v := range values {
		n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 31)
		if err != nil {
			return 0, true, fmt.Errorf("%w: invalid Content-Length %q", errSIPMalformed, v)
		}
		if length >= 0 && int(n) != length {
			return 0, true, fmt.Errorf("%w: conflicting Content-Length headers", errSIPMalformed)
		}
		length = int(n)
	}
	return length, true, nil
}

// parseSIPHead parses the start line and header fields, unfolding
// continuation lines and splitting list headers.
func parseSIPHead(head string) (*sipMessage, error) {
	if strings.IndexByte(head, 0) >= 0 {
		return nil, fmt.Errorf("%w: NUL byte in header block", errSIPMalformed)
	}
	normalized := strings.ReplaceAll(head, "\r\n", "\n")
	if strings.IndexByte(normalized, '\r') >= 0 {
		return nil, fmt.Errorf("%w: bare CR in header block", errSIPMalformed)
	}
	lines := strings.Split(normalized, "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("%w: empty message", errSIPMalformed)
	}

	msg := &sipMessage{}
	if err := parseSIPStartLine(lines[0], msg); err != nil {
		return nil, err
	}

	// Unfold: a line starting with SP or HT continues the previous header
	// (RFC 3261 §7.3.1); the fold collapses to a single space.
	var fields []string
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if len(fields) == 0 {
				return nil, fmt.Errorf("%w: continuation line before any header", errSIPMalformed)
			}
			fields[len(fields)-1] += " " + strings.TrimSpace(line)
			continue
		}
		fields = append(fields, line)
	}

	for _, field := range fields {
		name, value, found := strings.Cut(field, ":")
		if !found {
			return nil, fmt.Errorf("%w: header line without a colon: %q", errSIPMalformed, sipTruncate(field))
		}
		// HCOLON allows whitespace before the colon.
		name = strings.TrimRight(name, " \t")
		if !isSIPToken(name) {
			return nil, fmt.Errorf("%w: invalid header name %q", errSIPMalformed, sipTruncate(name))
		}
		value = strings.TrimSpace(value)
		canonical := sipCanonicalName(name)
		if sipListHeaders[sipHeaderKey(name)] {
			for _, part := range splitSIPList(value) {
				msg.Headers = append(msg.Headers, sipHeader{Name: canonical, Value: part})
			}
			continue
		}
		msg.Headers = append(msg.Headers, sipHeader{Name: canonical, Value: value})
	}
	return msg, nil
}

// parseSIPStartLine parses a Status-Line ("SIP/2.0 200 OK") or a
// Request-Line ("OPTIONS sip:x SIP/2.0"). Elements are separated by exactly
// one SP (RFC 3261 §7.1), so extra or trailing whitespace is an error, as is
// any version other than SIP/2.0.
func parseSIPStartLine(line string, msg *sipMessage) error {
	if len(line) >= 4 && strings.EqualFold(line[:4], "SIP/") {
		parts := strings.SplitN(line, " ", 3)
		if len(parts) < 2 {
			return fmt.Errorf("%w: invalid status line %q", errSIPMalformed, sipTruncate(line))
		}
		if !strings.EqualFold(parts[0], "SIP/2.0") {
			return fmt.Errorf("%w: unsupported SIP version %q", errSIPMalformed, sipTruncate(parts[0]))
		}
		if len(parts[1]) != 3 {
			return fmt.Errorf("%w: invalid status code %q", errSIPMalformed, sipTruncate(parts[1]))
		}
		code, err := strconv.Atoi(parts[1])
		if err != nil || code < 100 || code > 699 {
			return fmt.Errorf("%w: invalid status code %q", errSIPMalformed, sipTruncate(parts[1]))
		}
		msg.StatusCode = code
		if len(parts) == 3 {
			msg.Reason = strings.TrimSpace(parts[2])
		}
		return nil
	}

	parts := strings.Split(line, " ")
	if len(parts) != 3 {
		return fmt.Errorf("%w: invalid request line %q", errSIPMalformed, sipTruncate(line))
	}
	method, uri, version := parts[0], parts[1], parts[2]
	if !isSIPToken(method) {
		return fmt.Errorf("%w: invalid method %q", errSIPMalformed, sipTruncate(method))
	}
	if uri == "" || !strings.Contains(uri, ":") || strings.ContainsAny(uri, "<>\t\"") {
		return fmt.Errorf("%w: invalid Request-URI %q", errSIPMalformed, sipTruncate(uri))
	}
	if !strings.EqualFold(version, "SIP/2.0") {
		return fmt.Errorf("%w: unsupported SIP version %q", errSIPMalformed, sipTruncate(version))
	}
	msg.IsRequest = true
	msg.Method = method
	msg.RequestURI = uri
	return nil
}

// isSIPToken reports whether s is a non-empty RFC 3261 token.
func isSIPToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("-.!%*_+`'~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// splitSIPList splits a header value on commas that are outside quoted
// strings and outside <…> URIs. Empty elements are dropped.
func splitSIPList(value string) []string {
	var (
		parts   []string
		start   int
		inQuote bool
		inAngle bool
	)
	for i := 0; i < len(value); i++ {
		switch c := value[i]; {
		case inQuote && c == '\\':
			i++ // quoted-pair
		case c == '"':
			inQuote = !inQuote
		case !inQuote && c == '<':
			inAngle = true
		case !inQuote && c == '>':
			inAngle = false
		case !inQuote && !inAngle && c == ',':
			if part := strings.TrimSpace(value[start:i]); part != "" {
				parts = append(parts, part)
			}
			start = i + 1
		}
	}
	if part := strings.TrimSpace(value[start:]); part != "" {
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return []string{""}
	}
	return parts
}

// sipNameAddr is a parsed From/To/Contact/Route value: an optional display
// name, the URI, and the header parameters after it (raw, starting with ';'
// when present).
type sipNameAddr struct {
	Display string
	URI     string
	Params  string
}

// parseSIPNameAddr parses `"Display" <uri>;params`, `Display <uri>;params`
// or a bare addr-spec `uri;params` (whose parameters belong to the header,
// RFC 3261 §20.10).
func parseSIPNameAddr(value string) (sipNameAddr, error) {
	v := strings.TrimSpace(value)
	var na sipNameAddr

	// Find a '<' outside a quoted display name.
	angle := -1
	inQuote := false
	for i := 0; i < len(v) && angle < 0; i++ {
		switch c := v[i]; {
		case inQuote && c == '\\':
			i++
		case c == '"':
			inQuote = !inQuote
		case !inQuote && c == '<':
			angle = i
		}
	}
	if inQuote && angle < 0 {
		return na, fmt.Errorf("unbalanced quotes in %q", sipTruncate(v))
	}

	if angle < 0 {
		if strings.ContainsAny(v, `">`) {
			return na, fmt.Errorf("invalid name-addr %q", sipTruncate(v))
		}
		uri, params, _ := strings.Cut(v, ";")
		na.URI = strings.TrimSpace(uri)
		if params != "" {
			na.Params = ";" + params
		}
		if na.URI == "" {
			return na, fmt.Errorf("empty URI in %q", sipTruncate(v))
		}
		return na, nil
	}

	display := strings.TrimSpace(v[:angle])
	if strings.HasPrefix(display, `"`) {
		unquoted, err := sipUnquote(display)
		if err != nil {
			return na, err
		}
		display = unquoted
	}
	na.Display = display

	end := strings.IndexByte(v[angle:], '>')
	if end < 0 {
		return na, fmt.Errorf("unterminated <URI> in %q", sipTruncate(v))
	}
	na.URI = strings.TrimSpace(v[angle+1 : angle+end])
	if na.URI == "" {
		return na, fmt.Errorf("empty URI in %q", sipTruncate(v))
	}
	na.Params = strings.TrimSpace(v[angle+end+1:])
	if na.Params != "" && !strings.HasPrefix(na.Params, ";") {
		return na, fmt.Errorf("unexpected text after URI in %q", sipTruncate(v))
	}
	return na, nil
}

func sipUnquote(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", fmt.Errorf("invalid quoted string %q", sipTruncate(s))
	}
	var b strings.Builder
	inner := s[1 : len(s)-1]
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if c == '\\' {
			if i+1 >= len(inner) {
				return "", fmt.Errorf("invalid quoted string %q", sipTruncate(s))
			}
			i++
			c = inner[i]
		} else if c == '"' {
			return "", fmt.Errorf("invalid quoted string %q", sipTruncate(s))
		}
		b.WriteByte(c)
	}
	return b.String(), nil
}

// sipParam looks up a ;-separated parameter (case-insensitive name) in params
// such as ";tag=abc;lr". A parameter without a value is found with "".
func sipParam(params, name string) (string, bool) {
	for _, p := range splitSIPParams(params) {
		k, v, _ := strings.Cut(p, "=")
		if strings.EqualFold(strings.TrimSpace(k), name) {
			v = strings.TrimSpace(v)
			if unquoted, err := sipUnquote(v); err == nil {
				v = unquoted
			}
			return v, true
		}
	}
	return "", false
}

// splitSIPParams splits on ';' outside quoted strings.
func splitSIPParams(params string) []string {
	var (
		parts   []string
		start   int
		inQuote bool
	)
	for i := 0; i < len(params); i++ {
		switch c := params[i]; {
		case inQuote && c == '\\':
			i++
		case c == '"':
			inQuote = !inQuote
		case !inQuote && c == ';':
			if p := strings.TrimSpace(params[start:i]); p != "" {
				parts = append(parts, p)
			}
			start = i + 1
		}
	}
	if start < len(params) {
		if p := strings.TrimSpace(params[start:]); p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

// sipHeaderTag returns the tag parameter of a From/To value, or "".
func sipHeaderTag(value string) string {
	na, err := parseSIPNameAddr(value)
	if err != nil {
		return ""
	}
	tag, _ := sipParam(na.Params, "tag")
	return tag
}

func sipTruncate(s string) string {
	const limit = 80
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}

// sipRandomToken returns n random bytes, hex-encoded, for branches, tags and
// Call-IDs.
func sipRandomToken(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand does not fail on supported platforms (Go 1.24+ it
		// panics internally instead); keep a non-empty value regardless.
		return strings.Repeat("0", 2*n)
	}
	return hex.EncodeToString(buf)
}

// newSIPBranch returns an RFC 3261 branch ID (magic cookie + random).
func newSIPBranch() string {
	return "z9hG4bK" + sipRandomToken(8)
}

// sipRequestParams is everything the caller decides about one request.
// buildSIPRequest adds only what every request carries: Max-Forwards: 70,
// User-Agent: Probara and a computed Content-Length.
type sipRequestParams struct {
	Method      string // uppercase
	RequestURI  string
	Via         string // full top Via value, from sipFlow.viaValue(branch)
	From        string // name-addr without tag
	FromTag     string
	To          string // name-addr without tag
	ToTag       string // "" outside a dialog
	CallID      string
	CSeq        int
	Route       []string // route set, in order
	Contact     string   // full value; "" sends none
	Extra       []sipHeader
	Body        []byte
	ContentType string // required when Body is set
}

// buildSIPRequest assembles a request message.
func buildSIPRequest(p sipRequestParams) *sipMessage {
	msg := &sipMessage{IsRequest: true, Method: p.Method, RequestURI: p.RequestURI}
	msg.Add("Via", p.Via)
	msg.Add("Max-Forwards", "70")
	for _, route := range p.Route {
		msg.Add("Route", route)
	}
	from := p.From
	if p.FromTag != "" {
		from += ";tag=" + p.FromTag
	}
	msg.Add("From", from)
	to := p.To
	if p.ToTag != "" {
		to += ";tag=" + p.ToTag
	}
	msg.Add("To", to)
	msg.Add("Call-ID", p.CallID)
	msg.Add("CSeq", fmt.Sprintf("%d %s", p.CSeq, p.Method))
	if p.Contact != "" {
		msg.Add("Contact", p.Contact)
	}
	for _, h := range p.Extra {
		msg.Add(h.Name, h.Value)
	}
	msg.Add("User-Agent", "Probara")
	if len(p.Body) > 0 {
		msg.Add("Content-Type", p.ContentType)
		msg.Body = p.Body
	}
	return msg
}
