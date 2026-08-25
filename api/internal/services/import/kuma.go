package importservice

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/yassinebenameur/probara/api/internal/models"
)

// Uptime Kuma migration support.
//
// Kuma's only machine-readable surfaces are the JSON backup it wrote up to
// v1.23 (Settings -> Backup -> Export) and the internal socket.io API that
// replaced it. Both carry the same monitor rows, so this adapter accepts the
// backup envelope and normalizes it into ImportRows that already name a
// Probara monitor type and carry a fully-formed nested config. Setting
// SuggestedMapping.Config from those rows puts ExecuteImport on its raw path,
// which is what gives us every registry type, group resolution by name and
// secret stripping without a second import pipeline.
//
// scripts/kuma-export writes exactly this envelope for Kuma 2.x, where the
// backup button no longer exists.

const (
	// kumaCertExpiryDays is what expiryNotification becomes. Kuma notifies at
	// 21 and 7 days; 7 is the "about to break" edge, and tls_min_days_valid
	// raises a dedicated tls_expiry alert rather than failing the check, so it
	// preserves Kuma's notify-only semantics.
	kumaCertExpiryDays = 7

	// kumaMaxRedirects mirrors the HTTP validator's ceiling.
	kumaMaxRedirects = 50

	// kumaDefaultPacketSize is Kuma's own default; only a deviation is worth
	// reporting as lost.
	kumaDefaultPacketSize = 56
)

// kumaTypeTargets maps a Kuma monitor type onto the Probara type that performs
// an equivalent check. Types absent here are either in kumaSkipReasons or
// unrecognized, and are never guessed at.
var kumaTypeTargets = map[string]models.MonitorType{
	"http":         models.MonitorTypeHTTP,
	"keyword":      models.MonitorTypeHTTP,
	"json-query":   models.MonitorTypeHTTP,
	"grpc-keyword": models.MonitorTypeGRPC,
	"port":         models.MonitorTypeTCP,
	"ping":         models.MonitorTypePing,
	"dns":          models.MonitorTypeDNS,
	"push":         models.MonitorTypePush,
	"group":        models.MonitorTypeGroup,
	"postgres":     models.MonitorTypePostgres,
	"mysql":        models.MonitorTypeMySQL,
	"mongodb":      models.MonitorTypeMongoDB,
	"redis":        models.MonitorTypeRedis,
}

// kumaSkipReasons explains, per Kuma type, why there is nothing faithful to
// import. Degrading these into a near neighbour would produce monitors that
// look healthy while checking something else, so they are reported instead.
var kumaSkipReasons = map[string]string{
	"docker":         "Probara has no Docker monitor type",
	"sqlserver":      "Probara has no SQL Server monitor type",
	"steam":          "Steam server queries are not implemented",
	"gamedig":        "GameDig server queries are not implemented",
	"mqtt":           "MQTT checks are not implemented",
	"kafka-producer": "Kafka producer checks are not implemented",
	"radius":         "RADIUS checks are not implemented",
	"snmp":           "SNMP checks are not implemented",
	"tailscale-ping": "Tailscale ping requires the tailscale CLI on the prober",
	"manual":         "a manually-set status has no scheduled-check equivalent",
	"rabbitmq":       "Uptime Kuma polls the RabbitMQ management HTTP API while Probara's rabbitmq monitor performs an AMQP handshake - different port, different credential, different failure modes",
	"smtp":           "an SMTP banner check would degrade to a bare TCP connect",
	"real-browser":   "a real-browser check would degrade to a page load with no assertions - recreate it as a synthetic_browser monitor with steps",
}

var kumaHTTPMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "HEAD": true, "OPTIONS": true,
}

var kumaDNSRecordTypes = map[string]bool{
	"A": true, "AAAA": true, "CNAME": true, "TXT": true, "MX": true, "NS": true,
}

// kumaSensitiveHeaders are request headers we refuse to carry across. HTTP
// config headers are not in shared/secrets.MonitorSecretFields, so anything
// placed there is stored in plaintext and read back unmasked.
var kumaSensitiveHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"x-api-key":           true,
	"x-auth-token":        true,
}

// kumaBackup is the envelope of a Kuma backup export.
type kumaBackup struct {
	Version          string             `json:"version"`
	MonitorList      kumaMonitorList    `json:"monitorList"`
	NotificationList []kumaNotification `json:"notificationList"`
}

type kumaNotification struct {
	ID   kumaInt `json:"id"`
	Name string  `json:"name"`
}

// kumaMonitorList accepts both shapes Kuma produces: the backup file writes a
// JSON array, while the socket.io monitorList event is an object keyed by
// monitor id.
type kumaMonitorList []kumaMonitor

func (l *kumaMonitorList) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*l = nil
		return nil
	}

	if trimmed[0] == '[' {
		var arr []kumaMonitor
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return err
		}
		*l = arr
		return nil
	}

	var byID map[string]kumaMonitor
	if err := json.Unmarshal(trimmed, &byID); err != nil {
		return err
	}

	keys := make([]string, 0, len(byID))
	for key := range byID {
		keys = append(keys, key)
	}
	// Numeric ids sort numerically so the emitted order matches the backup
	// file's, which keeps fixtures and previews stable.
	sort.Slice(keys, func(i, j int) bool {
		a, errA := strconv.Atoi(keys[i])
		b, errB := strconv.Atoi(keys[j])
		if errA == nil && errB == nil {
			return a < b
		}
		return keys[i] < keys[j]
	})

	out := make([]kumaMonitor, 0, len(byID))
	for _, key := range keys {
		out = append(out, byID[key])
	}
	*l = out
	return nil
}

