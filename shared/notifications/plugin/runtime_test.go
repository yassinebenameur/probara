package plugin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/netguard"
)

func TestHostMatches(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"hooks.slack.com", true},
		{"slack.com", true},
		{"HOOKS.SLACK.COM", true},
		{"hooks.slack.com:443", true},
		{"hooks.slack.com.", true},
		{"notslack.com", false},
		{"slack.com.evil.io", false},
		{"evil.io", false},
	}
	for _, tc := range cases {
		if got := HostMatches(tc.host, "slack.com"); got != tc.want {
			t.Errorf("HostMatches(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestParseHTTPSURL(t *testing.T) {
	for raw, wantErr := range map[string]bool{
		"https://example.com/x":       false,
		"http://example.com/x":        true,
		"":                            true,
		"https://":                    true,
		"https://user:pw@example.com": true,
		"::not a url":                 true,
	} {
		if _, err := ParseHTTPSURL(raw, "url"); (err != nil) != wantErr {
			t.Errorf("ParseHTTPSURL(%q) err = %v, wantErr %v", raw, err, wantErr)
		}
	}
}

func response(status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: header}
}

func TestCheckResponseClassification(t *testing.T) {
	if err := CheckResponse(response(204, "", nil), "svc"); err != nil {
		t.Fatalf("2xx must be nil, got %v", err)
	}

	err := CheckResponse(response(400, `{"message":"Invalid routing key"}`, nil), "pagerduty")
	if !IsPermanent(err) || !strings.Contains(err.Error(), "Invalid routing key") {
		t.Fatalf("400: err = %v, want permanent with provider reason", err)
	}

	err = CheckResponse(response(503, "", nil), "svc")
	if err == nil || IsPermanent(err) {
		t.Fatalf("503: err = %v, want transient", err)
	}

	err = CheckResponse(response(429, "", http.Header{"Retry-After": []string{"30"}}), "svc")
	if d, ok := RetryAfterDelay(err); !ok || d != 30*time.Second || IsPermanent(err) {
		t.Fatalf("429: err = %v delay = %v ok = %v, want transient with 30s", err, d, ok)
	}

	err = CheckResponse(response(429, "", http.Header{"Retry-After": []string{"86400"}}), "svc")
	if d, _ := RetryAfterDelay(err); d != maxRetryAfter {
		t.Fatalf("Retry-After must be capped, got %v", d)
	}

	err = CheckResponse(response(302, "", nil), "svc")
	if !IsPermanent(err) {
		t.Fatalf("3xx: err = %v, want permanent (redirects are refused)", err)
	}
}

func TestPermanentAndRetryAfterUnwrap(t *testing.T) {
	base := errors.New("boom")
	if !errors.Is(Permanent(base), base) || !errors.Is(RetryAfter(base, time.Second), base) {
		t.Fatal("wrappers must unwrap to the cause")
	}
	if Permanent(nil) != nil || RetryAfter(nil, time.Second) != nil {
		t.Fatal("wrapping nil must stay nil")
	}
}

func TestDefaultRuntimeBlocksPrivateEgress(t *testing.T) {
	if !CurrentRuntime().Egress.BlockPrivate {
		t.Fatal("a binary that never calls Configure must fail closed")
	}
}

func TestPostErrorsDoNotLeakTheTargetURL(t *testing.T) {
	Configure(Runtime{Egress: netguard.Policy{BlockPrivate: true}})
	defer Configure(Runtime{Egress: netguard.Policy{BlockPrivate: true}})

	secretURL := "https://127.0.0.1:1/bot123456:SECRET-TOKEN/sendMessage"
	err := PostJSON(context.Background(), NewHTTPClient(time.Second), secretURL, map[string]string{}, nil, "telegram")
	if err == nil || strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Fatalf("err = %v, must not contain the URL", err)
	}
	if !IsPermanent(err) {
		t.Fatalf("a policy refusal must be permanent, got %v", err)
	}
}
