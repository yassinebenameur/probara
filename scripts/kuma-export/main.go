// Command kuma-export pulls the monitor list out of a running Uptime Kuma and
// writes it in Kuma's own backup format, which Probara's monitor importer
// understands.
//
// Kuma removed the Settings -> Backup -> Export button in v2.0 and has never
// had a REST API, so its only remaining machine-readable surface is the
// internal socket.io API this tool speaks. On Kuma 1.x the built-in backup
// file works directly and this tool is unnecessary.
//
// Deliberately a dumb transport: every decision about which Probara monitor
// type a Kuma monitor becomes lives in the API's importer, where it is
// covered by tests. The only content-aware behavior here is redaction.
//
//	go run ./scripts/kuma-export -url https://kuma.internal -user admin
//
// The password is read from KUMA_PASSWORD when -pass is omitted. Credentials
// stay on the operator's machine: nothing is persisted and nothing is sent to
// Probara.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

// kumaSecretFields hold nothing but a credential, so they are dropped whole
// unless -include-secrets is passed. Kuma stores them in plaintext and the
// importer never carries a credential into a monitor config, so writing them
// to disk buys nothing. tlsCert and tlsCa are deliberately absent: they are
// public certificates, and only the key is secret.
var kumaSecretFields = []string{
	"basic_auth_pass",
	"oauth_client_secret",
	"tlsKey",
	"radiusPassword",
	"radiusSecret",
	"mqttPassword",
	"pushToken",
}

// kumaSensitiveHeaders mirrors the importer's own rule. Redaction removes the
// credential, not the field, so a monitor's harmless headers survive.
var kumaSensitiveHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"x-api-key":           true,
	"x-auth-token":        true,
}

var kumaSensitiveHeaderMarkers = []string{"token", "secret", "apikey", "api-key", "password"}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "kuma-export: %v\n", err)
		os.Exit(1)
	}
}

type options struct {
	baseURL        string
	username       string
	password       string
	totp           string
	out            string
	includeSecrets bool
	insecure       bool
	timeout        time.Duration
}

func run() error {
	var opts options
	flag.StringVar(&opts.baseURL, "url", "", "base URL of the Uptime Kuma instance (required), e.g. https://kuma.internal")
	flag.StringVar(&opts.username, "user", "", "Uptime Kuma username (required)")
	flag.StringVar(&opts.password, "pass", "", "Uptime Kuma password (falls back to $KUMA_PASSWORD)")
	flag.StringVar(&opts.totp, "totp", "", "current TOTP code, if two-factor auth is enabled")
	flag.StringVar(&opts.out, "out", "kuma-export.json", "file to write; - for stdout")
	flag.BoolVar(&opts.includeSecrets, "include-secrets", false, "keep database connection strings and other credentials in the output")
	flag.BoolVar(&opts.insecure, "insecure", false, "skip TLS certificate verification")
	flag.DurationVar(&opts.timeout, "timeout", 45*time.Second, "overall deadline")
	flag.Parse()

	if strings.TrimSpace(opts.baseURL) == "" {
		return errors.New("-url is required")
	}
	if strings.TrimSpace(opts.username) == "" {
		return errors.New("-user is required")
	}
	if opts.password == "" {
		opts.password = os.Getenv("KUMA_PASSWORD")
	}
	if opts.password == "" {
		return errors.New("no password supplied: pass -pass or set KUMA_PASSWORD")
	}

	socketURL, err := socketIOURL(opts.baseURL)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	bundle, err := fetchBundle(ctx, socketURL, opts)
	if err != nil {
		return err
	}

	encoded, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("encode export: %w", err)
	}
	encoded = append(encoded, '\n')

	if opts.out == "-" {
		_, err = os.Stdout.Write(encoded)
		return err
	}
	// 0600: even redacted, the file describes internal hosts and ports.
	if err := os.WriteFile(opts.out, encoded, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", opts.out, err)
	}

	fmt.Fprintf(os.Stderr, "wrote %d monitor(s) to %s\n", len(bundle.MonitorList), opts.out)
	if !opts.includeSecrets {
		fmt.Fprintln(os.Stderr, "credentials were redacted; monitors that need one are imported disabled")
	}
	return nil
}

