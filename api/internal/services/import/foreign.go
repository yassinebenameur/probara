package importservice

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/yassinebenameur/probara/api/internal/models"
	"github.com/yassinebenameur/probara/api/internal/validation"
	"github.com/yassinebenameur/probara/shared/domainexpiry"
	sharedmodels "github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/monitorstate"
	"github.com/yassinebenameur/probara/shared/secrets"
)

// Shared translation scaffolding for the Better Stack, UptimeRobot and Gatus
// adapters. Each adapter turns its records into foreignMonitors and calls
// finish, which does what every source needs the same way: unique names,
// group rows built from membership, the registry check on every translated
// config, and the summary warnings above the preview.
//
// The registry check is what makes "every emitted row is importable" hold at
// runtime, not only in tests: a translation the API would reject is reported
// in SkippedRows with the validator's reason instead of failing on import.

const (
	// Probara's interval bounds (validation's minIntervalSeconds and
	// maxIntervalSeconds); the Kuma adapter names the same pair.
	minImportInterval = minKumaInterval
	maxImportInterval = maxKumaInterval

	// importDefaultTimeout is what ExecuteImport gives an active check that
	// carries no timeout.
	importDefaultTimeout = 30

	// foreignDomainInterval: a domain monitor created from a source's
	// domain-expiry check runs daily, the domain type's default, since a
	// registration date moves in days and registries rate-limit lookups.
	foreignDomainInterval = 86400
)

// foreignPasswordMissingWarning names a basic-auth username imported without
// its password; such a monitor is created disabled, as the Kuma importer does.
const foreignPasswordMissingWarning = "the basic auth password was not in the export, so this monitor was created disabled - re-enter the basic auth password and enable it"

// foreignMonitor is one translated record, held until names are made unique
// and groups are assembled.
type foreignMonitor struct {
	sourceID   string
	sourceType string
	name       string
	target     models.MonitorType
	config     map[string]interface{}
	interval   int
	// timeout is the source's own value in seconds; 0 means the source has
	// none and finish applies importDefaultTimeout.
	timeout           int
	enabled           bool
	failureThreshold  int
	recoveryThreshold int
	group             string
	warnings          []string
	// disabledByImport marks a monitor the source had enabled but the import
	// creates disabled (a credential or placeholder must be completed first).
	disabledByImport bool
	// derived names why the adapter added this monitor rather than
	// translating a source record one to one (models.ImportRow.Derived).
	derived string
}

func (m *foreignMonitor) warn(format string, args ...interface{}) {
	m.warnings = append(m.warnings, fmt.Sprintf(format, args...))
}

// disable creates the monitor disabled with a note saying what to complete.
func (m *foreignMonitor) disable(warning string) {
	if m.enabled {
		m.disabledByImport = true
	}
	m.enabled = false
	m.warnings = append(m.warnings, warning)
}

// setInterval clamps a source interval into Probara's range, reporting the
// change. sourceSeconds <= 0 keeps the current value.
func (m *foreignMonitor) setInterval(sourceSeconds int) {
	if sourceSeconds <= 0 {
		return
	}
	clamped := clampInt(sourceSeconds, minImportInterval, maxImportInterval)
	if clamped != sourceSeconds {
		m.warn("the check interval of %ds is outside Probara's range and was changed to %ds", sourceSeconds, clamped)
	}
	m.interval = clamped
}

// setPushPeriod sets a push monitor's expected check-in period and grace.
// A push monitor is late after interval + grace (monitorstate's
// EvidenceWindowSeconds), and the interval tops out at a day, so a longer
// source period moves its excess into the grace period: the deadline stays
// exactly the source's instead of expecting a weekly job every day.
func (m *foreignMonitor) setPushPeriod(period, grace int) {
	if grace < 0 {
		grace = 0
	}
	interval := period
	switch {
	case period > maxImportInterval:
		interval = maxImportInterval
		grace += period - maxImportInterval
		m.warn("the %ds period is longer than Probara's %ds maximum interval; the extra %ds went into the grace period, so the check-in deadline is unchanged",
			period, maxImportInterval, period-maxImportInterval)
	case period < minImportInterval:
		interval = minImportInterval
		m.warn("the %ds period is shorter than Probara's %ds minimum and was raised to it", period, minImportInterval)
	}
	m.interval = interval
	m.config = map[string]interface{}{
		"expected_interval_seconds": interval,
		"grace_period_seconds":      grace,
	}
}

