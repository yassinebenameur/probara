package statuspage

import (
	"strings"
	"testing"
)

func TestMonitorPresenter_ConfigureExtractsMonitorURLs(t *testing.T) {
	svc := &Service{presenters: newMonitorPresenters()}

	tests := []struct {
		name        string
		monitorType string
		configJSON  []byte
		wantURL     string
	}{
		{name: "http", monitorType: "http", configJSON: []byte(`{"url":"https://example.com"}`), wantURL: "https://example.com"},
		{name: "ping", monitorType: "ping", configJSON: []byte(`{"host":"example.com"}`), wantURL: "example.com"},
		{name: "dns", monitorType: "dns", configJSON: []byte(`{"host":"dns.example.com"}`), wantURL: "dns.example.com"},
		{name: "sip", monitorType: "sip", configJSON: []byte(`{"host":"sip.example.com","port":5061}`), wantURL: "sip.example.com:5061"},
		{name: "sip default port", monitorType: "sip", configJSON: []byte(`{"host":"sip.example.com"}`), wantURL: "sip.example.com:5060"},
		{name: "sip tls default port", monitorType: "sip", configJSON: []byte(`{"host":"sip.example.com","transport":"tls"}`), wantURL: "sip.example.com:5061"},
		{name: "sip tls default port any case", monitorType: "sip", configJSON: []byte(`{"host":"sip.example.com","transport":"TLS","port":0}`), wantURL: "sip.example.com:5061"},
		{name: "sip tls explicit port", monitorType: "sip", configJSON: []byte(`{"host":"sip.example.com","transport":"tls","port":5071}`), wantURL: "sip.example.com:5071"},
		{name: "sip tcp default port", monitorType: "sip", configJSON: []byte(`{"host":"sip.example.com","transport":"tcp"}`), wantURL: "sip.example.com:5060"},
		{name: "sip rfc3263 without port shows the domain", monitorType: "sip", configJSON: []byte(`{"host":"example.com","resolution":"rfc3263","port":0,"transport":""}`), wantURL: "example.com"},
		{name: "sip rfc3263 ip host shows the dialed default port", monitorType: "sip", configJSON: []byte(`{"host":"192.0.2.10","resolution":"rfc3263","port":0}`), wantURL: "192.0.2.10:5060"},
		{name: "sip rfc3263 ip host over tls", monitorType: "sip", configJSON: []byte(`{"host":"192.0.2.10","resolution":"rfc3263","transport":"tls"}`), wantURL: "192.0.2.10:5061"},
		{name: "sip rfc3263 with port", monitorType: "sip", configJSON: []byte(`{"host":"example.com","resolution":"rfc3263","port":5070}`), wantURL: "example.com:5070"},
		{name: "sip resolution a keeps the default port", monitorType: "sip", configJSON: []byte(`{"host":"sip.example.com","resolution":"a"}`), wantURL: "sip.example.com:5060"},
		// A test call shows its signalling host, never whom it dials.
		{name: "sip invite number", monitorType: "sip", configJSON: []byte(`{"host":"sip.example.com","transport":"tls","method":"invite","call":{"number":"+33123456789"}}`), wantURL: "sip.example.com:5061"},
		{name: "sip invite target uri", monitorType: "sip", configJSON: []byte(`{"host":"sip.example.com","method":"invite","call":{"target_uri":"sip:bob@pbx.example.com"}}`), wantURL: "sip.example.com:5060"},
		{name: "tcp", monitorType: "tcp", configJSON: []byte(`{"host":"db.example.com","port":5432}`), wantURL: "db.example.com:5432"},
		{name: "tcp without port", monitorType: "tcp", configJSON: []byte(`{"host":"db.example.com"}`), wantURL: "db.example.com"},
		{name: "agent", monitorType: "agent", configJSON: nil, wantURL: "System Agent"},
		{name: "push", monitorType: "push", configJSON: nil, wantURL: "Push Monitor"},
		{name: "group", monitorType: "group", configJSON: nil, wantURL: "Group Monitor"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			monitor := &MonitorStatus{}
			svc.monitorPresenter(tt.monitorType).Configure(monitor, tt.configJSON)
			if monitor.URL != tt.wantURL {
				t.Fatalf("URL = %q, want %q", monitor.URL, tt.wantURL)
			}
			for _, secret := range []string{"33123456789", "bob@", "pbx.example.com"} {
				if strings.Contains(monitor.URL, secret) {
					t.Fatalf("URL = %q publishes the called party %q", monitor.URL, secret)
				}
			}
		})
	}
}

func TestMonitorPresenter_UnknownTypeFallsBackSafely(t *testing.T) {
	svc := &Service{presenters: newMonitorPresenters()}
	monitor := &MonitorStatus{}

	svc.monitorPresenter("totally_unknown_type").Configure(monitor, []byte(`{"host":"ignored"}`))

	if monitor.URL != "" {
		t.Fatalf("URL = %q, want empty string", monitor.URL)
	}
}