// backup is the envelope Kuma's own export writes, and the shape Probara's
// importer detects.
type backup struct {
	Version          string                   `json:"version"`
	NotificationList []map[string]interface{} `json:"notificationList"`
	MonitorList      []map[string]interface{} `json:"monitorList"`
}

// socketIOURL turns the operator-facing base URL into the websocket endpoint,
// preserving any path prefix Kuma is mounted under.
func socketIOURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid -url: %w", err)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("invalid -url %q: no host", raw)
	}

	switch strings.ToLower(parsed.Scheme) {
	case "https", "wss":
		parsed.Scheme = "wss"
	case "http", "ws", "":
		parsed.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported scheme %q in -url", parsed.Scheme)
	}

	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/socket.io/"
	parsed.RawQuery = "EIO=4&transport=websocket"
	return parsed.String(), nil
}

type client struct {
	conn net.Conn
	rw   io.ReadWriter
}

func (c *client) send(payload string) error {
	return wsutil.WriteClientText(c.conn, []byte(payload))
}

// recv returns the next text frame. Websocket-level ping/pong and close frames
// are handled inside wsutil, so only Engine.IO payloads surface here.
func (c *client) recv() (string, error) {
	for {
		data, op, err := wsutil.ReadServerData(c.rw)
		if err != nil {
			return "", err
		}
		if op != ws.OpText {
			continue
		}
		return string(data), nil
	}
}

func fetchBundle(ctx context.Context, socketURL string, opts options) (*backup, error) {
	dialer := ws.Dialer{
		Timeout:   opts.timeout,
		TLSConfig: &tls.Config{InsecureSkipVerify: opts.insecure}, //nolint:gosec // opt-in via -insecure
	}

	conn, br, _, err := dialer.Dial(ctx, socketURL)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", socketURL, err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	// The server may have sent the Engine.IO open packet immediately after the
	// 101, in which case those bytes are buffered in br and must be drained
	// before reading the conn directly.
	var reader io.Reader = conn
	if br != nil {
		reader = br
	}
	c := &client{conn: conn, rw: struct {
		io.Reader
		io.Writer
	}{reader, conn}}

	// Engine.IO handshake: "0{...}" open, then Socket.IO CONNECT to the
	// default namespace, acknowledged with "40{...}".
	open, err := c.recv()
	if err != nil {
		return nil, fmt.Errorf("read Engine.IO open packet: %w", err)
	}
	if len(open) == 0 || open[0] != '0' {
		return nil, fmt.Errorf("unexpected Engine.IO handshake %q", truncate(open))
	}
	if err := c.send("40"); err != nil {
		return nil, fmt.Errorf("send Socket.IO connect: %w", err)
	}

	bundle := &backup{}
	var (
		connected     bool
		loginSent     bool
		monitorsFound bool
	)

	// Kuma pushes monitorList unprompted once the socket is authenticated, so
	// the loop reacts to whatever arrives instead of requesting each payload.
	for {
		packet, err := c.recv()
		if err != nil {
			if monitorsFound {
				// Everything needed already arrived; a truncated tail is fine.
				break
			}
			return nil, fmt.Errorf("read from Kuma: %w", err)
		}
		if packet == "" {
			continue
		}

		switch packet[0] {
		case '2': // Engine.IO ping
			if err := c.send("3"); err != nil {
				return nil, fmt.Errorf("reply to Engine.IO ping: %w", err)
			}
			continue
		case '1':
			return nil, errors.New("Kuma closed the connection")
		case '4':
			// Socket.IO packet; fall through.
		default:
			continue
		}

		if len(packet) < 2 {
			continue
		}

		switch packet[1] {
		case '0': // CONNECT acknowledged
			connected = true

		case '4': // CONNECT_ERROR
			return nil, fmt.Errorf("Kuma refused the socket.io connection: %s", truncate(packet[2:]))

		case '3': // ACK — the reply to our login
			payload, err := ackPayload(packet)
			if err != nil {
				return nil, err
			}
			if err := checkLoginResult(payload); err != nil {
				return nil, err
			}

		case '2': // EVENT
			event, arg, err := eventPayload(packet)
			if err != nil {
				continue
			}
			switch event {
			case "monitorList":
				monitors, err := normalizeMonitorList(arg)
				if err != nil {
					return nil, err
				}
				bundle.MonitorList = monitors
				monitorsFound = true
			case "notificationList":
				bundle.NotificationList = summarizeNotifications(arg)
			case "info":
				bundle.Version = extractVersion(arg)
			case "autoLogin":
				// Auth is disabled on this instance; no login needed.
				loginSent = true
			}
		}

		if connected && !loginSent {
			if err := c.send(loginPacket(opts)); err != nil {
				return nil, fmt.Errorf("send login: %w", err)
			}
			loginSent = true
		}

		// monitorList only arrives on an authenticated socket, so receiving it
		// is the success signal. Drain briefly so a notificationList or info
		// packet queued behind it is not missed.
		if monitorsFound {
			drain(c, bundle)
			break
		}
	}

	if len(bundle.MonitorList) == 0 {
		return nil, errors.New("Kuma returned no monitors")
	}

	if !opts.includeSecrets {
		redactMonitors(bundle.MonitorList)
	}
	return bundle, nil
}

