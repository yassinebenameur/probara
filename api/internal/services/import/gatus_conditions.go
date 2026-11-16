package importservice

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Gatus condition translation. A Gatus endpoint passes when every condition
// holds; each condition is translated onto the monitor's own assertions
// where Probara can express it. One that cannot be expressed is dropped with
// a note on the monitor (the import checks less than Gatus did, and says
// so), except a condition expecting failure ([CONNECTED] == false, a DNS
// error code, status conditions that exclude every code): those skip the
// endpoint, because importing it would alert backwards.

type gatusKind int

const (
	gatusKindHTTP gatusKind = iota
	gatusKindPing
	gatusKindTCP
	gatusKindTLS
	gatusKindWebSocket
	gatusKindGRPC
	gatusKindDNS
)

func (k gatusKind) label() string {
	switch k {
	case gatusKindHTTP:
		return "HTTP"
	case gatusKindPing:
		return "ping"
	case gatusKindTCP:
		return "TCP"
	case gatusKindTLS:
		return "TLS"
	case gatusKindWebSocket:
		return "WebSocket"
	case gatusKindGRPC:
		return "gRPC"
	default:
		return "DNS"
	}
}

// gatusOperators in the order Gatus tries them; each needs its spaces.
var gatusOperators = []string{" == ", " != ", " <= ", " >= ", " > ", " < "}

var gatusIndexSegment = regexp.MustCompile(`\[(\d+)\]`)
var gatusPathSegment = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type gatusCheck struct {
	kind           gatusKind
	monitor        *foreignMonitor
	status         *statusSet
	latency        int64
	bodyAssertions []map[string]interface{}
	jsonAssertions []map[string]interface{}
	certDays       int
	domainDays     int
	dnsAnswers     []string
	wsExpect       string
}

// translate folds one condition into the check. It returns a skip reason
// when the endpoint must not be imported at all.
func (c *gatusCheck) translate(raw string) string {
	condition := strings.TrimSpace(raw)
	drop := func(reason string) string {
		c.monitor.warn("the condition %q was dropped, so this monitor checks less than Gatus did: %s", condition, reason)
		return ""
	}

	if len(gatusPlaceholders(condition)) > 0 {
		return drop("it uses an environment variable that only Gatus resolves")
	}
	op, left, right, ok := splitGatusCondition(condition)
	if !ok {
		return drop("it is not a comparison the importer can read")
	}

	upper := strings.ToUpper(left)
	switch {
	case upper == "[STATUS]":
		return c.statusCondition(op, right, drop)
	case upper == "[RESPONSE_TIME]":
		return c.latencyCondition(op, right, drop)
	case upper == "[CONNECTED]":
		value := strings.ToLower(right)
		if (op == "==" && value == "true") || (op == "!=" && value == "false") {
			// Every Probara check already fails when it cannot connect.
			return ""
		}
		if (op == "==" && value == "false") || (op == "!=" && value == "true") {
			return "it expects the connection to fail ([CONNECTED] == false), which Probara cannot express - importing it would alert backwards"
		}
		return drop("it is not a comparison the importer can read")
	case upper == "[CERTIFICATE_EXPIRATION]":
		return c.certificateCondition(op, right, drop)
	case upper == "[DOMAIN_EXPIRATION]":
		days, ok := gatusLowerBoundDays(op, right)
		if !ok {
			return drop("only a lower bound on the time left (> or >=) can be expressed")
		}
		if days > c.domainDays {
			c.domainDays = days
		}
		return ""
	case upper == "[DNS_RCODE]":
		if c.kind != gatusKindDNS {
			return drop("only DNS checks have an answer code")
		}
		if op == "==" && strings.EqualFold(right, "NOERROR") {
			// Probara's DNS check fails unless the query succeeds.
			return ""
		}
		return "it expects a DNS answer code other than NOERROR, which Probara cannot express - its DNS check fails unless the query succeeds"
	case upper == "[BODY]":
		return c.bodyCondition(op, right, drop)
	case strings.HasPrefix(upper, "[BODY]"):
		return c.jsonCondition(left, op, right, drop)
	case strings.HasPrefix(upper, "LEN(") && strings.HasSuffix(left, ")"):
		return c.lengthCondition(left[4:len(left)-1], op, right, drop)
	case strings.HasPrefix(upper, "HAS(") && strings.HasSuffix(left, ")"):
		return c.hasCondition(left[4:len(left)-1], op, right, drop)
	case upper == "[IP]":
		return drop("the resolved IP address cannot be asserted")
	}
	return drop("Probara has no equivalent for " + left)
}