// setFailureThreshold sets how many consecutive failures mark the monitor
// down, clamped to the column's range. It reports whether the value moved,
// so the caller can word the note in the source's terms.
func (m *foreignMonitor) setFailureThreshold(n int) bool {
	if n <= 0 {
		return false
	}
	m.failureThreshold = clampInt(n, minConsecutiveFailuresThreshold, maxConsecutiveFailuresThreshold)
	return m.failureThreshold != n
}

// setRecoveryThreshold sets how many passing checks mark the monitor up
// again (S-T7), clamped like setFailureThreshold.
func (m *foreignMonitor) setRecoveryThreshold(n int) bool {
	if n <= 0 {
		return false
	}
	m.recoveryThreshold = clampInt(n, monitorstate.MinRecoveryThreshold, monitorstate.MaxRecoveryThreshold)
	return m.recoveryThreshold != n
}

type foreignGroup struct {
	name    string
	enabled bool
}

// foreignTranslation accumulates one source document's translation.
type foreignTranslation struct {
	source     string
	schema     string
	monitors   []*foreignMonitor
	skipped    []models.ImportSkippedRow
	notes      []string
	noted      map[string]bool
	groups     map[string]*foreignGroup
	groupOrder []string
	domains    map[string]*foreignMonitor
}

func newForeignTranslation(source, schema string) *foreignTranslation {
	return &foreignTranslation{
		source:  source,
		schema:  schema,
		noted:   make(map[string]bool),
		groups:  make(map[string]*foreignGroup),
		domains: make(map[string]*foreignMonitor),
	}
}

func (t *foreignTranslation) skip(name, sourceType, reason string) {
	t.skipped = append(t.skipped, models.ImportSkippedRow{Name: name, SourceType: sourceType, Reason: reason})
}

// note records a document-level warning once, however many records cause it
// (regions, notification settings, maintenance windows).
func (t *foreignTranslation) note(message string) {
	if t.noted[message] {
		return
	}
	t.noted[message] = true
	t.notes = append(t.notes, message)
}

func (t *foreignTranslation) add(m *foreignMonitor) {
	t.monitors = append(t.monitors, m)
}

// declareGroup registers a group the source defines explicitly, with its own
// paused state. Groups only referenced by members are created enabled.
func (t *foreignTranslation) declareGroup(name string, enabled bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	key := strings.ToLower(name)
	if existing, ok := t.groups[key]; ok {
		// Two source groups with one name (a monitor group and a heartbeat
		// group, say) become one group; it stays enabled if either is.
		existing.enabled = existing.enabled || enabled
		return
	}
	t.groups[key] = &foreignGroup{name: name, enabled: enabled}
	t.groupOrder = append(t.groupOrder, key)
}

// addDomainExpiry turns a source's domain-expiry check into a domain
// monitor, one per registrable domain however many source monitors watch it.
// The widest warning window wins.
func (t *foreignTranslation) addDomainExpiry(origin *foreignMonitor, host string, days int, semantics string) {
	domain, err := domainexpiry.RegistrableDomain(host)
	if err != nil {
		origin.warn("the domain expiration check was not imported: the monitored host has no registrable domain to look up")
		return
	}
	days = clampInt(days, 0, sharedmodels.DomainMaxMinDaysValid)
	// A registry lookup needs none of the origin's credentials, so only the
	// source's own paused state carries over.
	enabled := origin.enabled || origin.disabledByImport

	if existing, ok := t.domains[domain]; ok {
		if current, _ := existing.config["min_days_valid"].(int); days > current {
			existing.config["min_days_valid"] = days
		}
		existing.enabled = existing.enabled || enabled
		origin.warn("its domain expiration check is covered by the %q domain monitor", existing.name)
		return
	}

	monitor := &foreignMonitor{
		sourceID:         origin.sourceID,
		sourceType:       "domain expiration",
		name:             domain + " domain expiry",
		target:           models.MonitorTypeDomain,
		config:           map[string]interface{}{"domain": domain, "min_days_valid": days},
		interval:         foreignDomainInterval,
		enabled:          enabled,
		failureThreshold: sharedmodels.DomainMinConsecutiveFailures,
		derived:          models.ImportRowDerivedDomainExpiry,
	}
	monitor.warn("created from the domain expiration check of %q; %s", origin.name, semantics)
	origin.warn("its domain expiration check became the separate %q domain monitor", monitor.name)
	t.domains[domain] = monitor
	t.monitors = append(t.monitors, monitor)
}

