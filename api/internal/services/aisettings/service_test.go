package aisettings

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/yassinebenameur/probara/shared/ai"
)

const testToken = "0123456789abcdef-token"

// newTestService builds a service with a controllable probe and clock and no
// database (the readiness path never touches it).
func newTestService(cfg ai.Config, probe func(context.Context, ai.Config) (string, error)) (*Service, *time.Time) {
	s := NewService(nil, cfg)
	s.log = logrus.New()
	now := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return now }
	if probe != nil {
		s.probe = probe
	}
	return s, &now
}

func TestReadinessConcurrentGetsShareOneProbe(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	s, _ := newTestService(ai.Config{BaseURL: "http://127.0.0.1:8090", APIKey: testToken}, func(ctx context.Context, _ ai.Config) (string, error) {
		calls.Add(1)
		<-release
		return "laya-1", nil
	})

	const n = 32
	var wg sync.WaitGroup
	results := make([]TestResult, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = s.readiness(context.Background(), false)
		}(i)
	}
	// Let every goroutine reach the in-flight probe before it completes.
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("probe calls = %d, want 1 for %d concurrent GETs", got, n)
	}
	for i, r := range results {
		if !r.OK || r.Model != "laya-1" {
			t.Fatalf("result %d = %+v", i, r)
		}
	}
	// Within the TTL the cached result is served without probing.
	for i := 0; i < 5; i++ {
		if r := s.readiness(context.Background(), false); !r.OK {
			t.Fatalf("cached result %+v", r)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("probe calls within TTL = %d, want 1", got)
	}
}

func TestReadinessExpiresAfterTTL(t *testing.T) {
	var calls atomic.Int32
	s, now := newTestService(ai.Config{BaseURL: "http://127.0.0.1:8090", APIKey: testToken}, func(context.Context, ai.Config) (string, error) {
		calls.Add(1)
		return "laya-1", nil
	})
	s.readiness(context.Background(), false)
	*now = now.Add(readinessTTL - time.Second)
	s.readiness(context.Background(), false)
	if got := calls.Load(); got != 1 {
		t.Fatalf("probe calls before expiry = %d, want 1", got)
	}
	*now = now.Add(2 * time.Second)
	s.readiness(context.Background(), false)
	if got := calls.Load(); got != 2 {
		t.Fatalf("probe calls after expiry = %d, want 2", got)
	}
}

func TestTestBypassesAndRefreshesCache(t *testing.T) {
	var calls atomic.Int32
	var down atomic.Bool
	down.Store(true)
	s, _ := newTestService(ai.Config{BaseURL: "http://127.0.0.1:8090", APIKey: testToken}, func(context.Context, ai.Config) (string, error) {
		calls.Add(1)
		if down.Load() {
			return "", fmt.Errorf("local AI unavailable: %w", &url.Error{Op: "Get", URL: "http://10.0.0.7:8090/readyz", Err: errors.New("connection refused")})
		}
		return "laya-2", nil
	})

	if r := s.readiness(context.Background(), false); r.OK || r.Message != MessageUnreachable {
		t.Fatalf("first GET = %+v", r)
	}
	down.Store(false)
	// The failure is cached, so a GET within the TTL still reports it...
	if r := s.readiness(context.Background(), false); r.OK {
		t.Fatalf("cached GET = %+v", r)
	}
	// ...until the explicit test probes afresh and stores the new result.
	if r := s.Test(context.Background(), uuid.Nil, TestRequest{}); !r.OK || r.Model != "laya-2" {
		t.Fatalf("Test = %+v", r)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("probe calls = %d, want 2", got)
	}
	if r := s.readiness(context.Background(), false); !r.OK || r.Model != "laya-2" {
		t.Fatalf("GET after Test = %+v", r)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("GET after Test probed again: calls = %d", got)
	}
}

func TestOlderProbeCannotOverwriteNewerResult(t *testing.T) {
	slowRelease := make(chan struct{})
	var calls atomic.Int32
	s, _ := newTestService(ai.Config{BaseURL: "http://127.0.0.1:8090", APIKey: testToken}, func(context.Context, ai.Config) (string, error) {
		if calls.Add(1) == 1 {
			<-slowRelease // the GET probe started before the fix
			return "", errors.New("local AI service returned HTTP 503")
		}
		return "laya-3", nil
	})
	done := make(chan TestResult)
	go func() { done <- s.readiness(context.Background(), false) }()
	for calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	if r := s.Test(context.Background(), uuid.Nil, TestRequest{}); !r.OK {
		t.Fatalf("Test = %+v", r)
	}
	close(slowRelease)
	if r := <-done; r.OK || r.Message != MessageNotReady {
		t.Fatalf("slow GET = %+v", r)
	}
	if r := s.readiness(context.Background(), false); !r.OK || r.Model != "laya-3" {
		t.Fatalf("stale probe overwrote newer result: %+v", r)
	}
}