// splitGatusCondition splits "left op right", putting the placeholder on
// the left when the condition was written the other way round.
func splitGatusCondition(condition string) (string, string, string, bool) {
	for _, candidate := range gatusOperators {
		parts := strings.Split(condition, candidate)
		if len(parts) == 1 {
			continue
		}
		if len(parts) != 2 {
			return "", "", "", false
		}
		op := strings.TrimSpace(candidate)
		left, right := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if !isGatusOperand(left) && isGatusOperand(right) {
			left, right = right, left
			op = map[string]string{"==": "==", "!=": "!=", "<": ">", ">": "<", "<=": ">=", ">=": "<="}[op]
		}
		return op, left, right, left != "" && right != ""
	}
	return "", "", "", false
}

func isGatusOperand(value string) bool {
	upper := strings.ToUpper(value)
	return strings.HasPrefix(upper, "[") || strings.HasPrefix(upper, "LEN(") || strings.HasPrefix(upper, "HAS(")
}

// gatusValue splits a right-hand side into its function (pat, any, or ""
// for a literal) and its arguments.
func gatusValue(right string) (string, []string) {
	lower := strings.ToLower(right)
	for _, fn := range []string{"pat", "any"} {
		if strings.HasPrefix(lower, fn+"(") && strings.HasSuffix(right, ")") {
			inner := right[len(fn)+1 : len(right)-1]
			if fn == "pat" {
				return fn, []string{inner}
			}
			args := strings.Split(inner, ",")
			for i := range args {
				args[i] = strings.TrimSpace(args[i])
			}
			return fn, args
		}
	}
	return "", []string{right}
}

func (c *gatusCheck) statusCondition(op, right string, drop func(string) string) string {
	if c.kind != gatusKindHTTP {
		return drop("only HTTP checks have a status code")
	}
	fn, args := gatusValue(right)

	var matched statusSet
	switch fn {
	case "pat":
		if op != "==" && op != "!=" {
			return drop("a pattern can only be compared with == or !=")
		}
		re := regexp.MustCompile(globToRegex(args[0]))
		for code := 100; code <= 599; code++ {
			matched.set(code, re.MatchString(strconv.Itoa(code)))
		}
	default:
		if fn == "any" && op != "==" && op != "!=" {
			return drop("any() can only be compared with == or !=")
		}
		for _, arg := range args {
			code, err := strconv.Atoi(arg)
			if err != nil {
				return drop("the status code is not a number")
			}
			matched.set(code, true)
		}
	}

	if c.status == nil {
		full := fullStatusSet()
		c.status = &full
	}
	bound := 0
	if fn == "" {
		bound, _ = strconv.Atoi(args[0])
	}
	for code := 100; code <= 599; code++ {
		keep := c.status.has(code)
		switch op {
		case "==":
			keep = keep && matched.has(code)
		case "!=":
			keep = keep && !matched.has(code)
		case "<":
			keep = keep && code < bound
		case "<=":
			keep = keep && code <= bound
		case ">":
			keep = keep && code > bound
		case ">=":
			keep = keep && code >= bound
		}
		c.status.set(code, keep)
	}
	if c.status.empty() {
		return "its status code conditions exclude every status code, so it could never pass"
	}
	return ""
}

func (c *gatusCheck) latencyCondition(op, right string, drop func(string) string) string {
	if c.kind != gatusKindHTTP && c.kind != gatusKindPing && c.kind != gatusKindWebSocket {
		return drop("Probara's " + c.kind.label() + " monitor has no response-time threshold")
	}
	bound, err := strconv.ParseInt(right, 10, 64)
	if err != nil {
		return drop("the response time is not a number of milliseconds")
	}
	var limit int64
	switch op {
	case "<":
		limit = bound - 1
	case "<=":
		limit = bound
	default:
		return drop("only an upper bound on the response time (< or <=) can be expressed")
	}
	if limit <= 0 {
		return drop("the response time bound is not positive")
	}
	if c.latency == 0 || limit < c.latency {
		c.latency = limit
	}
	return ""
}

// gatusLowerBoundDays reads "> 720h" or ">= 30d" as whole days, rounding up.
func gatusLowerBoundDays(op, right string) (int, bool) {
	if op != ">" && op != ">=" {
		return 0, false
	}
	d, ok := parseGatusDuration(right)
	if !ok {
		return 0, false
	}
	return int(math.Ceil(d.Hours() / 24)), true
}