// finish validates, names and orders the translation into a parsedSource.
func (t *foreignTranslation) finish() *parsedSource {
	valid := make([]*foreignMonitor, 0, len(t.monitors))
	for _, m := range t.monitors {
		t.resolveTimeout(m)
		warnURLQueryCredentials(m)
		if reason := foreignValidationError(m); reason != "" {
			t.skip(m.name, m.sourceType, reason)
			continue
		}
		valid = append(valid, m)
	}

	renamed := t.disambiguate(valid)
	groupRows := t.groupRows(valid, &renamed)

	rows := make([]models.ImportRow, 0, len(valid)+len(groupRows))
	disabled := 0
	for _, m := range valid {
		if m.disabledByImport {
			disabled++
		}
		rows = append(rows, m.row(len(rows)))
	}
	for _, g := range groupRows {
		g.Index = len(rows)
		rows = append(rows, g)
	}

	warnings := make([]string, 0, len(t.notes)+3)
	if len(t.skipped) > 0 {
		warnings = append(warnings, fmt.Sprintf("%d %s record(s) could not be imported: %s",
			len(t.skipped), t.source, summarizeSkipReasons(t.skipped)))
	}
	if disabled > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%d monitor(s) will be created disabled until a credential or placeholder is completed - see their notes", disabled))
	}
	if renamed > 0 {
		warnings = append(warnings, fmt.Sprintf("%d monitor(s) were renamed to keep names unique", renamed))
	}
	warnings = append(warnings, t.notes...)

	return &parsedSource{Rows: rows, Schema: t.schema, Warnings: warnings, SkippedRows: t.skipped}
}

// resolveTimeout gives an active check the timeout ExecuteImport would, and
// keeps it below the interval, saying so when the source's own value moves.
func (t *foreignTranslation) resolveTimeout(m *foreignMonitor) {
	if !validation.IsActiveCheckType(m.target) {
		m.timeout = 0
		return
	}
	timeout := m.timeout
	if timeout <= 0 {
		timeout = importDefaultTimeout
	}
	if timeout >= m.interval {
		if m.timeout > 0 {
			m.warn("the %ds timeout was lowered to %ds to stay below the check interval", m.timeout, m.interval-1)
		}
		timeout = m.interval - 1
	}
	m.timeout = timeout
}

// foreignValidationError runs a translated monitor through the same checks
// ExecuteImport applies before create, so a translation the API rejects is
// reported now. The URL is cut out of the message: it can carry a token.
func foreignValidationError(m *foreignMonitor) string {
	raw, err := json.Marshal(m.config)
	if err != nil {
		return "the translated configuration could not be encoded"
	}
	if m.target != models.MonitorTypeGroup {
		err = validation.DefaultRegistry.Validate(m.target, raw)
		if err == nil {
			err = validation.DefaultRegistry.ValidateSchedule(m.target, raw, m.interval, m.timeout)
		}
	}
	if err == nil && m.target == models.MonitorTypeDomain {
		err = sharedmodels.ValidateDomainMonitorRules(m.failureThreshold, 0)
	}
	if err == nil {
		return ""
	}
	message := err.Error()
	if u, ok := m.config["url"].(string); ok && u != "" {
		message = strings.ReplaceAll(message, u, "<url>")
	}
	return "the translated monitor would be rejected: " + message
}

