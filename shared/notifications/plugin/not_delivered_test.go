package plugin

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/netguard"
)

// A non-2xx answer, a dial that never connected and refused egress all prove
// the provider accepted nothing; a timeout after the request went out does
// not, nor does success.
func TestNotDeliveredClassification(t *testing.T) {
	for _, code := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusNotFound, http.StatusRequestTimeout} {
		err := CheckResponse(&http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("no"))}, "svc")
		if !IsNotDelivered(err) {
			t.Fatalf("status %d: IsNotDelivered = false (%v)", code, err)
		}
	}
	if err := CheckResponse(&http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {"5"}}, Body: http.NoBody}, "svc"); !IsNotDelivered(err) {
		t.Fatal("a 429 with Retry-After must still read as not delivered")
	} else if _, ok := RetryAfterDelay(err); !ok {
		t.Fatal("the Retry-After delay must survive the marking")
	}

	Configure(Runtime{Egress: netguard.Policy{BlockPrivate: false}})
	defer Configure(Runtime{Egress: netguard.Policy{BlockPrivate: true}})
	ctx := context.Background()

	// Nothing listens on a port just closed: the dial fails.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + ln.Addr().String()
	_ = ln.Close()
	if err := Post(ctx, NewHTTPClient(2*time.Second), closed, "text/plain", nil, nil, "svc"); !IsNotDelivered(err) {
		t.Fatalf("refused dial: IsNotDelivered = false (%v)", err)
	}

	// The request reached a server that never answered in time: ambiguous.
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(time.Second) }))
	defer hang.Close()
	if err := Post(ctx, NewHTTPClient(200*time.Millisecond), hang.URL, "text/plain", nil, nil, "svc"); err == nil || IsNotDelivered(err) {
		t.Fatalf("timeout after the request was sent must stay ambiguous, got %v", err)
	}

	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	defer ok.Close()
	if err := Post(ctx, NewHTTPClient(2*time.Second), ok.URL, "text/plain", nil, nil, "svc"); err != nil {
		t.Fatalf("accepted: %v", err)
	}

	Configure(Runtime{Egress: netguard.Policy{BlockPrivate: true}})
	if err := Post(ctx, NewHTTPClient(2*time.Second), ok.URL, "text/plain", nil, nil, "svc"); !IsNotDelivered(err) || !IsPermanent(err) {
		t.Fatalf("refused egress: not delivered %v, permanent %v (%v)", IsNotDelivered(err), IsPermanent(err), err)
	}
}
