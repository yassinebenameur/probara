package worker

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yassinebenameur/probara/shared/netguard"
)

// The HTTP checker dials through the shared guard: a loopback target is
// refused before any connection is made, and HTTP_ALLOWED_CIDRS lets it back
// in.
func TestHTTPChecker_SSRFPolicy(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	config := []byte(fmt.Sprintf(`{"url":%q,"method":"GET"}`, server.URL))

	blocked := NewHTTPChecker(1024, true, nil).Check(context.Background(), config, 5)
	if blocked.ErrorMessage == nil || !strings.Contains(*blocked.ErrorMessage, "ssrf_blocked") {
		t.Fatalf("blocked result = %+v, want an ssrf_blocked error", blocked)
	}
	if hits != 0 {
		t.Fatalf("server saw %d requests through a blocking policy, want 0", hits)
	}

	_, loopback, _ := net.ParseCIDR("127.0.0.0/8")
	allowed := NewHTTPChecker(1024, true, []*net.IPNet{loopback}).Check(context.Background(), config, 5)
	if allowed.Status != "success" {
		t.Fatalf("allow-listed result = %+v, want success", allowed)
	}
}

func TestIsSSRFBlocked(t *testing.T) {
	wrapped := fmt.Errorf("dial: %w", fmt.Errorf("%w: ip 10.0.0.1 not allowed", netguard.ErrBlocked))
	flattened := fmt.Errorf("driver: %s", wrapped.Error()) // chain lost, text kept
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"nil":             {nil, false},
		"wrapped":         {wrapped, true},
		"flattened":       {flattened, true},
		"unrelated error": {fmt.Errorf("connection refused"), false},
	} {
		if got := isSSRFBlocked(tc.err); got != tc.want {
			t.Errorf("%s: isSSRFBlocked = %v, want %v", name, got, tc.want)
		}
	}
}