// disambiguate makes names unique across every translated monitor, whatever
// its type: the importer resolves group members by name alone, so two
// members sharing a name would make their group fail. It returns how many
// monitors were renamed.
func (t *foreignTranslation) disambiguate(monitors []*foreignMonitor) int {
	byName := make(map[string][]*foreignMonitor, len(monitors))
	taken := make(map[string]bool, len(monitors))
	for _, m := range monitors {
		key := strings.ToLower(m.name)
		byName[key] = append(byName[key], m)
		taken[key] = true
	}

	renamed := 0
	for _, m := range monitors {
		key := strings.ToLower(m.name)
		if len(byName[key]) < 2 || byName[key][0] == m {
			continue
		}
		candidates := make([]string, 0, 2)
		if m.group != "" {
			candidates = append(candidates, m.group+" / "+m.name)
		}
		candidates = append(candidates, fmt.Sprintf("%s (%s #%s)", m.name, t.source, m.sourceID))
		chosen := ""
		for _, candidate := range candidates {
			if !taken[strings.ToLower(candidate)] {
				chosen = candidate
				break
			}
		}
		for n := 2; chosen == ""; n++ {
			if candidate := fmt.Sprintf("%s (%d)", m.name, n); !taken[strings.ToLower(candidate)] {
				chosen = candidate
			}
		}
		taken[strings.ToLower(chosen)] = true
		m.warn("renamed from %q to keep monitor names unique", m.name)
		m.name = chosen
		renamed++
	}
	return renamed
}

// groupRows builds one group row per group that kept at least one member.
// A group whose name is also a monitor's is renamed: member resolution is by
// name, so the two would be ambiguous.
func (t *foreignTranslation) groupRows(monitors []*foreignMonitor, renamed *int) []models.ImportRow {
	members := make(map[string][]string)
	for _, m := range monitors {
		if m.group == "" {
			continue
		}
		key := strings.ToLower(m.group)
		if _, ok := t.groups[key]; !ok {
			t.declareGroup(m.group, true)
		}
		members[key] = append(members[key], m.name)
	}

	monitorNames := make(map[string]bool, len(monitors))
	for _, m := range monitors {
		monitorNames[strings.ToLower(m.name)] = true
	}

	rows := make([]models.ImportRow, 0, len(t.groupOrder))
	for _, key := range t.groupOrder {
		group := t.groups[key]
		names := members[key]
		if len(names) == 0 {
			t.skip(group.name, "group", "none of the group's monitors could be imported, so the group would have been empty")
			continue
		}
		name := group.name
		var warnings []string
		if monitorNames[strings.ToLower(name)] {
			name = group.name + " (group)"
			warnings = append(warnings, fmt.Sprintf("renamed from %q because a monitor has the same name", group.name))
			*renamed++
		}
		rows = append(rows, models.ImportRow{
			Fields: map[string]interface{}{
				"name":             name,
				"type":             string(models.MonitorTypeGroup),
				"config":           map[string]interface{}{"monitor_ids": []string{}},
				"interval_seconds": 60,
				"enabled":          group.enabled,
				"group_members":    names,
			},
			Warnings: warnings,
		})
	}
	return rows
}

// row renders the monitor with canonical field names, so suggestMapping
// wires every field and ExecuteImport takes its raw-config path.
func (m *foreignMonitor) row(index int) models.ImportRow {
	fields := map[string]interface{}{
		"name":             m.name,
		"type":             string(m.target),
		"config":           m.config,
		"interval_seconds": m.interval,
		"enabled":          m.enabled,
	}
	if m.timeout > 0 {
		fields["timeout_seconds"] = m.timeout
	}
	if m.failureThreshold > 0 {
		fields["consecutive_failures_threshold"] = m.failureThreshold
	}
	if m.recoveryThreshold > 0 {
		fields["recovery_threshold"] = m.recoveryThreshold
	}
	return models.ImportRow{Index: index, Fields: fields, Warnings: m.warnings, Derived: m.derived}
}

// ---------------------------------------------------------------------------
// shared field helpers
// ---------------------------------------------------------------------------

// foreignID accepts an identifier written as a JSON number or string.
type foreignID string

func (id *foreignID) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*id = ""
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		*id = foreignID(strings.TrimSpace(s))
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(trimmed, &n); err != nil {
		return fmt.Errorf("unexpected identifier %s", trimmed)
	}
	*id = foreignID(n.String())
	return nil
}

