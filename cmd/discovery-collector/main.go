// discovery-collector runs inside Kubernetes with namespaced read-only RBAC.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/yassinebenameur/probara/shared/discovery"
)

const (
	// scanInterval is the pause between two discovery cycles. The API refuses
	// snapshots closer together than its own floor (one minute) with 429.
	scanInterval = 5 * time.Minute
	// maxPayload matches the API's request body limit for a snapshot.
	maxPayload = 4 << 20
	// maxResponse bounds what is read from a successful platform response.
	maxResponse = 1 << 20
	// maxErrorBody is how much of a refused request's body goes into the error.
	maxErrorBody = 512
	// maxRetryAfter bounds a platform-requested delay, so a bad header cannot
	// park the collector for good.
	maxRetryAfter = time.Hour
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	base, err := platformBase(os.Getenv("PROBARA_URL"))
	if err != nil {
		return err
	}
	id := os.Getenv("PROBARA_DISCOVERY_ID")
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("invalid PROBARA_DISCOVERY_ID")
	}
	tokenPath := os.Getenv("PROBARA_DISCOVERY_TOKEN_FILE")
	if tokenPath == "" {
		tokenPath = "/var/run/probara/token"
	}
	const saDir = "/var/run/secrets/kubernetes.io/serviceaccount/"
	ca, err := os.ReadFile(saDir + "ca.crt")
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return fmt.Errorf("invalid Kubernetes CA")
	}
	kubeClient := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, CheckRedirect: noRedirect}
	platformClient := &http.Client{Timeout: 30 * time.Second, CheckRedirect: noRedirect}
	kube := discovery.Kubernetes{BaseURL: "https://" + net.JoinHostPort(os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")), Client: kubeClient, Token: func() (string, error) { b, e := os.ReadFile(saDir + "token"); return string(b), e }}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	endpoint := base + "/api/v1/discovery-collectors/" + id
	for {
		cycleCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err := cycle(cycleCtx, platformClient, kube, endpoint, tokenPath)
		cancel()
		if err != nil {
			log.Printf("Discovery cycle failed: %v", err)
		} else {
			log.Print("Inventory synchronized")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(nextDelay(err)):
		}
	}
}

// platformBase validates PROBARA_URL: an HTTPS origin only (no path,
// credentials, query or fragment), since the collector token is sent to it.
func platformBase(raw string) (string, error) {
	base := strings.TrimRight(raw, "/")
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("PROBARA_URL must be an HTTPS origin")
	}
	return base, nil
}

// noRedirect makes both clients return a redirect as the response instead of
// following it, so the bearer token never reaches another origin.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// httpError is a non-2xx platform response.
type httpError struct {
	Status     int
	Body       string
	RetryAfter time.Duration // only for 409/429/503 with a usable Retry-After
}

func (e *httpError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("platform returned HTTP %d", e.Status)
	}
	return fmt.Sprintf("platform returned HTTP %d: %s", e.Status, e.Body)
}

// parseRetryAfter reads a Retry-After header (delay-seconds or HTTP-date),
// bounded to (0, maxRetryAfter].
func parseRetryAfter(h string, now time.Time) (time.Duration, bool) {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0, false
	}
	var d time.Duration
	if secs, err := strconv.ParseInt(h, 10, 64); err == nil {
		if secs < 0 {
			return 0, false
		}
		d = time.Duration(min(secs, int64(maxRetryAfter/time.Second))) * time.Second
	} else if at, err := http.ParseTime(h); err == nil {
		d = at.Sub(now)
	} else {
		return 0, false
	}
	return min(max(d, time.Second), maxRetryAfter), true
}

// nextDelay is the wait before the next cycle: the platform's Retry-After
// when it asked for one, else the scan interval.
func nextDelay(err error) time.Duration {
	var he *httpError
	if errors.As(err, &he) && he.RetryAfter > 0 {
		return he.RetryAfter
	}
	return scanInterval
}

// platformRequest sends one authenticated request and returns the body of a
// 2xx response (at most maxResponse bytes). A non-2xx response becomes an
// *httpError carrying up to maxErrorBody bytes of the body and, for 409, 429
// and 503, the requested Retry-After. The API answers 409 with Retry-After
// when a namespace was removed while this cycle scanned it, so the collector
// re-reads its configuration within seconds instead of a full scan interval.
func platformRequest(ctx context.Context, c *http.Client, method, target, token string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		he := &httpError{Status: resp.StatusCode, Body: strings.TrimSpace(string(snippet))}
		switch resp.StatusCode {
		case http.StatusConflict, http.StatusTooManyRequests, http.StatusServiceUnavailable:
			if d, ok := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
				he.RetryAfter = d
			}
		}
		return nil, he
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxResponse))
}

