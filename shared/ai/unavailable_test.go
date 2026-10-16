package ai

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func decideOnce(t *testing.T, baseURL string, timeout time.Duration) error {
	t.Helper()
	a, err := NewAnalyzer(Config{BaseURL: baseURL, APIKey: "test-internal-token", Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.(*localAnalyzer).decide(context.Background(), []Decision{{ID: "d1", Task: "rca", Evidence: MonitorEvidence{}, Choices: choices("rca")}})
	if err == nil {
		t.Fatal("call succeeded")
	}
	return err
}

// A saturated, loading, restarting or unreachable service is ErrUnavailable
// (consumers retry it); anything else stays a terminal error.
func TestLocalAIUnavailableIsTransient(t *testing.T) {
	statuses := []struct {
		code       int
		retryAfter string
		want       bool
		hint       time.Duration
	}{
		{http.StatusTooManyRequests, "7", true, 7 * time.Second},
		{http.StatusServiceUnavailable, "", true, 0},
		{http.StatusBadGateway, "nonsense", true, 0},
		{http.StatusGatewayTimeout, "-3", true, 0},
		{http.StatusUnprocessableEntity, "5", false, 0},
		{http.StatusUnauthorized, "", false, 0},
		{http.StatusInternalServerError, "", false, 0},
	}
	for _, tc := range statuses {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if tc.retryAfter != "" {
				w.Header().Set("Retry-After", tc.retryAfter)
			}
			w.WriteHeader(tc.code)
		}))
		err := decideOnce(t, srv.URL, 5*time.Second)
		srv.Close()
		if got := errors.Is(err, ErrUnavailable); got != tc.want {
			t.Fatalf("HTTP %d: ErrUnavailable=%v, want %v (%v)", tc.code, got, tc.want, err)
		}
		if got := RetryAfterHint(err); got != tc.hint {
			t.Fatalf("HTTP %d: hint %s, want %s", tc.code, got, tc.hint)
		}
	}

	// Connection refused: nothing listens on a port that was just freed.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	if err := decideOnce(t, "http://"+addr, 5*time.Second); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("connection refused: %v", err)
	}

	// A model that outlives the client timeout is slow, not unavailable.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer slow.Close()
	if err := decideOnce(t, slow.URL, 200*time.Millisecond); errors.Is(err, ErrUnavailable) {
		t.Fatalf("client timeout must not be ErrUnavailable: %v", err)
	}

	if RetryAfterHint(errors.New("other")) != 0 {
		t.Fatal("hint on a foreign error")
	}
	if d := parseRetryAfter(time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)); d <= 0 || d > time.Minute {
		t.Fatalf("HTTP-date Retry-After parsed as %s", d)
	}
}
