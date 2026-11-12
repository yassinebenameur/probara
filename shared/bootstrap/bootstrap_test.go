package bootstrap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yassinebenameur/probara/shared/logger"
	"github.com/yassinebenameur/probara/shared/metrics"
	"github.com/yassinebenameur/probara/shared/queue"
)

func get(handler http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// Liveness must fail once the NATS connection is permanently closed so
// Kubernetes restarts the pod instead of leaving a zombie that can never
// consume jobs again. A connection that is still reconnecting is healthy.
func TestHealthzFailsOnlyOnPermanentlyClosedQueue(t *testing.T) {
	client, err := queue.NewClient("nats://127.0.0.1:59999")
	if err != nil {
		t.Fatalf("queue.NewClient: %v", err)
	}
	handler := Healthz(client)

	if rec := get(handler, "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("healthz while reconnecting = %d, want %d", rec.Code, http.StatusOK)
	}
	client.Close()
	if rec := get(handler, "/healthz"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("healthz after permanent close = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

// NATS is optional for some services: a nil client must not fail liveness.
func TestHealthzWithoutQueueClient(t *testing.T) {
	if rec := get(Healthz(nil), "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("healthz with nil NATS client = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestReadyzRunsChecksInOrderAndNamesTheFirstFailure(t *testing.T) {
	log := logger.New("bootstrap-test", "error")
	var ran []string
	check := func(name string, err error) ReadyCheck {
		return ReadyCheck{Name: name, Check: func(context.Context) error {
			ran = append(ran, name)
			return err
		}}
	}

	ok := get(Readyz(log, check("Database", nil), check("NATS", nil)), "/readyz")
	if ok.Code != http.StatusOK || ok.Body.String() != "OK" {
		t.Fatalf("all checks passing = %d %q, want 200 OK", ok.Code, ok.Body.String())
	}

	ran = nil
	failed := get(Readyz(log, check("Database", errors.New("down")), check("NATS", nil)), "/readyz")
	if failed.Code != http.StatusServiceUnavailable || failed.Body.String() != "Database unavailable" {
		t.Fatalf("failing check = %d %q, want 503 %q", failed.Code, failed.Body.String(), "Database unavailable")
	}
	if len(ran) != 1 {
		t.Fatalf("checks run after a failure = %v, want only the failing one", ran)
	}

	if none := get(Readyz(log), "/readyz"); none.Code != http.StatusOK {
		t.Fatalf("no checks = %d, want 200", none.Code)
	}
}

// A probe the kubelet gave up on must stop its checks.
func TestReadyzChecksSeeTheRequestContext(t *testing.T) {
	log := logger.New("bootstrap-test", "error")
	handler := Readyz(log, ReadyCheck{Name: "Database", Check: func(ctx context.Context) error { return ctx.Err() }})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz on a cancelled probe = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestOpsMuxServesAllThreeEndpoints(t *testing.T) {
	log := logger.New("bootstrap-test", "error")
	mux := OpsMux(metrics.NewRegistry("bootstrap-test"), Healthz(nil), Readyz(log))
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		if rec := get(mux, path); rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
	}
}

func TestPlatformEncryptor(t *testing.T) {
	t.Setenv("PROBARA_SECRETS_KEY", "")
	enc, configured, err := PlatformEncryptor()
	if err != nil || configured || enc == nil {
		t.Fatalf("no key: enc=%v configured=%v err=%v, want a no-op encryptor and no error", enc, configured, err)
	}

	t.Setenv("PROBARA_SECRETS_KEY", "not-a-valid-key")
	if _, _, err := PlatformEncryptor(); err == nil {
		t.Fatal("invalid key: want an error so the service refuses to start")
	}

	// 32 zero bytes, base64.
	t.Setenv("PROBARA_SECRETS_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	enc, configured, err = PlatformEncryptor()
	if err != nil || !configured || enc == nil {
		t.Fatalf("valid key: enc=%v configured=%v err=%v, want a configured encryptor", enc, configured, err)
	}
}
