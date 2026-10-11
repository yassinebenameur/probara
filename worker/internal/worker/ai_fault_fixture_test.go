package worker

import (
	"context"
	"encoding/json"
	"github.com/yassinebenameur/probara/shared/ai"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// Controlled loopback faults supply real checker outputs for the training
// corpus. No injected hidden cause is used as a label by the generator.
func TestAIFaultFixtures(t *testing.T) {
	checker := NewHTTPChecker(1024, false, nil)
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer bad.Close()
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer tls.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(550 * time.Millisecond); w.WriteHeader(200) }))
	defer slow.Close()
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := closed.Addr().String()
	closed.Close()
	// Minimal DNS NXDOMAIN responder preserves the question and returns no data.
	dns, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dns.Close()
	go func() {
		b := make([]byte, 4096)
		for {
			n, a, e := dns.ReadFrom(b)
			if e != nil {
				return
			}
			if n < 12 {
				continue
			}
			reply := append([]byte{}, b[:n]...)
			reply[2] = 0x81
			reply[3] = 0x83
			for i := 6; i < 12; i++ {
				reply[i] = 0
			}
			dns.WriteTo(reply, a)
		}
	}()
	fixtures := map[string]ai.CheckContext{}
	run := func(name, target string) {
		cfg, _ := json.Marshal(map[string]any{"url": target, "max_latency_ms": 500})
		r := checker.Check(context.Background(), cfg, 2)
		c := ai.CheckContext{Status: r.Status, HTTPStatus: r.HTTPStatus, Metrics: r.MetricsData, CheckedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		if r.LatencyMs != nil {
			v := int(*r.LatencyMs)
			c.LatencyMS = &v
		}
		if r.ErrorMessage != nil {
			c.ErrorMessage = *r.ErrorMessage
			c.ErrorCategory = ai.ErrorCategory(*r.ErrorMessage)
		}
		fixtures[name] = c
	}
	run("healthy", good.URL)
	run("protocol_error", bad.URL)
	run("tls_problem", tls.URL)
	run("connection_failure", "http://"+address)
	run("latency_degradation", slow.URL)
	dcfg, _ := json.Marshal(map[string]any{"host": "fixture.invalid", "record_type": "A", "nameserver": dns.LocalAddr().String()})
	r := NewDNSChecker().Check(context.Background(), dcfg, 2)
	c := ai.CheckContext{Status: r.Status, CheckedAt: fixtures["healthy"].CheckedAt}
	if r.ErrorMessage != nil {
		c.ErrorCategory = ai.ErrorCategory(*r.ErrorMessage)
		c.ErrorMessage = *r.ErrorMessage
	}
	fixtures["dns_failure"] = c
	for name, category := range map[string]string{"dns_failure": "dns", "connection_failure": "connection", "tls_problem": "tls", "protocol_error": "protocol"} {
		c := fixtures[name]
		if (c.Status != "failure" && c.Status != "error") || c.ErrorCategory != category {
			t.Fatalf("%s: %+v", name, c)
		}
	}
	if path := os.Getenv("PROBARA_AI_FIXTURES"); path != "" {
		b, err := json.MarshalIndent(fixtures, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0644); err != nil {
			t.Fatal(err)
		}
	}
}
