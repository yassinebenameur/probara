package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/yassinebenameur/probara/shared/netguard"
)

// Runtime is the process-level context every plugin shares. Each binary that
// dispatches notifications (api, alerter, worker) calls Configure once at
// startup from shared/config, next to email.SetMailer.
type Runtime struct {
	// AppBaseURL is the public operator-UI origin, e.g.
	// "https://probara.example.com". Empty omits deep links from every
	// channel — a guessed host in a notification is worse than no link.
	AppBaseURL string
	// Egress is the outbound-connection policy applied to every plugin HTTP
	// request. Channel URLs are tenant-supplied, so this is the SSRF boundary.
	Egress netguard.Policy
}

// runtime defaults to blocking private addresses: a binary that forgets
// Configure fails closed rather than letting tenants reach internal services.
var runtime atomic.Pointer[Runtime]

func init() {
	runtime.Store(&Runtime{Egress: netguard.Policy{BlockPrivate: true}})
}

// Configure installs the process-level runtime. Safe to call at any time;
// clients built by NewHTTPClient read the policy on every dial.
func Configure(r Runtime) {
	r.AppBaseURL = strings.TrimRight(strings.TrimSpace(r.AppBaseURL), "/")
	runtime.Store(&r)
}

// CurrentRuntime returns the installed runtime.
func CurrentRuntime() Runtime { return *runtime.Load() }

// NewHTTPClient returns the HTTP client plugins must use for outbound
// requests. It dials through the configured egress policy (checked against
// the resolved IP at connect time) and never follows redirects: a webhook
// that answers 3xx is a failed delivery, not an instruction to POST the alert
// somewhere else.
func NewHTTPClient(timeout time.Duration) *http.Client {
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		d := &netguard.Dialer{Policy: runtime.Load().Egress, Timeout: timeout}
		return d.DialContext(ctx, network, addr)
	}
	transport := &http.Transport{
		// An HTTPS_PROXY deployment keeps working; the guard then vets the
		// proxy's address (allow-list it) and egress policy for the final
		// target is the proxy's job.
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dial,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ParseHTTPSURL parses a channel URL field and requires the https scheme and a
// host. field names the config key in error messages.
func ParseHTTPSURL(raw, field string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%s is required", field)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s is not a valid URL: %w", field, err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("%s must use https", field)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("%s must include a host", field)
	}
	if u.User != nil {
		return nil, errors.New(field + " must not embed credentials")
	}
	return u, nil
}

// HostMatches reports whether host (a URL host, optionally with port) is one
// of domains or a subdomain of one. Matching is on label boundaries, so
// "hooks.slack.com" matches "slack.com" but "slack.com.evil.io" and
// "notslack.com" do not.
func HostMatches(host string, domains ...string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, d := range domains {
		d = strings.ToLower(d)
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// String returns the trimmed string value of a decrypted config field, or ""
// when it is absent or not a string.
func (c ChannelRef) String(key string) string {
	if c.Config == nil {
		return ""
	}
	s, _ := c.Config[key].(string)
	return strings.TrimSpace(s)
}

// PostJSON marshals body, POSTs it to target with client, and classifies the
// response with CheckResponse. headers are added after Content-Type, so a
// plugin may override it. service names the provider in errors.
func PostJSON(ctx context.Context, client *http.Client, target string, body any, headers map[string]string, service string) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return Permanent(fmt.Errorf("marshal %s payload: %w", service, err))
	}
	return Post(ctx, client, target, "application/json", payload, headers, service)
}

// Post sends a pre-encoded body. See PostJSON.
func Post(ctx context.Context, client *http.Client, target, contentType string, body []byte, headers map[string]string, service string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return Permanent(fmt.Errorf("build %s request: %w", service, err))
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "Probara-Alerts/1.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		// *url.Error prints the request URL, and channel URLs are
		// credentials (Slack/Discord webhook paths, Telegram bot tokens).
		// Keep only the cause so a network failure cannot leak one to logs.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		if errors.Is(err, netguard.ErrBlocked) {
			// The egress policy refused the destination; retrying resolves
			// to the same address.
			return Permanent(fmt.Errorf("post %s: %w", service, err))
		}
		return fmt.Errorf("post %s: %w", service, err)
	}
	defer resp.Body.Close()
	return CheckResponse(resp, service)
}