func (c *gatusCheck) certificateCondition(op, right string, drop func(string) string) string {
	days, ok := gatusLowerBoundDays(op, right)
	if !ok {
		return drop("only a lower bound on the time left (> or >=) can be expressed")
	}
	switch c.kind {
	case gatusKindHTTP:
		if !isHTTPS(c.monitor.config["url"].(string)) {
			return drop("the endpoint is a plain http:// URL")
		}
	case gatusKindTLS:
	default:
		return drop("Probara's " + c.kind.label() + " monitor does not check certificate expiry")
	}
	if days > c.certDays {
		c.certDays = days
	}
	return ""
}

func (c *gatusCheck) bodyCondition(op, right string, drop func(string) string) string {
	fn, args := gatusValue(right)
	switch c.kind {
	case gatusKindHTTP:
		var assertion map[string]interface{}
		switch {
		case op != "==" && op != "!=":
			return drop("the whole body can only be compared with == or !=")
		case fn == "pat":
			if inner, ok := globSubstring(args[0]); ok {
				assertion = map[string]interface{}{"op": "contains", "value": inner}
			} else {
				assertion = map[string]interface{}{"op": "regex", "value": globToRegex(args[0])}
			}
		case fn == "any":
			assertion = map[string]interface{}{"op": "regex", "value": anyToRegex(args)}
		default:
			assertion = map[string]interface{}{"op": "regex", "value": "(?s)^" + regexp.QuoteMeta(args[0]) + "$"}
		}
		if op == "!=" {
			assertion["op"] = "not_" + assertion["op"].(string)
		}
		c.bodyAssertions = append(c.bodyAssertions, assertion)
		return ""

	case gatusKindWebSocket:
		if op != "==" || fn == "any" {
			return drop("a WebSocket monitor can only require that the first message contains a value")
		}
		if fn == "pat" {
			inner, ok := globSubstring(args[0])
			if !ok {
				return drop("a WebSocket monitor can only require that the first message contains a value")
			}
			c.wsExpect = inner
			return ""
		}
		c.wsExpect = args[0]
		c.monitor.warn("the condition [BODY] == %q became \"the first message contains %q\", which also passes on a longer message", args[0], args[0])
		return ""

	case gatusKindDNS:
		if op != "==" || fn != "" {
			return drop("a DNS monitor can only require that a given answer is returned")
		}
		c.dnsAnswers = append(c.dnsAnswers, args[0])
		return ""
	}
	return drop("Probara's " + c.kind.label() + " monitor does not read a response body")
}

// anyToRegex matches a whole body equal to one of the values.
func anyToRegex(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = regexp.QuoteMeta(value)
	}
	return "(?s)^(?:" + strings.Join(quoted, "|") + ")$"
}

// gatusJSONPath turns "[BODY].data[0].name" into the gjson path
// "data.0.name". The bool is false for the whole body or a path using
// syntax beyond keys and indexes.
func gatusJSONPath(placeholder string) (string, bool) {
	if len(placeholder) < len("[BODY]") || !strings.EqualFold(placeholder[:6], "[BODY]") {
		return "", false
	}
	rest := gatusIndexSegment.ReplaceAllString(placeholder[6:], ".$1")
	rest = strings.TrimPrefix(rest, ".")
	if rest == "" {
		return "", false
	}
	for _, segment := range strings.Split(rest, ".") {
		if !gatusPathSegment.MatchString(segment) {
			return "", false
		}
	}
	return rest, true
}

func (c *gatusCheck) jsonCondition(left, op, right string, drop func(string) string) string {
	if c.kind == gatusKindGRPC {
		if strings.EqualFold(left, "[BODY].status") && op == "==" && strings.EqualFold(right, "SERVING") {
			// Probara's gRPC monitor is a grpc.health.v1 probe: SERVING is
			// exactly what it requires.
			return ""
		}
		return drop("Probara's gRPC monitor only checks the health status")
	}
	if c.kind != gatusKindHTTP {
		return drop("Probara's " + c.kind.label() + " monitor does not read a JSON response")
	}
	path, ok := gatusJSONPath(left)
	if !ok {
		return drop("the JSON path uses syntax beyond keys and array indexes")
	}

	fn, args := gatusValue(right)
	assertion := map[string]interface{}{"path": path}
	switch {
	case fn == "pat" && op == "==":
		assertion["op"], assertion["value"] = "regex", globToRegex(args[0])
	case fn == "any" && op == "==":
		assertion["op"], assertion["value"] = "regex", anyToRegex(args)
	case fn != "":
		return drop("a pattern or any() on a JSON value can only be compared with ==")
	case op == "==" || op == "!=":
		value := args[0]
		lower := strings.ToLower(value)
		if op == "==" && (lower == "true" || lower == "false") {
			assertion["op"], assertion["value"] = "bool_is", lower
		} else if op == "==" {
			assertion["op"], assertion["value"] = "equals", value
		} else {
			assertion["op"], assertion["value"] = "not_equals", value
		}
	default:
		if _, err := strconv.ParseFloat(args[0], 64); err != nil {
			return drop("an ordering comparison needs a number")
		}
		assertion["op"], assertion["value"] = numberOp(op), args[0]
	}
	c.jsonAssertions = append(c.jsonAssertions, assertion)
	return ""
}

