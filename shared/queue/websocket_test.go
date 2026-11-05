package queue

import "testing"

func TestWebsocketPath(t *testing.T) {
	for _, tc := range []struct {
		in, wantURL, wantPath string
	}{
		{"wss://loc:cred@nats.example.com", "wss://loc:cred@nats.example.com", ""},
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
