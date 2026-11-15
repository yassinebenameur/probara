package queue

import (
	"strings"
	"testing"
)

func TestWebsocketPath(t *testing.T) {
	for _, tc := range []struct {
		in, wantURL, wantPath string
	}{
		{"wss://loc:s3cr3t-value@nats.example.com", "wss://loc:s3cr3t-value@nats.example.com", ""},
		{"wss://nats.example.com/", "wss://nats.example.com/", ""},
		{"wss://loc:s%3Acret@probara.example.com/nats", "wss://loc:s%3Acret@probara.example.com", "/nats"},
		{"ws://127.0.0.1:8443/a/b/", "ws://127.0.0.1:8443", "/a/b"},
		{"tls://nats.example.com:4222/ignored", "tls://nats.example.com:4222/ignored", ""},
		{"nats://a:4222,nats://b:4222", "nats://a:4222,nats://b:4222", ""},
	} {
		gotURL, opts := websocketPath(tc.in)
		if gotURL != tc.wantURL {
			t.Errorf("websocketPath(%q) url = %q, want %q", tc.in, gotURL, tc.wantURL)
		}
		if (len(opts) == 1) != (tc.wantPath != "") {
			t.Errorf("websocketPath(%q) returned %d options, want a ProxyPath option: %v", tc.in, len(opts), tc.wantPath != "")
		}
	}
}

func TestCheckWebsocketCredentials(t *testing.T) {
	for _, tc := range []struct {
		url    string
		reject bool
	}{
		{"ws://loc:s3cr3t-value@nats.example.com:8443", true},
		{"WS://loc:s3cr3t-value@10.0.0.5:8443/nats", true},
		{"nats://a:4222,ws://loc:s3cr3t-value@nats.example.com", true},
		{"ws://loc:s3cr3t-value@127.0.0.1:8443", false},
		{"ws://loc:s3cr3t-value@localhost:8443", false},
		{"ws://loc:s3cr3t-value@[::1]:8443", false},
		{"ws://nats.example.com:8443", false},
		{"wss://loc:s3cr3t-value@nats.example.com", false},
		{"tls://loc:s3cr3t-value@nats.example.com:4222", false},
		{"nats://platform:pw@nats:4222", false},
	} {
		err := checkWebsocketCredentials(tc.url)
		if (err != nil) != tc.reject {
			t.Errorf("checkWebsocketCredentials(%q) = %v, reject %v", tc.url, err, tc.reject)
		}
		if err != nil && strings.Contains(err.Error(), "s3cr3t-value") {
			t.Errorf("error leaks the credential: %v", err)
		}
	}
	if _, err := NewClient("ws://loc:s3cr3t-value@nats.example.com:8443"); err == nil {
		t.Fatal("NewClient accepted credentials over plaintext ws:// to a remote host")
	}
}
