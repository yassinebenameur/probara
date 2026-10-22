package ai

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrNotConfigured = errors.New("local AI is disabled")

// ErrUnavailable marks a call the local AI service could not take right now:
// it refused or reset the connection, its name did not resolve, or it
// answered 429 (queue full), 502, 503 (model loading, shutting down) or 504.
// A later attempt may succeed, so job consumers retry it instead of
// recording a verdict. A model that ran past the timeout, an auth or
// validation failure, and an invalid response are not ErrUnavailable.
// Match with errors.Is; RetryAfterHint returns the service's Retry-After.
var ErrUnavailable = errors.New("local AI temporarily unavailable")

// unavailableError is an ErrUnavailable with its cause and Retry-After hint.
// The cause stays reachable (errors.As *url.Error still works for readiness
// classification); the text carries no more detail than before.
type unavailableError struct {
	msg        string
	cause      error
	retryAfter time.Duration
}

func (e *unavailableError) Error() string        { return e.msg }
func (e *unavailableError) Unwrap() error        { return e.cause }
func (e *unavailableError) Is(target error) bool { return target == ErrUnavailable }

// RetryAfterHint returns the Retry-After the service sent with an
// ErrUnavailable answer, or 0 when there was none.
func RetryAfterHint(err error) time.Duration {
	var u *unavailableError
	if errors.As(err, &u) {
		return u.retryAfter
	}
	return 0
}

// transientTransport reports whether a transport error (the request's own
// context still live) means the service is unreachable for now. A dial
// failure is, even when it timed out; a request that timed out after
// connecting is a slow model, not an unavailable one, and retrying it would
// spend the inference slot again; a certificate the client rejects will not
// fix itself.
func transientTransport(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return false
	}
	return true
}

// parseRetryAfter reads a Retry-After header: delay-seconds or an HTTP date.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil {
		if n < 0 {
			return 0
		}
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// Config is installation-owned. No tenant can supply an inference destination.
type Config struct {
	Disabled bool
	BaseURL  string
	APIKey   string
	Timeout  time.Duration
}

type Decision struct {
	ID       string            `json:"id"`
	Task     string            `json:"task"`
	Evidence any               `json:"evidence"`
	Choices  map[string]string `json:"choices"`
}
type DecisionResult struct {
	ID            string             `json:"id"`
	Probabilities map[string]float64 `json:"probabilities"`
}
type ModelIdentity struct {
	Engine    string `json:"engine"`
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
	Catalogue string `json:"catalogue"`
}
type BatchResult struct {
	Model      ModelIdentity      `json:"model"`
	Thresholds map[string]float64 `json:"thresholds"`
	Results    []DecisionResult   `json:"results"`
}

type localAnalyzer struct {
	cfg    Config
	client *http.Client
}

func NewAnalyzer(cfg Config) (RootCauseAnalyzer, error) {
	a, err := newLocalAnalyzer(cfg)
	if err != nil {
		// Return a literal nil, not a nil *localAnalyzer: wrapping a typed nil
		// pointer would give callers a non-nil interface on the error path.
		return nil, err
	}
	return a, nil
}

// newLocalAnalyzer is the one place the client is validated and built.
// NewAnalyzer, NewDependencyAdvisor and Probe all go through it, so none of
// them needs to type-assert the interface NewAnalyzer returns.
func newLocalAnalyzer(cfg Config) (*localAnalyzer, error) {
	if cfg.Disabled {
		return nil, ErrNotConfigured
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://127.0.0.1:8090"
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("invalid LOCAL_AI_URL")
	}
	if len(strings.TrimSpace(cfg.APIKey)) < 16 {
		return nil, errors.New("LOCAL_AI_TOKEN must have at least 16 characters when local AI is enabled")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 90 * time.Second
	}
	return &localAnalyzer{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (a *localAnalyzer) call(ctx context.Context, method, path string, body any, out any) error {
	var b bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&b).Encode(body); err != nil {
			return err
		}
	}
	if b.Len() > 512*1024 {
		return errors.New("local AI request exceeds evidence budget")
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.cfg.BaseURL, "/")+path, &b)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		if ctx.Err() == nil && transientTransport(err) {
			return &unavailableError{msg: "local AI unavailable: " + err.Error(), cause: err}
		}
		return fmt.Errorf("local AI unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("local AI service returned HTTP %d; retry after checking service readiness", resp.StatusCode)
		switch resp.StatusCode {
		case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return &unavailableError{msg: msg, retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
		}
		return errors.New(msg)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024+1))
	if err != nil {
		return err
	}
	if len(data) > 512*1024 {
		return errors.New("local AI response exceeds budget")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("invalid local AI response: %w", err)
	}
	if dec.Decode(new(any)) != io.EOF {
		return errors.New("invalid trailing local AI response")
	}
	return nil
}

func (a *localAnalyzer) decide(ctx context.Context, decisions []Decision) (BatchResult, error) {
	if len(decisions) == 0 || len(decisions) > 50 {
		return BatchResult{}, errors.New("decision batch must contain 1 to 50 items")
	}
	var out BatchResult
	if err := a.call(ctx, "POST", "/v1/decisions", struct {
		Decisions []Decision `json:"decisions"`
	}{decisions}, &out); err != nil {
		return out, err
	}
	_, identityErr := hex.DecodeString(out.Model.SHA256)
	if identityErr != nil || out.Model.Engine != "laya-cpu" || out.Model.Version == "" || len(out.Model.SHA256) != 64 || out.Model.Catalogue != CatalogueVersion || len(out.Results) != len(decisions) {
		return out, errors.New("invalid local AI model identity or batch size")
	}
	expected := map[string]Decision{}
	for _, d := range decisions {
		if _, ok := expected[d.ID]; ok {
			return out, errors.New("duplicate decision id")
		}
		expected[d.ID] = d
	}
	for _, r := range out.Results {
		d, ok := expected[r.ID]
		if !ok {
			return out, errors.New("unknown or duplicate response id")
		}
		delete(expected, r.ID)
		if len(r.Probabilities) != len(d.Choices) {
			return out, errors.New("incomplete decision probabilities")
		}
		total := 0.0
		for k, p := range r.Probabilities {
			if _, ok := d.Choices[k]; !ok || math.IsNaN(p) || math.IsInf(p, 0) || p < 0 || p > 1 {
				return out, errors.New("invalid choice probability")
			}
			total += p
		}
		threshold, ok := out.Thresholds[d.Task]
		if !ok || math.IsNaN(threshold) || threshold <= 0 || threshold > 1 || math.Abs(total-1) > 1e-4 {
			return out, errors.New("invalid calibration or probability sum")
		}
	}
	// Preserve request order even if the service returns responses out of order.
	byID := map[string]DecisionResult{}
	for _, r := range out.Results {
		byID[r.ID] = r
	}
	for i, d := range decisions {
		out.Results[i] = byID[d.ID]
	}
	return out, nil
}

func Probe(ctx context.Context, cfg Config) (string, error) {
	analyzer, err := newLocalAnalyzer(cfg)
	if err != nil {
		return "", err
	}
	var out struct {
		Model ModelIdentity `json:"model"`
	}
	if err := analyzer.call(ctx, "GET", "/readyz", nil, &out); err != nil {
		return "", err
	}
	if out.Model.Engine != "laya-cpu" || out.Model.Version == "" || out.Model.Catalogue != CatalogueVersion {
		return "", errors.New("invalid local AI readiness identity")
	}
	return out.Model.Version, nil
}