// jsonPresent reports whether an optional JSON value carries anything:
// absent, null, false and empty strings, arrays and objects do not.
func jsonPresent(raw json.RawMessage) bool {
	switch string(bytes.TrimSpace(raw)) {
	case "", "null", "false", `""`, "[]", "{}":
		return false
	}
	return true
}

// splitForeignHeaders sorts request headers into plain ones and
// credential-named ones (secrets.IsSensitiveHeaderName, the rule the API
// applies on every write) bound for the encrypted secret map.
func splitForeignHeaders(headers map[string]string) (map[string]string, map[string]string) {
	var plain, secret map[string]string
	for name, value := range headers {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if secrets.IsSensitiveHeaderName(name) {
			if secret == nil {
				secret = make(map[string]string)
			}
			secret[name] = value
			continue
		}
		if plain == nil {
			plain = make(map[string]string)
		}
		plain[name] = value
	}
	return plain, secret
}

// setForeignHTTPHeaders puts headers on an http config, credential-named
// ones in secret_headers.
func setForeignHTTPHeaders(config map[string]interface{}, headers map[string]string) {
	plain, secret := splitForeignHeaders(headers)
	if len(plain) > 0 {
		config["headers"] = plain
	}
	if len(secret) > 0 {
		config["secret_headers"] = secret
	}
}

// setForeignBasicAuth carries basic auth into the http monitor's own fields
// (the password is a registered secret, encrypted on create). An
// Authorization header beside it is dropped, since basic auth replaces it
// and the validator refuses both. A username without its password, or one
// basic auth cannot encode, leaves the monitor disabled with a note.
func setForeignBasicAuth(m *foreignMonitor, config map[string]interface{}, user, password string) {
	user = strings.TrimSpace(user)
	if user == "" {
		return
	}
	if strings.Contains(user, ":") || strings.ContainsAny(user+password, "\r\n") {
		m.disable("the basic auth credentials could not be imported (the username contains ':' or a line break), so this monitor was created disabled - set its authentication and enable it")
		return
	}
	config["basic_auth_username"] = user
	// An export that redacts the password with asterisks has not carried it.
	if strings.Trim(password, "*") == "" {
		password = ""
	}
	if password != "" {
		config["basic_auth_password"] = password
	} else {
		m.disable(foreignPasswordMissingWarning)
	}
	for _, key := range []string{"headers", "secret_headers"} {
		headers, _ := config[key].(map[string]string)
		for name := range headers {
			if strings.EqualFold(name, "Authorization") {
				delete(headers, name)
				m.warn("the Authorization header was dropped because the monitor's basic auth was imported in its place")
			}
		}
		if headers != nil && len(headers) == 0 {
			delete(config, key)
		}
	}
}

// parseForeignHTTPURL accepts an http(s) URL and moves any user:password@
// out of it, so a credential never lands in the plaintext url field. The
// returned reason never echoes the URL, which can carry a token.
func parseForeignHTTPURL(raw string) (clean string, user string, password string, reason string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", "", "monitor has no URL"
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", "", "", "the monitor's URL is not a valid http(s) URL"
	}
	if parsed.User != nil {
		user = parsed.User.Username()
		password, _ = parsed.User.Password()
		parsed.User = nil
	}
	return parsed.String(), user, password, ""
}

// warnURLQueryCredentials notes a URL whose query carries a credential
// (secrets.MaskURLCredentials, the rule that masks URL fields on every
// read), naming only the parameter. The value stays where the source put it:
// a URL has no secret counterpart, so the operator should move it to a secret
// header when the endpoint accepts one.
func warnURLQueryCredentials(m *foreignMonitor) {
	raw, ok := m.config["url"].(string)
	if !ok {
		return
	}
	if keys := urlQueryCredentialKeys(raw); len(keys) > 0 {
		m.warn("the URL carries a credential in its query (%s), which is stored with the URL as written and masked when read - move it to a secret header if the endpoint accepts one",
			strings.Join(keys, ", "))
	}
}