// drain reads whatever is already queued, without blocking the caller for long
// if nothing else is coming.
func drain(c *client, bundle *backup) {
	_ = c.conn.SetReadDeadline(time.Now().Add(750 * time.Millisecond))
	defer func() { _ = c.conn.SetReadDeadline(time.Time{}) }()

	for {
		packet, err := c.recv()
		if err != nil {
			return
		}
		if len(packet) < 2 || packet[0] != '4' || packet[1] != '2' {
			continue
		}
		event, arg, err := eventPayload(packet)
		if err != nil {
			continue
		}
		switch event {
		case "notificationList":
			if len(bundle.NotificationList) == 0 {
				bundle.NotificationList = summarizeNotifications(arg)
			}
		case "info":
			if bundle.Version == "" {
				bundle.Version = extractVersion(arg)
			}
		}
	}
}

func loginPacket(opts options) string {
	credentials := map[string]string{
		"username": opts.username,
		"password": opts.password,
		"token":    opts.totp,
	}
	encoded, _ := json.Marshal([]interface{}{"login", credentials})
	// Ack id 0: Kuma answers login through the callback, not an event.
	return "420" + string(encoded)
}

// eventPayload splits a Socket.IO EVENT packet ("42[...]", optionally with an
// ack id) into the event name and its first argument.
func eventPayload(packet string) (string, json.RawMessage, error) {
	body := strings.TrimLeft(packet[2:], "0123456789")

	var parts []json.RawMessage
	if err := json.Unmarshal([]byte(body), &parts); err != nil || len(parts) == 0 {
		return "", nil, fmt.Errorf("malformed event packet")
	}

	var name string
	if err := json.Unmarshal(parts[0], &name); err != nil {
		return "", nil, fmt.Errorf("malformed event name")
	}
	if len(parts) < 2 {
		return name, nil, nil
	}
	return name, parts[1], nil
}

func ackPayload(packet string) (json.RawMessage, error) {
	body := strings.TrimLeft(packet[2:], "0123456789")

	var parts []json.RawMessage
	if err := json.Unmarshal([]byte(body), &parts); err != nil || len(parts) == 0 {
		return nil, fmt.Errorf("malformed acknowledgement from Kuma")
	}
	return parts[0], nil
}