// kumaBool tolerates every spelling of a boolean Kuma emits. SQLite-backed
// exports use 0/1, the socket.io payload uses true/false, and some columns
// arrive as quoted strings.
type kumaBool bool

func (b *kumaBool) UnmarshalJSON(data []byte) error {
	raw := strings.ToLower(strings.Trim(strings.TrimSpace(string(data)), `"`))
	switch raw {
	case "true", "1":
		*b = true
	case "false", "0", "", "null":
		*b = false
	default:
		return fmt.Errorf("unexpected boolean value %q", raw)
	}
	return nil
}

// kumaInt tolerates numbers arriving as JSON numbers, quoted strings, floats
// or null. Absent and null both mean zero, which every caller treats as unset.
type kumaInt int

func (i *kumaInt) UnmarshalJSON(data []byte) error {
	raw := strings.Trim(strings.TrimSpace(string(data)), `"`)
	if raw == "" || strings.EqualFold(raw, "null") {
		*i = 0
		return nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fmt.Errorf("unexpected numeric value %q", raw)
	}
	*i = kumaInt(int(value))
	return nil
}

// kumaStringList accepts a JSON array of strings, a JSON-encoded string
// containing such an array (Kuma's *_json columns), or null.
type kumaStringList []string

func (l *kumaStringList) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*l = nil
		return nil
	}

	if trimmed[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return err
		}
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			out = append(out, strings.Trim(strings.TrimSpace(string(item)), `"`))
		}
		*l = out
		return nil
	}

	var encoded string
	if err := json.Unmarshal(trimmed, &encoded); err != nil {
		return err
	}
	if strings.TrimSpace(encoded) == "" {
		*l = nil
		return nil
	}
	var nested []json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &nested); err != nil {
		// A bare scalar string is a single entry.
		*l = []string{encoded}
		return nil
	}
	out := make([]string, 0, len(nested))
	for _, item := range nested {
		out = append(out, strings.Trim(strings.TrimSpace(string(item)), `"`))
	}
	*l = out
	return nil
}

// kumaIDList accepts notificationIDList in both its object form
// ({"1": true}) and the array form some tooling produces.
type kumaIDList []string

func (l *kumaIDList) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*l = nil
		return nil
	}

	if trimmed[0] == '{' {
		var asMap map[string]interface{}
		if err := json.Unmarshal(trimmed, &asMap); err != nil {
			return err
		}
		ids := make([]string, 0, len(asMap))
		for key, value := range asMap {
			if enabled, ok := value.(bool); ok && !enabled {
				continue
			}
			ids = append(ids, key)
		}
		sort.Strings(ids)
		*l = ids
		return nil
	}

	var arr []json.RawMessage
	if err := json.Unmarshal(trimmed, &arr); err != nil {
		return err
	}
	ids := make([]string, 0, len(arr))
	for _, item := range arr {
		ids = append(ids, strings.Trim(strings.TrimSpace(string(item)), `"`))
	}
	*l = ids
	return nil
}