func numberOp(op string) string {
	switch op {
	case "<":
		return "number_lt"
	case "<=":
		return "number_lte"
	case ">":
		return "number_gt"
	default:
		return "number_gte"
	}
}

// lengthCondition: len([BODY].items) becomes gjson's array count, items.#.
func (c *gatusCheck) lengthCondition(inner, op, right string, drop func(string) string) string {
	if c.kind != gatusKindHTTP {
		return drop("Probara's " + c.kind.label() + " monitor does not read a JSON response")
	}
	path, ok := gatusJSONPath(strings.TrimSpace(inner))
	if !ok {
		return drop("only the length of a JSON array field can be asserted")
	}
	if _, err := strconv.Atoi(right); err != nil {
		return drop("the length is not a number")
	}
	assertion := map[string]interface{}{"path": path + ".#", "value": right}
	switch op {
	case "==":
		assertion["op"] = "equals"
	case "!=":
		assertion["op"] = "not_equals"
	default:
		assertion["op"] = numberOp(op)
	}
	c.jsonAssertions = append(c.jsonAssertions, assertion)
	c.monitor.warn("the condition on len(%s) now counts the items of a JSON array; on a string field it would not match", inner)
	return ""
}

// hasCondition: has([BODY].x) == true becomes an exists assertion; the
// absence of a field cannot be asserted.
func (c *gatusCheck) hasCondition(inner, op, right string, drop func(string) string) string {
	if c.kind != gatusKindHTTP {
		return drop("Probara's " + c.kind.label() + " monitor does not read a JSON response")
	}
	path, ok := gatusJSONPath(strings.TrimSpace(inner))
	if !ok {
		return drop("the JSON path uses syntax beyond keys and array indexes")
	}
	value := strings.ToLower(right)
	if (op == "==" && value == "true") || (op == "!=" && value == "false") {
		c.jsonAssertions = append(c.jsonAssertions, map[string]interface{}{"path": path, "op": "exists"})
		return ""
	}
	return drop("the absence of a JSON field cannot be asserted")
}

// apply writes the translated conditions onto the monitor's config.
func (c *gatusCheck) apply() string {
	config := c.monitor.config
	switch c.kind {
	case gatusKindHTTP:
		if c.status == nil {
			// Gatus only checks the status code when a condition says so.
			config["expected_status_ranges"] = []map[string]int{{"min": 100, "max": 599}}
			c.monitor.warn("Gatus checked no status code, so every status is accepted here too - set an expected status if the endpoint should answer 2xx")
		} else if !c.status.isDefault2xx() {
			c.status.apply(config)
		}
		if c.latency > 0 {
			config["max_latency_ms"] = c.latency
		}
		if len(c.bodyAssertions) > 0 {
			config["body_assertions"] = c.bodyAssertions
		}
		if len(c.jsonAssertions) > 0 {
			config["json_assertions"] = c.jsonAssertions
		}
		if c.certDays > 0 {
			config["tls_min_days_valid"] = c.certDays
		}
	case gatusKindPing:
		if c.latency > 0 {
			config["max_rtt_ms"] = c.latency
		}
	case gatusKindWebSocket:
		if c.latency > 0 {
			config["max_latency_ms"] = c.latency
		}
		if c.wsExpect != "" {
			config["expected_substring"] = c.wsExpect
		}
	case gatusKindTLS:
		if c.certDays > 0 {
			config["tls_min_days_valid"] = c.certDays
		}
	case gatusKindDNS:
		if len(c.dnsAnswers) > 0 {
			config["expected_answers"] = c.dnsAnswers
			c.monitor.warn("the expected DNS answer must be among the records returned; Gatus compared it with the whole answer")
		}
	}
	if c.certDays > 0 {
		c.monitor.warn("Gatus failed the check once the certificate had less time left than the condition allowed; Probara raises a TLS expiry alert at %d days while the check keeps passing", c.certDays)
	}
	return ""
}
