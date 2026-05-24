package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yassinebenameur/probara/shared/notifications"
	"github.com/yassinebenameur/probara/shared/notifications/plugin"
)

func TestManifest(t *testing.T) {
	m := New().Manifest()
	if m.Type != pluginType {
		t.Errorf("Type = %q", m.Type)
	}
	if !m.HasCapability(plugin.CapabilityRawEvent) {
		t.Error("expected CapabilityRawEvent")
	}
	if len(m.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(m.Fields))
	}
	// url + hmac_secret must be Secret so the encryption layer protects them.
	if !m.Fields[0].Secret || !m.Fields[1].Secret {
		t.Error("url and hmac_secret must both be Secret-typed")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"valid minimal", `{"url":"https://example.com/hook"}`, false},
		{"valid with headers", `{"url":"https://example.com","custom_headers":"{\"X-A\":\"v\"}"}`, false},
		{"missing url", `{}`, true},
		{"http (insecure)", `{"url":"http://example.com"}`, true},
		{"malformed headers", `{"url":"https://example.com","custom_headers":"not json"}`, true},
		{"nested headers", `{"url":"https://example.com","custom_headers":"{\"x\":{\"y\":1}}"}`, true},
	}
	p := New()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.Validate(json.RawMessage(tc.raw))
			if (err != nil) != tc.wantErr {
				t.Errorf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestSend_PostsEventBodyAndHeaders(t *testing.T) {
	var (
		gotBody    []byte
		gotMethod  string
		gotEvtType string
		gotSig     string
		gotCustom  string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotEvtType = r.Header.Get(eventTypeHeader)
		gotSig = r.Header.Get(signatureHeader)
		gotCustom = r.Header.Get("X-Source")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{
			ID: "c-1",
			Config: map[string]any{
				"url":            srv.URL,
				"hmac_secret":    "topsecret",
				"custom_headers": `{"X-Source":"probara"}`,
			},
		},
		Event: sampleEvent(),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q", gotMethod)
	}
	if gotEvtType != "created" {
		t.Errorf("event type header = %q", gotEvtType)
	}
	if gotCustom != "probara" {
		t.Errorf("custom header missing or wrong: %q", gotCustom)
	}

	// Verify HMAC.
	mac := hmac.New(sha256.New, []byte("topsecret"))
	mac.Write(gotBody)
	expect := signaturePrefix + hex.EncodeToString(mac.Sum(nil))
	if gotSig != expect {
		t.Errorf("signature mismatch:\n got  %s\n want %s", gotSig, expect)
	}

	// Body must be the AlertEvent JSON.
	var decoded notifications.AlertEvent
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("body unmarshal: %v", err)
	}
	if decoded.Alert.MonitorName != "API health" {
		t.Errorf("body did not contain expected event payload: %+v", decoded)
	}
}

func TestSend_NoHMAC_OmitsSignatureHeader(t *testing.T) {
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get(signatureHeader)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"url": srv.URL}},
		Event:   sampleEvent(),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotSig != "" {
		t.Errorf("signature header should be absent when no hmac_secret; got %q", gotSig)
	}
}

func TestSend_FailsOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	err := New().Send(context.Background(), plugin.DispatchRequest{
		Channel: plugin.ChannelRef{Config: map[string]any{"url": srv.URL}},
		Event:   sampleEvent(),
	})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected 403 error, got %v", err)
	}
}

func TestComputeSignature_Stable(t *testing.T) {
	body := []byte(`{"foo":"bar"}`)
	if computeSignature("k", body) != computeSignature("k", body) {
		t.Fatal("same inputs must produce same signature")
	}
	if computeSignature("k", body) == computeSignature("k2", body) {
		t.Fatal("different secrets must produce different signatures")
	}
}

func sampleEvent() notifications.AlertEvent {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	return notifications.AlertEvent{
		Type:      "created",
		TenantID:  "tenant-1",
		Timestamp: now,
		Alert: notifications.AlertDetails{
			ID:           "a-1",
			MonitorName:  "API health",
			PolicyName:   "Critical",
			Status:       "active",
			TriggeredAt:  now,
			FailureCount: 3,
		},
	}
}