// urlQueryCredentialKeys lists the query parameters secrets.MaskURLCredentials
// would mask in raw, without their values.
func urlQueryCredentialKeys(raw string) []string {
	masked, changed := secrets.MaskURLCredentials(raw)
	if !changed {
		return nil
	}
	_, query, _ := strings.Cut(raw, "?")
	_, maskedQuery, _ := strings.Cut(masked, "?")
	query, _, _ = strings.Cut(query, "#")
	maskedQuery, _, _ = strings.Cut(maskedQuery, "#")
	before, after := strings.Split(query, "&"), strings.Split(maskedQuery, "&")
	var keys []string
	for i := range before {
		if i < len(after) && before[i] != after[i] {
			name, _, _ := strings.Cut(before[i], "=")
			keys = append(keys, name)
		}
	}
	return keys
}

// foreignURLName is a monitor name for a source record that has none: the
// URL's host and path, never its userinfo or query, which can carry a
// credential.
func foreignURLName(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Hostname() + strings.TrimSuffix(parsed.EscapedPath(), "/")
}

// isHTTPS reports whether a (validated) URL uses https.
func isHTTPS(raw string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(raw)), "https://")
}

// urlHostname is the host part of a URL, or "" when it has none.
func urlHostname(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// foreignHost accepts a bare host or a URL and returns the host, for
// sources whose host-based checks keep the target in a url field.
func foreignHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") {
		return urlHostname(raw)
	}
	return strings.TrimSuffix(raw, "/")
}

// statusSet is a set of HTTP status codes 100-599.
type statusSet [500]bool

func fullStatusSet() statusSet {
	var set statusSet
	for i := range set {
		set[i] = true
	}
	return set
}

func (s *statusSet) has(code int) bool {
	return code >= 100 && code <= 599 && s[code-100]
}

func (s *statusSet) set(code int, value bool) {
	if code >= 100 && code <= 599 {
		s[code-100] = value
	}
}

func (s *statusSet) empty() bool {
	for _, v := range s {
		if v {
			return false
		}
	}
	return true
}

// isDefault2xx reports whether the set is exactly 200-299, which is the
// http monitor's default and needs no field.
func (s *statusSet) isDefault2xx() bool {
	for code := 100; code <= 599; code++ {
		if s.has(code) != (code >= 200 && code <= 299) {
			return false
		}
	}
	return true
}

// apply writes the set onto an http config as discrete codes and ranges.
func (s *statusSet) apply(config map[string]interface{}) {
	statuses := make([]int, 0)
	ranges := make([]map[string]int, 0)
	for code := 100; code <= 599; code++ {
		if !s.has(code) {
			continue
		}
		end := code
		for end+1 <= 599 && s.has(end+1) {
			end++
		}
		if end == code {
			statuses = append(statuses, code)
		} else {
			ranges = append(ranges, map[string]int{"min": code, "max": end})
		}
		code = end
	}
	if len(statuses) > 0 {
		config["expected_statuses"] = statuses
	}
	if len(ranges) > 0 {
		config["expected_status_ranges"] = ranges
	}
}

// globToRegex turns a '*'-wildcard pattern into an anchored regular
// expression matching the whole value.
func globToRegex(pattern string) string {
	parts := strings.Split(pattern, "*")
	for i, part := range parts {
		parts[i] = regexp.QuoteMeta(part)
	}
	return "(?s)^" + strings.Join(parts, ".*") + "$"
}

// globSubstring returns x for a "*x*" pattern with no other wildcard: a
// plain substring test.
func globSubstring(pattern string) (string, bool) {
	if len(pattern) < 3 || !strings.HasPrefix(pattern, "*") || !strings.HasSuffix(pattern, "*") {
		return "", false
	}
	inner := pattern[1 : len(pattern)-1]
	if inner == "" || strings.Contains(inner, "*") {
		return "", false
	}
	return inner, true
}

// periodToChecks converts a source's time-based confirmation or recovery
// period into a check count at the given interval: the first check plus one
// for every interval the period spans.
func periodToChecks(periodSeconds, intervalSeconds int) int {
	if periodSeconds <= 0 || intervalSeconds <= 0 {
		return 1
	}
	return 1 + (periodSeconds+intervalSeconds-1)/intervalSeconds
}

// sortedKeys returns a map's keys in order, for stable messages.
func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