type kumaTag struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// kumaMonitor is one row of Kuma's monitor table. Only fields this adapter
// reads are declared; unknown columns are ignored by encoding/json.
type kumaMonitor struct {
	ID             kumaInt  `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Type           string   `json:"type"`
	Active         kumaBool `json:"active"`
	Interval       kumaInt  `json:"interval"`
	RetryInterval  kumaInt  `json:"retryInterval"`
	ResendInterval kumaInt  `json:"resendInterval"`
	MaxRetries     kumaInt  `json:"maxretries"`
	Timeout        kumaInt  `json:"timeout"`
	UpsideDown     kumaBool `json:"upsideDown"`
	Parent         kumaInt  `json:"parent"`
	PathName       string   `json:"pathName"`

	// http / keyword / json-query
	URL                     string         `json:"url"`
	Method                  string         `json:"method"`
	Body                    string         `json:"body"`
	Headers                 string         `json:"headers"`
	MaxRedirects            kumaInt        `json:"maxredirects"`
	AcceptedStatusCodes     kumaStringList `json:"accepted_statuscodes"`
	AcceptedStatusCodesJSON kumaStringList `json:"accepted_statuscodes_json"`
	IgnoreTLS               kumaBool       `json:"ignoreTls"`
	ExpiryNotification      kumaBool       `json:"expiryNotification"`
	Keyword                 string         `json:"keyword"`
	InvertKeyword           kumaBool       `json:"invertKeyword"`
	JSONPath                string         `json:"jsonPath"`
	ExpectedValue           string         `json:"expectedValue"`
	AuthMethod              string         `json:"authMethod"`
	BasicAuthUser           string         `json:"basic_auth_user"`
	BasicAuthPass           string         `json:"basic_auth_pass"`
	OAuthClientID           string         `json:"oauth_client_id"`
	OAuthClientSecret       string         `json:"oauth_client_secret"`
	TLSCert                 string         `json:"tlsCert"`
	TLSKey                  string         `json:"tlsKey"`

	// host-based
	Hostname   string  `json:"hostname"`
	Port       kumaInt `json:"port"`
	PacketSize kumaInt `json:"packetSize"`

	// dns
	DNSResolveServer string `json:"dns_resolve_server"`
	DNSResolveType   string `json:"dns_resolve_type"`

	// grpc
	GRPCURL         string   `json:"grpcUrl"`
	GRPCServiceName string   `json:"grpcServiceName"`
	GRPCMethod      string   `json:"grpcMethod"`
	GRPCEnableTLS   kumaBool `json:"grpcEnableTls"`

	// databases
	DatabaseConnectionString string `json:"databaseConnectionString"`
	DatabaseQuery            string `json:"databaseQuery"`

	Tags               []kumaTag  `json:"tags"`
	NotificationIDList kumaIDList `json:"notificationIDList"`
}

// acceptedStatusCodes prefers the decoded array and falls back to the
// *_json column a raw table dump carries.
func (m kumaMonitor) acceptedStatusCodes() []string {
	if len(m.AcceptedStatusCodes) > 0 {
		return m.AcceptedStatusCodes
	}
	return m.AcceptedStatusCodesJSON
}

// kumaMapped is a monitor that survived translation, held until names are
// disambiguated and group membership is inverted.
type kumaMapped struct {
	source   kumaMonitor
	target   models.MonitorType
	config   map[string]interface{}
	name     string
	enabled  bool
	members  []string
	warnings []string
}

// parseKumaExport recognizes an Uptime Kuma backup envelope and translates it.
// The bool reports whether the payload was Kuma's at all, so a non-match falls
// through to the generic JSON shapes.
func (s *Service) parseKumaExport(data []byte) (*parsedSource, bool, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false, nil
	}

	var probe struct {
		MonitorList json.RawMessage `json:"monitorList"`
	}
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return nil, false, nil
	}
	raw := bytes.TrimSpace(probe.MonitorList)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, false, nil
	}

	// From here the payload is unambiguously Kuma's, so a decode failure is a
	// real error rather than a reason to try the generic parsers.
	var bundle kumaBackup
	if err := json.Unmarshal(trimmed, &bundle); err != nil {
		return nil, true, fmt.Errorf("invalid Uptime Kuma export: %w", err)
	}

	return translateKumaBackup(bundle), true, nil
}

func translateKumaBackup(bundle kumaBackup) *parsedSource {
	notifierNames := make(map[string]string, len(bundle.NotificationList))
	for _, notifier := range bundle.NotificationList {
		notifierNames[strconv.Itoa(int(notifier.ID))] = notifier.Name
	}

	nameByID := make(map[int]string, len(bundle.MonitorList))
	parentByID := make(map[int]int, len(bundle.MonitorList))
	for _, monitor := range bundle.MonitorList {
		nameByID[int(monitor.ID)] = strings.TrimSpace(monitor.Name)
		parentByID[int(monitor.ID)] = int(monitor.Parent)
	}

	mapped := make([]*kumaMapped, 0, len(bundle.MonitorList))
	skipped := make([]models.ImportSkippedRow, 0)

	for _, monitor := range bundle.MonitorList {
		entry, reason := translateKumaMonitor(monitor, notifierNames)
		if reason != "" {
			skipped = append(skipped, models.ImportSkippedRow{
				Name:       kumaDisplayName(monitor),
				SourceType: strings.TrimSpace(monitor.Type),
				Reason:     reason,
			})
			continue
		}
		mapped = append(mapped, entry)
	}

	disambiguateKumaNames(mapped, nameByID, parentByID)

	mapped, emptyGroups := attachKumaGroupMembers(mapped)
	skipped = append(skipped, emptyGroups...)
	orderKumaMapped(mapped, parentByID)

	rows := make([]models.ImportRow, 0, len(mapped))
	for i, entry := range mapped {
		rows = append(rows, entry.row(i))
	}

	return &parsedSource{
		Rows:        rows,
		Schema:      models.ImportSchemaUptimeKumaExport,
		Warnings:    summarizeKumaTranslation(mapped, skipped),
		SkippedRows: skipped,
	}
}

// translateKumaMonitor turns one Kuma row into a mapped entry, or returns a
// reason it cannot be imported.
func translateKumaMonitor(monitor kumaMonitor, notifierNames map[string]string) (*kumaMapped, string) {
	kumaType := strings.ToLower(strings.TrimSpace(monitor.Type))
	if kumaType == "" {
		return nil, "monitor has no type"
	}

	if bool(monitor.UpsideDown) {
		return nil, "\"upside down\" mode inverts up and down, which Probara cannot express - importing it would alert backwards"
	}

	if reason, ok := kumaSkipReasons[kumaType]; ok {
		return nil, reason
	}

	target, ok := kumaTypeTargets[kumaType]
	if !ok {
		return nil, fmt.Sprintf("unrecognized Uptime Kuma monitor type %q", kumaType)
	}

	var (
		config   map[string]interface{}
		warnings []string
		reason   string
		disable  bool
	)

	switch kumaType {
	case "http", "keyword", "json-query":
		config, warnings, reason = kumaHTTPConfig(monitor, kumaType)
	case "grpc-keyword":
		config, warnings, reason = kumaGRPCConfig(monitor)
	case "port":
		config, warnings, reason = kumaTCPConfig(monitor)
	case "ping":
		config, warnings, reason = kumaPingConfig(monitor)
	case "dns":
		config, warnings, reason = kumaDNSConfig(monitor)
	case "push":
		config, warnings, reason = kumaPushConfig(monitor)
	case "group":
		config, warnings, reason = kumaGroupConfig()
	case "postgres", "mysql", "mongodb", "redis":
		config, warnings, reason, disable = kumaDBConfig(monitor, target)
	default:
		return nil, fmt.Sprintf("unrecognized Uptime Kuma monitor type %q", kumaType)
	}
	if reason != "" {
		return nil, reason
	}

	entry := &kumaMapped{
		source:  monitor,
		target:  target,
		config:  config,
		name:    kumaDisplayName(monitor),
		enabled: bool(monitor.Active) && !disable,
	}
	entry.warnings = append(entry.warnings, warnings...)
	entry.warnings = append(entry.warnings, kumaCommonWarnings(monitor, notifierNames)...)
	return entry, ""
}

func kumaDisplayName(monitor kumaMonitor) string {
	if name := strings.TrimSpace(monitor.Name); name != "" {
		return name
	}
	return fmt.Sprintf("Uptime Kuma monitor %d", int(monitor.ID))
}

// kumaCommonWarnings reports the type-independent settings that do not survive
// the move, so nothing silently changes behavior.
func kumaCommonWarnings(monitor kumaMonitor, notifierNames map[string]string) []string {
	var warnings []string

	if len(monitor.NotificationIDList) > 0 {
		names := make([]string, 0, len(monitor.NotificationIDList))
		for _, id := range monitor.NotificationIDList {
			if name, ok := notifierNames[id]; ok && strings.TrimSpace(name) != "" {
				names = append(names, strconv.Quote(name))
				continue
			}
			names = append(names, "notifier "+id)
		}
		warnings = append(warnings, fmt.Sprintf(
			"Uptime Kuma notifications were not migrated (%s) - attach Probara alert channels to this monitor",
			strings.Join(names, ", ")))
	}

	if interval := int(monitor.Interval); interval > 0 && (interval < minKumaInterval || interval > maxKumaInterval) {
		warnings = append(warnings, fmt.Sprintf(
			"check interval %ds is outside Probara's supported range and was clamped to %ds",
			interval, clampInt(interval, minKumaInterval, maxKumaInterval)))
	}

	if retry := int(monitor.RetryInterval); retry > 0 && retry != int(monitor.Interval) {
		warnings = append(warnings, "Uptime Kuma's retry interval (faster polling while down) has no equivalent - Probara keeps one fixed interval")
	}

	if int(monitor.ResendInterval) > 0 {
		warnings = append(warnings, "Uptime Kuma's notification resend interval has no equivalent and was dropped")
	}

	if strings.TrimSpace(monitor.Description) != "" {
		warnings = append(warnings, "the monitor description was dropped - Probara monitors have no description field")
	}

	return warnings
}

const (
	minKumaInterval = 10
	maxKumaInterval = 86400
)

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// ---------------------------------------------------------------------------
// per-type config builders
// ---------------------------------------------------------------------------

func kumaHTTPConfig(monitor kumaMonitor, kumaType string) (map[string]interface{}, []string, string) {
	raw := strings.TrimSpace(monitor.URL)
	if raw == "" {
		return nil, nil, "monitor has no URL"
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, nil, fmt.Sprintf("%q is not a valid http(s) URL", raw)
	}

	method := strings.ToUpper(strings.TrimSpace(monitor.Method))
	if method == "" {
		method = "GET"
	}
	if !kumaHTTPMethods[method] {
		return nil, nil, fmt.Sprintf("HTTP method %q is not supported", method)
	}

	config := map[string]interface{}{"url": raw, "method": method}
	var warnings []string

	headers, dropped, headerErr := parseKumaHeaders(monitor.Headers)
	if headerErr != "" {
		warnings = append(warnings, headerErr)
	}
	if len(headers) > 0 {
		config["headers"] = headers
	}
	if len(dropped) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"credential-bearing request headers were not imported (%s) - re-enter them on the monitor",
			strings.Join(dropped, ", ")))
	}

	if body := strings.TrimSpace(monitor.Body); body != "" {
		config["body"] = body
	}

	statuses, ranges, statusWarn := parseKumaAcceptedStatusCodes(monitor.acceptedStatusCodes())
	if len(statuses) > 0 {
		config["expected_statuses"] = statuses
	}
	if len(ranges) > 0 {
		config["expected_status_ranges"] = ranges
	}
	if statusWarn != "" {
		warnings = append(warnings, statusWarn)
	}

	// Kuma treats 0 redirects as "do not follow".
	if redirects := int(monitor.MaxRedirects); redirects > 0 {
		config["max_redirects"] = clampInt(redirects, 0, kumaMaxRedirects)
	} else {
		config["follow_redirects"] = false
	}

	// tls_skip_verify and tls_min_days_valid are rejected outright on plain
	// HTTP, so they are gated on the scheme rather than copied.
	if parsed.Scheme == "https" {
		if bool(monitor.IgnoreTLS) {
			config["tls_skip_verify"] = true
		}
		if bool(monitor.ExpiryNotification) {
			config["tls_min_days_valid"] = kumaCertExpiryDays
		}
	} else if bool(monitor.IgnoreTLS) || bool(monitor.ExpiryNotification) {
		warnings = append(warnings, "TLS settings were dropped because the monitor targets a plain http:// URL")
	}

	switch kumaType {
	case "keyword":
		keyword := strings.TrimSpace(monitor.Keyword)
		if keyword == "" {
			warnings = append(warnings, "the keyword check had no keyword set and was imported as a plain HTTP check")
			break
		}
		op := "contains"
		if bool(monitor.InvertKeyword) {
			op = "not_contains"
		}
		config["body_assertions"] = []map[string]interface{}{{"op": op, "value": keyword}}

	case "json-query":
		path := normalizeKumaJSONPath(monitor.JSONPath)
		if path == "" {
			warnings = append(warnings, "the JSON query check had no path set and was imported as a plain HTTP check")
			break
		}
		assertion := map[string]interface{}{"path": path}
		if expected := strings.TrimSpace(monitor.ExpectedValue); expected != "" {
			assertion["op"] = "equals"
			assertion["value"] = expected
		} else {
			assertion["op"] = "exists"
		}
		config["json_assertions"] = []map[string]interface{}{assertion}
		warnings = append(warnings, fmt.Sprintf(
			"Uptime Kuma evaluates JSON queries with JSONata while Probara uses gjson paths; %q was carried over verbatim and should be verified",
			path))
	}

	if authWarning := kumaAuthWarning(monitor); authWarning != "" {
		warnings = append(warnings, authWarning)
	}

	return config, warnings, ""
}

// kumaAuthWarning reports dropped HTTP authentication. None of it is imported:
// Probara's http config has no auth fields, and folding credentials into
// headers would store them in plaintext.
func kumaAuthWarning(monitor kumaMonitor) string {
	dropped := make([]string, 0, 4)
	if strings.TrimSpace(monitor.BasicAuthPass) != "" || strings.TrimSpace(monitor.BasicAuthUser) != "" {
		dropped = append(dropped, "basic auth")
	}
	if strings.TrimSpace(monitor.OAuthClientSecret) != "" || strings.TrimSpace(monitor.OAuthClientID) != "" {
		dropped = append(dropped, "OAuth2 client credentials")
	}
	if strings.TrimSpace(monitor.TLSKey) != "" || strings.TrimSpace(monitor.TLSCert) != "" {
		dropped = append(dropped, "client certificate")
	}
	if method := strings.ToLower(strings.TrimSpace(monitor.AuthMethod)); method == "ntlm" || method == "mtls" {
		dropped = append(dropped, method+" authentication")
	}
	if len(dropped) == 0 {
		return ""
	}
	return fmt.Sprintf("HTTP authentication was not imported (%s) - Probara's HTTP monitor has no credential fields", strings.Join(dropped, ", "))
}

func kumaTCPConfig(monitor kumaMonitor) (map[string]interface{}, []string, string) {
	host := strings.TrimSpace(monitor.Hostname)
	if host == "" {
		return nil, nil, "monitor has no hostname"
	}
	port := int(monitor.Port)
	if port <= 0 || port > 65535 {
		return nil, nil, "TCP monitors require a port, and this one has none"
	}
	return map[string]interface{}{"host": host, "port": port}, nil, ""
}

func kumaPingConfig(monitor kumaMonitor) (map[string]interface{}, []string, string) {
	host := strings.TrimSpace(monitor.Hostname)
	if host == "" {
		return nil, nil, "monitor has no hostname"
	}
	var warnings []string
	if size := int(monitor.PacketSize); size > 0 && size != kumaDefaultPacketSize {
		warnings = append(warnings, fmt.Sprintf("the custom ICMP packet size (%d bytes) was dropped - Probara's ping check is not tunable", size))
	}
	return map[string]interface{}{"host": host}, warnings, ""
}

func kumaDNSConfig(monitor kumaMonitor) (map[string]interface{}, []string, string) {
	host := strings.TrimSpace(monitor.Hostname)
	if host == "" {
		return nil, nil, "monitor has no hostname to resolve"
	}

	recordType := strings.ToUpper(strings.TrimSpace(monitor.DNSResolveType))
	if recordType == "" {
		recordType = "A"
	}
	if !kumaDNSRecordTypes[recordType] {
		return nil, nil, fmt.Sprintf("DNS record type %s is not supported (Probara resolves A, AAAA, CNAME, TXT, MX and NS)", recordType)
	}

	config := map[string]interface{}{"host": host, "record_type": recordType}

	if resolver := strings.TrimSpace(monitor.DNSResolveServer); resolver != "" {
		if port := int(monitor.Port); port > 0 && port != 53 {
			config["nameserver"] = fmt.Sprintf("%s:%d", resolver, port)
		} else {
			config["nameserver"] = resolver
		}
	}

	if expected := strings.TrimSpace(monitor.Keyword); expected != "" {
		config["expected_answers"] = []string{expected}
	}

	return config, nil, ""
}

func kumaGRPCConfig(monitor kumaMonitor) (map[string]interface{}, []string, string) {
	target := strings.TrimSpace(monitor.GRPCURL)
	if target == "" {
		return nil, nil, "monitor has no gRPC URL"
	}

	host, port, useTLS := splitKumaGRPCTarget(target)
	if host == "" {
		return nil, nil, fmt.Sprintf("%q is not a valid gRPC target", target)
	}

	config := map[string]interface{}{"host": host}
	if port > 0 {
		config["port"] = port
	}
	if bool(monitor.GRPCEnableTLS) || useTLS {
		config["use_tls"] = true
	}

	// Deliberately not carrying grpcServiceName into `service`: Probara probes
	// grpc.health.v1 and `service` names the health service to query, not the
	// RPC Kuma invoked. Copying it across would fail every check.
	warnings := []string{
		"Probara's gRPC monitor performs a grpc.health.v1 health probe; the Uptime Kuma method call and keyword assertion were not imported",
	}

	return config, warnings, ""
}

// splitKumaGRPCTarget accepts "host:port", "scheme://host:port" and bare
// hostnames, reporting whether the scheme implied TLS.
func splitKumaGRPCTarget(target string) (string, int, bool) {
	useTLS := false
	if idx := strings.Index(target, "://"); idx >= 0 {
		scheme := strings.ToLower(target[:idx])
		useTLS = scheme == "grpcs" || scheme == "https"
		target = target[idx+3:]
	}
	target = strings.TrimSuffix(strings.TrimSpace(target), "/")
	if target == "" {
		return "", 0, useTLS
	}
	if slash := strings.Index(target, "/"); slash >= 0 {
		target = target[:slash]
	}

	host := target
	port := 0
	if idx := strings.LastIndex(target, ":"); idx > 0 && !strings.Contains(target[idx+1:], "]") {
		if parsed, err := strconv.Atoi(target[idx+1:]); err == nil && parsed > 0 && parsed <= 65535 {
			host = target[:idx]
			port = parsed
		}
	}
	return strings.Trim(host, "[]"), port, useTLS
}

func kumaPushConfig(monitor kumaMonitor) (map[string]interface{}, []string, string) {
	interval := clampInt(int(monitor.Interval), minKumaInterval, maxKumaInterval)
	config := map[string]interface{}{
		"expected_interval_seconds": interval,
		"grace_period_seconds":      0,
	}
	warnings := []string{
		"Probara issues its own push token, so this monitor's push URL is different from the Uptime Kuma one - repoint whatever sends the heartbeat before enabling it",
	}
	return config, warnings, ""
}

func kumaGroupConfig() (map[string]interface{}, []string, string) {
	// Members are resolved by name during the import's second pass; the empty
	// list here is overwritten by createGroupMonitorFromConfig.
	return map[string]interface{}{"monitor_ids": []string{}}, nil, ""
}

// kumaDBConfig translates Kuma's single databaseConnectionString into discrete
// fields, deliberately dropping the password. The fourth return value asks the
// caller to import the monitor disabled, so a credential-less database check
// cannot page anyone before an operator completes it.
func kumaDBConfig(monitor kumaMonitor, target models.MonitorType) (map[string]interface{}, []string, string, bool) {
	conn := strings.TrimSpace(monitor.DatabaseConnectionString)
	if conn == "" {
		return nil, nil, "monitor has no database connection string", false
	}

	parsed, err := url.Parse(conn)
	if err != nil || parsed.Host == "" {
		// Never echo the connection string: it carries the password.
		return nil, nil, "the database connection string could not be parsed", false
	}

	scheme := strings.ToLower(parsed.Scheme)
	allowed, defaultPort := kumaDBSchemes(target)
	if !allowed[scheme] {
		return nil, nil, fmt.Sprintf("connection scheme %q is not valid for a %s monitor", scheme, target), false
	}

	config := map[string]interface{}{"host": parsed.Hostname()}
	if portText := parsed.Port(); portText != "" {
		port, convErr := strconv.Atoi(portText)
		if convErr != nil || port <= 0 || port > 65535 {
			return nil, nil, "the database connection string has an invalid port", false
		}
		config["port"] = port
	} else if defaultPort > 0 {
		config["port"] = defaultPort
	}

	username := ""
	if parsed.User != nil {
		username = parsed.User.Username()
	}
	database := strings.TrimPrefix(parsed.Path, "/")

	var warnings []string

	switch target {
	case models.MonitorTypePostgres:
		if username == "" {
			return nil, nil, "PostgreSQL monitors require a username and the connection string has none", false
		}
		config["username"] = username
		if database != "" {
			config["database"] = database
		}
		if mode := strings.ToLower(parsed.Query().Get("sslmode")); validKumaPostgresSSLModes[mode] {
			config["ssl_mode"] = mode
		}

	case models.MonitorTypeMySQL:
		if username == "" {
			return nil, nil, "MySQL monitors require a username and the connection string has none", false
		}
		config["username"] = username
		if database != "" {
			config["database"] = database
		}

	case models.MonitorTypeMongoDB:
		// MongoDB rejects a username without a password, and the password is
		// never imported, so the username is dropped with it.
		if username != "" {
			warnings = append(warnings, "the MongoDB username was dropped along with the password, which must be supplied together")
		}
		if authSource := strings.TrimSpace(parsed.Query().Get("authSource")); authSource != "" {
			config["auth_source"] = authSource
		}
		if replicaSet := strings.TrimSpace(parsed.Query().Get("replicaSet")); replicaSet != "" {
			config["replica_set"] = replicaSet
		}

	case models.MonitorTypeRedis:
		if username != "" {
			config["username"] = username
		}
		if db := strings.TrimPrefix(parsed.Path, "/"); db != "" {
			if index, convErr := strconv.Atoi(db); convErr == nil && index >= 0 && index <= 15 {
				config["db"] = index
			}
		}
	}

	if scheme == "rediss" || scheme == "mongodb+srv" {
		config["tls_enabled"] = true
	}

	warnings = append(warnings, "the database password was not imported, so this monitor was created disabled - set the password and enable it")

	if query := strings.TrimSpace(monitor.DatabaseQuery); query != "" {
		switch target {
		case models.MonitorTypePostgres, models.MonitorTypeMySQL:
			config["query"] = query
			warnings = append(warnings, "Uptime Kuma only required the query to execute, while Probara asserts it returns at least one row")
		default:
			warnings = append(warnings, fmt.Sprintf("the Uptime Kuma query was dropped - Probara's %s monitor does not run queries", target))
		}
	}

	return config, warnings, "", true
}

var validKumaPostgresSSLModes = map[string]bool{
	"disable":     true,
	"require":     true,
	"verify-full": true,
}

func kumaDBSchemes(target models.MonitorType) (map[string]bool, int) {
	switch target {
	case models.MonitorTypePostgres:
		return map[string]bool{"postgres": true, "postgresql": true}, 5432
	case models.MonitorTypeMySQL:
		return map[string]bool{"mysql": true}, 3306
	case models.MonitorTypeMongoDB:
		return map[string]bool{"mongodb": true, "mongodb+srv": true}, 27017
	case models.MonitorTypeRedis:
		return map[string]bool{"redis": true, "rediss": true}, 6379
	default:
		return map[string]bool{}, 0
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// parseKumaHeaders decodes Kuma's JSON-string headers column, dropping any
// header whose name suggests it carries a credential.
func parseKumaHeaders(raw string) (map[string]string, []string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" || raw == "{}" {
		return nil, nil, ""
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, nil, "the custom request headers could not be parsed and were dropped"
	}

	headers := make(map[string]string, len(decoded))
	dropped := make([]string, 0)
	for name, value := range decoded {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if isKumaSensitiveHeader(trimmed) {
			dropped = append(dropped, trimmed)
			continue
		}
		headers[trimmed] = fmt.Sprint(value)
	}
	sort.Strings(dropped)
	if len(headers) == 0 {
		headers = nil
	}
	return headers, dropped, ""
}

func isKumaSensitiveHeader(name string) bool {
	lower := strings.ToLower(name)
	if kumaSensitiveHeaders[lower] {
		return true
	}
	for _, marker := range []string{"token", "secret", "apikey", "api-key", "password"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// parseKumaAcceptedStatusCodes converts Kuma's status-code selectors into the
// discrete and range forms Probara's HTTP config uses.
func parseKumaAcceptedStatusCodes(codes []string) ([]int, []map[string]int, string) {
	statuses := make([]int, 0, len(codes))
	ranges := make([]map[string]int, 0, len(codes))
	unsupported := make([]string, 0)

	for _, code := range codes {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}

		if lower, upper, ok := strings.Cut(code, "-"); ok {
			min, minErr := strconv.Atoi(strings.TrimSpace(lower))
			max, maxErr := strconv.Atoi(strings.TrimSpace(upper))
			if minErr != nil || maxErr != nil || min < 100 || max > 599 || min > max {
				unsupported = append(unsupported, code)
				continue
			}
			ranges = append(ranges, map[string]int{"min": min, "max": max})
			continue
		}

		status, err := strconv.Atoi(code)
		if err != nil || status < 100 || status > 599 {
			unsupported = append(unsupported, code)
			continue
		}
		statuses = append(statuses, status)
	}

	warning := ""
	if len(unsupported) > 0 {
		warning = fmt.Sprintf("accepted status codes %s were not understood and were dropped", strings.Join(unsupported, ", "))
	}

	if len(statuses) == 0 {
		statuses = nil
	}
	if len(ranges) == 0 {
		ranges = nil
	}
	return statuses, ranges, warning
}

// normalizeKumaJSONPath strips the JSONata/JSONPath root prefix so the
// remainder reads as a gjson path.
func normalizeKumaJSONPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "$.")
	path = strings.TrimPrefix(path, "$")
	return strings.TrimSpace(path)
}

// disambiguateKumaNames resolves name collisions among mapped monitors. Kuma
// allows the same name under two parents, but Probara's importer dedupes on
// type+name and group resolution rejects an ambiguous member name.
func disambiguateKumaNames(mapped []*kumaMapped, nameByID map[int]string, parentByID map[int]int) {
	byKey := make(map[string][]*kumaMapped, len(mapped))
	for _, entry := range mapped {
		key := string(entry.target) + "\x00" + strings.ToLower(entry.name)
		byKey[key] = append(byKey[key], entry)
	}

	taken := make(map[string]bool, len(mapped))
	for key, entries := range byKey {
		if len(entries) == 1 {
			taken[key] = true
		}
	}

	for _, entries := range byKey {
		if len(entries) < 2 {
			continue
		}
		for _, entry := range entries {
			candidate := entry.name
			if parentName := strings.TrimSpace(nameByID[parentByID[int(entry.source.ID)]]); parentName != "" {
				candidate = parentName + " / " + entry.name
			}

			key := string(entry.target) + "\x00" + strings.ToLower(candidate)
			if taken[key] {
				candidate = fmt.Sprintf("%s (Uptime Kuma #%d)", entry.name, int(entry.source.ID))
				key = string(entry.target) + "\x00" + strings.ToLower(candidate)
			}

			if candidate != entry.name {
				entry.warnings = append(entry.warnings, fmt.Sprintf(
					"renamed from %q to keep monitor names unique", entry.name))
				entry.name = candidate
			}
			taken[key] = true
		}
	}
}

// attachKumaGroupMembers inverts Kuma's child->parent links into the
// member-name lists Probara's group import expects, and drops groups left with
// no importable members. It returns the surviving entries because dropping an
// empty group can in turn empty its parent, which needs another pass.
func attachKumaGroupMembers(mapped []*kumaMapped) ([]*kumaMapped, []models.ImportSkippedRow) {
	childNames := make(map[int][]string)
	for _, entry := range mapped {
		if parent := int(entry.source.Parent); parent > 0 {
			childNames[parent] = append(childNames[parent], entry.name)
		}
	}

	kept := make([]*kumaMapped, 0, len(mapped))
	skipped := make([]models.ImportSkippedRow, 0)
	for _, entry := range mapped {
		if entry.target != models.MonitorTypeGroup {
			kept = append(kept, entry)
			continue
		}

		members := childNames[int(entry.source.ID)]
		if len(members) == 0 {
			skipped = append(skipped, models.ImportSkippedRow{
				Name:       entry.name,
				SourceType: "group",
				Reason:     "none of the group's monitors could be imported, so the group would have been empty",
			})
			continue
		}

		entry.config["monitor_ids"] = []string{}
		entry.members = members
		kept = append(kept, entry)
	}

	if len(kept) != len(mapped) {
		survivors, more := attachKumaGroupMembers(kept)
		return survivors, append(skipped, more...)
	}
	return kept, skipped
}

// orderKumaMapped puts plain monitors first and then groups deepest-first.
// ExecuteImport creates groups in a second pass that resolves member names as
// it goes, so a group must appear after every group it contains.
func orderKumaMapped(mapped []*kumaMapped, parentByID map[int]int) {
	depth := func(id int) int {
		steps := 0
		for current := parentByID[id]; current > 0 && steps < len(parentByID)+1; current = parentByID[current] {
			steps++
		}
		return steps
	}

	sort.SliceStable(mapped, func(i, j int) bool {
		iGroup := mapped[i].target == models.MonitorTypeGroup
		jGroup := mapped[j].target == models.MonitorTypeGroup
		if iGroup != jGroup {
			return jGroup
		}
		if !iGroup {
			return false
		}
		return depth(int(mapped[i].source.ID)) > depth(int(mapped[j].source.ID))
	})
}

// summarizeKumaTranslation rolls per-monitor warnings up into the handful of
// lines worth showing above the preview table.
func summarizeKumaTranslation(mapped []*kumaMapped, skipped []models.ImportSkippedRow) []string {
	warnings := make([]string, 0, 4)

	if len(skipped) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%d Uptime Kuma monitor(s) could not be imported: %s",
			len(skipped), summarizeSkipReasons(skipped)))
	}

	notifications, renamed, disabled := 0, 0, 0
	for _, entry := range mapped {
		for _, warning := range entry.warnings {
			switch {
			case strings.HasPrefix(warning, "Uptime Kuma notifications were not migrated"):
				notifications++
			case strings.HasPrefix(warning, "renamed from"):
				renamed++
			case strings.HasPrefix(warning, "the database password was not imported"):
				disabled++
			}
		}
	}

	if notifications > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"Uptime Kuma notifications are never migrated: %d monitor(s) reference notifiers that must be replaced with Probara alert channels",
			notifications))
	}
	if renamed > 0 {
		warnings = append(warnings, fmt.Sprintf("%d monitor(s) were renamed to keep names unique", renamed))
	}
	if disabled > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%d database monitor(s) were imported disabled because their password was not carried over",
			disabled))
	}

	return warnings
}

// row renders the mapped monitor as an ImportRow whose fields are already the
// canonical target names, so suggestMapping wires them up without operator
// input and ExecuteImport takes its raw-config path.
func (m *kumaMapped) row(index int) models.ImportRow {
	fields := map[string]interface{}{
		"name":             m.name,
		"type":             string(m.target),
		"config":           m.config,
		"interval_seconds": clampInt(int(m.source.Interval), minKumaInterval, maxKumaInterval),
		"enabled":          m.enabled,
	}

	if timeout := int(m.source.Timeout); timeout > 0 {
		fields["timeout_seconds"] = timeout
	}
	if retries := int(m.source.MaxRetries); retries > 0 {
		fields["consecutive_failures_threshold"] = retries
	}
	if tags := kumaTagNames(m.source.Tags); len(tags) > 0 {
		fields["tags"] = tags
	}
	if len(m.members) > 0 {
		fields["group_members"] = m.members
	}

	return models.ImportRow{Index: index, Fields: fields, Warnings: m.warnings}
}

// kumaTagNames flattens Kuma's tag objects into the plain strings Probara
// stores. A tag with a value becomes "name:value".
func kumaTagNames(tags []kumaTag) []string {
	names := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		name := strings.TrimSpace(tag.Name)
		if name == "" {
			continue
		}
		if value := strings.TrimSpace(tag.Value); value != "" {
			name += ":" + value
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}