func checkLoginResult(payload json.RawMessage) error {
	var result struct {
		OK       *bool  `json:"ok"`
		Msg      string `json:"msg"`
		Message  string `json:"message"`
		TokenReq *bool  `json:"tokenRequired"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return fmt.Errorf("could not read the login response: %w", err)
	}

	if result.TokenReq != nil && *result.TokenReq {
		return errors.New("this account has two-factor auth enabled: pass the current code with -totp")
	}
	if result.OK != nil && !*result.OK {
		reason := strings.TrimSpace(result.Msg)
		if reason == "" {
			reason = strings.TrimSpace(result.Message)
		}
		if reason == "" {
			reason = "login rejected"
		}
		return fmt.Errorf("Kuma rejected the login: %s", reason)
	}
	return nil
}

// normalizeMonitorList converts Kuma's id-keyed monitorList object into the
// array its backup file uses, ordered by monitor id so the output is stable.
func normalizeMonitorList(arg json.RawMessage) ([]map[string]interface{}, error) {
	if len(arg) == 0 {
		return nil, errors.New("empty monitorList payload")
	}

	var asArray []map[string]interface{}
	if err := json.Unmarshal(arg, &asArray); err == nil {
		return asArray, nil
	}

	var byID map[string]map[string]interface{}
	if err := json.Unmarshal(arg, &byID); err != nil {
		return nil, fmt.Errorf("could not read monitorList: %w", err)
	}

	keys := make([]string, 0, len(byID))
	for key := range byID {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, errA := strconv.Atoi(keys[i])
		b, errB := strconv.Atoi(keys[j])
		if errA == nil && errB == nil {
			return a < b
		}
		return keys[i] < keys[j]
	})

	monitors := make([]map[string]interface{}, 0, len(byID))
	for _, key := range keys {
		monitors = append(monitors, byID[key])
	}
	return monitors, nil
}

// summarizeNotifications keeps only what the importer reads. A Kuma notifier's
// config holds Slack webhooks and SMTP passwords, and notifications are never
// migrated, so the rest is dropped unconditionally.
func summarizeNotifications(arg json.RawMessage) []map[string]interface{} {
	var notifiers []map[string]interface{}
	if err := json.Unmarshal(arg, &notifiers); err != nil {
		return nil
	}

	summary := make([]map[string]interface{}, 0, len(notifiers))
	for _, notifier := range notifiers {
		entry := map[string]interface{}{}
		if id, ok := notifier["id"]; ok {
			entry["id"] = id
		}
		if name, ok := notifier["name"]; ok {
			entry["name"] = name
		}
		if len(entry) > 0 {
			summary = append(summary, entry)
		}
	}
	return summary
}

func extractVersion(arg json.RawMessage) string {
	var info struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(arg, &info); err != nil {
		return ""
	}
	return info.Version
}

func redactMonitors(monitors []map[string]interface{}) {
	for _, monitor := range monitors {
		for _, field := range kumaSecretFields {
			delete(monitor, field)
		}

		// A connection string is mostly topology. Strip only the password so
		// the importer can still build a host/port/database config and mark
		// the monitor as needing a credential.
		if conn, ok := monitor["databaseConnectionString"].(string); ok && conn != "" {
			monitor["databaseConnectionString"] = stripURLPassword(conn)
		}

		if headers, ok := monitor["headers"].(string); ok && headers != "" {
			monitor["headers"] = stripSensitiveHeaders(headers)
		}
	}
}

// stripURLPassword removes the password from a URI-style connection string,
// leaving the scheme, user, host, port, path and query intact. A string it
// cannot parse is dropped entirely rather than risk leaking it.
func stripURLPassword(conn string) string {
	parsed, err := url.Parse(conn)
	if err != nil || parsed.Host == "" {
		return ""
	}
	if parsed.User != nil {
		if name := parsed.User.Username(); name != "" {
			parsed.User = url.User(name)
		} else {
			parsed.User = nil
		}
	}
	return parsed.String()
}

// stripSensitiveHeaders drops credential-bearing entries from Kuma's
// JSON-encoded headers column, keeping the rest.
func stripSensitiveHeaders(encoded string) string {
	var headers map[string]interface{}
	if err := json.Unmarshal([]byte(encoded), &headers); err != nil {
		// Unparseable: we cannot tell what is in there, so drop it.
		return ""
	}

	for name := range headers {
		if isSensitiveHeader(name) {
			delete(headers, name)
		}
	}
	if len(headers) == 0 {
		return ""
	}

	rewritten, err := json.Marshal(headers)
	if err != nil {
		return ""
	}
	return string(rewritten)
}

func isSensitiveHeader(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if kumaSensitiveHeaders[lower] {
		return true
	}
	for _, marker := range kumaSensitiveHeaderMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func truncate(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 160 {
		return text[:160] + "..."
	}
	return text
}