func TestForcedProbesCoalesce(t *testing.T) {
	var calls, running, maxRunning atomic.Int32
	release := make(chan struct{}, 8)
	s, _ := newTestService(ai.Config{BaseURL: "http://127.0.0.1:8090", APIKey: testToken}, func(context.Context, ai.Config) (string, error) {
		n := calls.Add(1)
		if r := running.Add(1); r > maxRunning.Load() {
			maxRunning.Store(r)
		}
		defer running.Add(-1)
		<-release
		return fmt.Sprintf("laya-%d", n), nil
	})

	// First Test starts probe 1 and blocks on it.
	first := make(chan TestResult)
	go func() { first <- s.Test(context.Background(), uuid.Nil, TestRequest{}) }()
	for calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}

	// A burst of Tests during probe 1, half of them aborted at once (the
	// abuse pattern), must queue exactly one follow-up probe.
	const n = 20
	var wg sync.WaitGroup
	results := make([]TestResult, n)
	for i := 0; i < n; i++ {
		ctx := context.Background()
		if i%2 == 1 {
			c, cancel := context.WithCancel(ctx)
			cancel()
			ctx = c
		}
		wg.Add(1)
		go func(i int, ctx context.Context) {
			defer wg.Done()
			results[i] = s.Test(ctx, uuid.Nil, TestRequest{})
		}(i, ctx)
	}
	// Wait until every caller has registered (aborted ones return at once).
	for {
		s.mu.Lock()
		queued := s.forceNext != nil
		s.mu.Unlock()
		if queued {
			break
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("probe calls while first forced probe runs = %d, want 1", got)
	}

	release <- struct{}{}
	if r := <-first; !r.OK || r.Model != "laya-1" {
		t.Fatalf("first Test = %+v", r)
	}
	release <- struct{}{}
	wg.Wait()
	for i, r := range results {
		if i%2 == 1 {
			continue
		}
		// Joiners arrived after probe 1 started, so they get probe 2's result.
		if !r.OK || r.Model != "laya-2" {
			t.Fatalf("Test %d = %+v, want the follow-up probe's result", i, r)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("probe calls = %d, want 2 (one running, one coalesced)", got)
	}
	if got := maxRunning.Load(); got != 1 {
		t.Fatalf("concurrent forced probes = %d, want 1", got)
	}
}

func TestReadinessCallerCancellationDoesNotPoisonSharedProbe(t *testing.T) {
	release := make(chan struct{})
	var probeCtxErr error
	s, _ := newTestService(ai.Config{BaseURL: "http://127.0.0.1:8090", APIKey: testToken}, func(ctx context.Context, _ ai.Config) (string, error) {
		<-release
		probeCtxErr = ctx.Err()
		return "laya-1", nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan TestResult)
	go func() { first <- s.readiness(ctx, false) }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	if r := <-first; r.OK {
		t.Fatalf("cancelled caller got %+v", r)
	}
	second := make(chan TestResult)
	go func() { second <- s.readiness(context.Background(), false) }()
	time.Sleep(10 * time.Millisecond)
	close(release)
	if r := <-second; !r.OK {
		t.Fatalf("joined probe = %+v", r)
	}
	if probeCtxErr != nil {
		t.Fatalf("probe context inherited caller cancellation: %v", probeCtxErr)
	}
}

// assertNoAddress fails when a user-facing message leaks a host or port.
func assertNoAddress(t *testing.T, msg string, addrs ...string) {
	t.Helper()
	for _, a := range addrs {
		host, port, _ := net.SplitHostPort(a)
		for _, needle := range []string{a, host, ":" + port} {
			if needle != "" && needle != ":" && strings.Contains(msg, needle) {
				t.Fatalf("message %q leaks %q", msg, needle)
			}
		}
	}
	for _, needle := range []string{"http://", "https://", "dial", "tcp"} {
		if strings.Contains(msg, needle) {
			t.Fatalf("message %q leaks transport detail %q", msg, needle)
		}
	}
}

func TestProbeErrorsAreSanitized(t *testing.T) {
	// Unreachable: a closed port on loopback yields a *url.Error naming it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := ln.Addr().String()
	_ = ln.Close()

	// Not ready: the service answers, but not with HTTP 200.
	notReady := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "warming up", http.StatusServiceUnavailable)
	}))
	defer notReady.Close()
	notReadyAddr := strings.TrimPrefix(notReady.URL, "http://")

	// Not ready: wrong readiness identity.
	badIdentity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"model":{"engine":"other","version":"1","sha256":"","catalogue":"x"}}`))
	}))
	defer badIdentity.Close()
	badIdentityAddr := strings.TrimPrefix(badIdentity.URL, "http://")

	cases := []struct {
		name string
		cfg  ai.Config
		want string
		addr string
	}{
		{"unreachable", ai.Config{BaseURL: "http://" + closedAddr, APIKey: testToken}, MessageUnreachable, closedAddr},
		{"http error", ai.Config{BaseURL: notReady.URL, APIKey: testToken}, MessageNotReady, notReadyAddr},
		{"bad identity", ai.Config{BaseURL: badIdentity.URL, APIKey: testToken}, MessageNotReady, badIdentityAddr},
		{"bad url", ai.Config{BaseURL: "ftp://10.1.2.3:9999", APIKey: testToken}, MessageMisconfigured, "10.1.2.3:9999"},
		{"short token", ai.Config{BaseURL: "http://10.1.2.3:9999", APIKey: "short"}, MessageMisconfigured, "10.1.2.3:9999"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestService(tc.cfg, nil) // real ai.Probe
			r := s.Test(context.Background(), uuid.Nil, TestRequest{})
			if r.OK || r.Message != tc.want {
				t.Fatalf("Test = %+v, want message %q", r, tc.want)
			}
			assertNoAddress(t, r.Message, tc.addr)
		})
	}

	t.Run("timeout", func(t *testing.T) {
		s, _ := newTestService(ai.Config{BaseURL: "http://10.9.8.7:8090", APIKey: testToken}, func(ctx context.Context, cfg ai.Config) (string, error) {
			return "", fmt.Errorf("local AI unavailable: %w", &url.Error{Op: "Get", URL: cfg.BaseURL + "/readyz", Err: context.DeadlineExceeded})
		})
		r := s.Test(context.Background(), uuid.Nil, TestRequest{})
		if r.Message != MessageUnreachable {
			t.Fatalf("Test = %+v", r)
		}
		assertNoAddress(t, r.Message, "10.9.8.7:8090")
	})

	t.Run("installation disabled", func(t *testing.T) {
		s, _ := newTestService(ai.Config{Disabled: true}, func(context.Context, ai.Config) (string, error) {
			t.Fatal("disabled installation must not probe")
			return "", nil
		})
		if r := s.Test(context.Background(), uuid.Nil, TestRequest{}); r.OK || r.Message != MessageDisabled {
			t.Fatalf("Test = %+v", r)
		}
	})
}

func TestUpdateRequestValidateRanges(t *testing.T) {
	i := func(v int) *int { return &v }
	b := func(v bool) *bool { return &v }
	for name, c := range map[string]struct {
		req  UpdateRequest
		want string
	}{
		"empty":            {UpdateRequest{}, "at least one setting is required"},
		"enabled only":     {UpdateRequest{Enabled: b(false)}, ""},
		"auto only":        {UpdateRequest{AutoTriageEnabled: b(true)}, ""},
		"delay low":        {UpdateRequest{TriageDelaySeconds: i(-1)}, "triage_delay_seconds must be between 0 and 3600"},
		"delay edges":      {UpdateRequest{TriageDelaySeconds: i(0)}, ""},
		"delay max":        {UpdateRequest{TriageDelaySeconds: i(3600)}, ""},
		"delay high":       {UpdateRequest{TriageDelaySeconds: i(3601)}, "triage_delay_seconds must be between 0 and 3600"},
		"cooldown low":     {UpdateRequest{TriageCooldownSeconds: i(-1)}, "triage_cooldown_seconds must be between 0 and 86400"},
		"cooldown zero":    {UpdateRequest{TriageCooldownSeconds: i(0)}, ""},
		"cooldown max":     {UpdateRequest{TriageCooldownSeconds: i(86400)}, ""},
		"cooldown high":    {UpdateRequest{TriageCooldownSeconds: i(86401)}, "triage_cooldown_seconds must be between 0 and 86400"},
		"max per hour 0":   {UpdateRequest{TriageMaxPerHour: i(0)}, "triage_max_per_hour must be between 1 and 600"},
		"max per hour 1":   {UpdateRequest{TriageMaxPerHour: i(1)}, ""},
		"max per hour 600": {UpdateRequest{TriageMaxPerHour: i(600)}, ""},
		"max per hour 601": {UpdateRequest{TriageMaxPerHour: i(601)}, "triage_max_per_hour must be between 1 and 600"},
	} {
		err := c.req.Validate()
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Msg != c.want {
			t.Errorf("%s: got %v, want %q", name, err, c.want)
		}
	}
}