// errPayloadTooLarge is an encoded snapshot above the API's body limit.
var errPayloadTooLarge = errors.New("inventory payload exceeds 4 MiB")

// encodeSnapshot serializes a snapshot, refusing one above the API's limit.
func encodeSnapshot(snapshot discovery.Snapshot) ([]byte, error) {
	b, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	if len(b) > maxPayload {
		return nil, errPayloadTooLarge
	}
	return b, nil
}

// fitSnapshot encodes a snapshot within the upload cap. The cap is part of a
// pending namespace's all-or-nothing rule, like the resource budget in
// Collect: when the full snapshot is too large, the active namespaces are
// kept and each promoted pending namespace is added back in scan order only
// if the payload still fits; one that does not is dropped with its resources
// and reported as too_large. A big new namespace therefore never stops the
// active ones, or a smaller pending one, from syncing. Active namespaces
// alone above the cap still fail the cycle, as any active failure does.
func fitSnapshot(s discovery.Snapshot, active []string) ([]byte, discovery.Snapshot, error) {
	b, err := encodeSnapshot(s)
	if !errors.Is(err, errPayloadTooLarge) {
		return b, s, err
	}
	byNamespace := map[string][]discovery.Resource{}
	for _, r := range s.Resources {
		byNamespace[r.Namespace] = append(byNamespace[r.Namespace], r)
	}
	// Every promoted namespace starts out skipped and loses its entry when it
	// fits, so each encoding below already carries the final skip list and the
	// last successful one is exactly what is uploaded.
	fitted := discovery.Snapshot{Revision: s.Revision, Namespaces: []string{}, Skipped: maps.Clone(s.Skipped), Resources: []discovery.Resource{}}
	var promoted []string
	for _, ns := range s.Namespaces {
		if slices.Contains(active, ns) {
			fitted.Namespaces = append(fitted.Namespaces, ns)
			fitted.Resources = append(fitted.Resources, byNamespace[ns]...)
			continue
		}
		promoted = append(promoted, ns)
		if fitted.Skipped == nil {
			fitted.Skipped = map[string]string{}
		}
		fitted.Skipped[ns] = discovery.SkipTooLarge
	}
	if b, err = encodeSnapshot(fitted); err != nil {
		return nil, s, err
	}
	for _, ns := range promoted {
		candidate := fitted
		candidate.Namespaces = append(slices.Clip(fitted.Namespaces), ns)
		candidate.Resources = append(slices.Clip(fitted.Resources), byNamespace[ns]...)
		candidate.Skipped = maps.Clone(fitted.Skipped)
		delete(candidate.Skipped, ns)
		if len(candidate.Skipped) == 0 {
			candidate.Skipped = nil
		}
		encoded, err := encodeSnapshot(candidate)
		if errors.Is(err, errPayloadTooLarge) {
			continue
		}
		if err != nil {
			return nil, s, err
		}
		fitted, b = candidate, encoded
	}
	return b, fitted, nil
}

func cycle(ctx context.Context, c *http.Client, k discovery.Kubernetes, endpoint, tokenPath string) error {
	raw, err := os.ReadFile(tokenPath)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(string(raw))
	b, err := platformRequest(ctx, c, http.MethodGet, endpoint+"/config", token, nil)
	if err != nil {
		return err
	}
	var cfg discovery.Config
	if err = json.Unmarshal(b, &cfg); err != nil {
		return err
	}
	snapshot, err := k.Collect(ctx, cfg)
	if err != nil {
		return err
	}
	b, snapshot, err = fitSnapshot(snapshot, cfg.Namespaces)
	if err != nil {
		return err
	}
	logSkipped(snapshot.Skipped)
	_, err = platformRequest(ctx, c, http.MethodPost, endpoint+"/snapshot", token, b)
	return err
}

// skipHints explains each skip reason in the collector log, where the
// operator applying an access manifest looks first.
var skipHints = map[string]string{
	discovery.SkipForbidden: "cannot list Services/Ingresses there: the namespace does not exist or the access manifest is not applied",
	discovery.SkipTooLarge:  "the namespace exceeds the per-scan limits (a 4 MiB list page, 2,000 resources or the 4 MiB upload)",
	discovery.SkipDeadline:  "not scanned within this cycle's time budget",
	discovery.SkipError:     "the Kubernetes API refused or failed the listing",
}

// logSkipped reports, in a stable order, each pending namespace left out of
// this snapshot. It stays pending and is retried every cycle.
func logSkipped(skipped map[string]string) {
	names := make([]string, 0, len(skipped))
	for ns := range skipped {
		names = append(names, ns)
	}
	sort.Strings(names)
	for _, ns := range names {
		hint := skipHints[skipped[ns]]
		if hint == "" {
			hint = skipped[ns]
		}
		log.Printf("Pending namespace %s skipped (%s): %s", ns, skipped[ns], hint)
	}
}
