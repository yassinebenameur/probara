package worker

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// The INVITE client against SIPp, a third-party SIP implementation, next to
// the scripted fakeUAS in sip_invite_test.go: the fake checks what the
// client sends, SIPp checks that an independent UAS accepts it as a
// complete, well-formed call. Each scenario in testdata/sipp/ ends with the
// messages the client owes (2xx ACK, BYE, ACK of the 487) as mandatory
// receives, and SIPp runs with -m 1, so its exit code says whether the call
// completed on its side.
//
// Unlike the Postgres-backed integration tests (testutil.SetupPostgresDB,
// which fail without Docker), this one skips when no container provider is
// reachable: it is not a database test and has no DSN fallback. The image is
// scripts/sipp/Dockerfile, kept between runs so only the first build pays
// for apt. SIPp's Debian build has no TLS, so TLS stays with the fake UAS.

const (
	sippImageRepo = "probara-sipp-test"
	sippImageTag  = "3.6.1-1"
	sippPassword  = "probara-secret" // uas_digest.xml's verifyauth password
)

type sippUAS struct {
	container testcontainers.Container
	host      string
	port      int
	transport string
}

// startSIPpUAS runs one SIPp UAS call of scenario (a testdata/sipp file)
// over transport (udp or tcp).
func startSIPpUAS(t *testing.T, scenario, transport string) *sippUAS {
	t.Helper()
	ctx := context.Background()

	sippTransport, port := "u1", "5060/udp"
	if transport == "tcp" {
		sippTransport, port = "t1", "5060/tcp"
	}
	scenarioPath, err := filepath.Abs(filepath.Join("testdata", "sipp", scenario))
	if err != nil {
		t.Fatal(err)
	}

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:   filepath.Join("..", "..", "..", "scripts", "sipp"),
			Repo:      sippImageRepo,
			Tag:       sippImageTag,
			KeepImage: true,
		},
		ExposedPorts: []string{port},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      scenarioPath,
			ContainerFilePath: "/sipp/scenario.xml",
			FileMode:          0o644,
		}},
		Cmd: []string{
			"-sf", "/sipp/scenario.xml",
			"-p", "5060",
			"-t", sippTransport,
			"-m", "1",
			// A probe that never finishes the call fails SIPp instead of
			// leaving it waiting.
			"-timeout", "30", "-timeout_error",
			"-nostdin",
			"-trace_err", "-error_file", "/sipp/errors.log",
			"-trace_msg", "-message_file", "/sipp/messages.log",
		},
		// 5060 is 13C4 in /proc/net: ready once SIPp has bound its socket,
		// for UDP as well as TCP.
		WaitingFor: wait.ForExec([]string{"sh", "-c", "grep -qi ':13C4 ' /proc/net/udp /proc/net/tcp /proc/net/udp6 /proc/net/tcp6"}).
			WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("start SIPp: %v", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	mapped, err := c.MappedPort(ctx, port)
	if err != nil {
		t.Fatal(err)
	}
	s := &sippUAS{container: c, host: host, port: int(mapped.Num()), transport: transport}
	t.Cleanup(func() {
		if t.Failed() {
			s.dumpLogs(t)
		}
	})
	return s
}

func (s *sippUAS) config(call map[string]any, extra map[string]any) map[string]any {
	cfg := map[string]any{
		"host": s.host, "port": s.port, "transport": s.transport, "method": "invite", "call": call,
	}
	for k, v := range extra {
		cfg[k] = v
	}
	return cfg
}

// requireCallCompleted waits for SIPp to exit after its one call and fails
// unless that call reached the end of the scenario.
func (s *sippUAS) requireCallCompleted(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(40 * time.Second)
	for {
		state, err := s.container.State(ctx)
		if err != nil {
			t.Fatalf("SIPp state: %v", err)
		}
		if !state.Running {
			if state.ExitCode != 0 {
				t.Fatalf("SIPp exited %d: its call did not complete the scenario", state.ExitCode)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("SIPp is still running: its call never completed")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *sippUAS) dumpLogs(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"/sipp/errors.log", "/sipp/messages.log"} {
		r, err := s.container.CopyFileFromContainer(ctx, name)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(r)
		r.Close()
		t.Logf("SIPp %s:\n%s", name, body)
	}
}

func TestSIPInvite_SIPpUAS_Integration(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)

	// 100 → 180 → 200 + SDP; SIPp requires the 2xx ACK (CSeq 1 ACK) and
	// the BYE (CSeq 2 BYE) after the hold, over each transport it speaks.
	for _, transport := range []string{"udp", "tcp"} {
		t.Run("answer/"+transport, func(t *testing.T) {
			uas := startSIPpUAS(t, "uas_answer.xml", transport)
			result, m := runInvite(t, newInviteTestChecker(), uas.config(map[string]any{"number": "1000", "hold_ms": 300}, nil), 10)
			requireStatus(t, result, "success")
			requireHTTPStatus(t, result, 200)
			call := m.Call
			if call == nil || !call.Answered || call.Teardown != "bye_ok" || call.TeardownStatus != 200 {
				t.Fatalf("call = %+v, want answered and bye_ok", call)
			}
			if call.PDDSource != "180" || len(call.Provisional) != 2 || call.Provisional[0].Code != 100 {
				t.Fatalf("pdd_source = %q provisional = %+v", call.PDDSource, call.Provisional)
			}
			if call.SDPAnswer == nil || call.SDPAnswer.Codec != "PCMU/8000" {
				t.Fatalf("sdp_answer = %+v", call.SDPAnswer)
			}
			// SIPp's Contact is its container address; the ACK and BYE it
			// required went over the dialed flow regardless (D7).
			if call.AnsweredBy == nil || call.AnsweredBy.ContactHost == "" {
				t.Fatalf("answered_by = %+v", call.AnsweredBy)
			}
			uas.requireCallCompleted(t)
		})
	}

	// SIPp's verifyauth checks the digest over the retried INVITE; the 2xx
	// ACK must carry the same Authorization (D22).
	t.Run("digest", func(t *testing.T) {
		uas := startSIPpUAS(t, "uas_digest.xml", "udp")
		cfg := uas.config(map[string]any{"number": "1000"}, map[string]any{"username": "probara", "password": sippPassword})
		result, m := runInvite(t, newInviteTestChecker(), cfg, 10)
		requireStatus(t, result, "success")
		requireHTTPStatus(t, result, 200)
		if m.Auth == nil || m.Auth.Challenge != 401 || m.Auth.Rounds != 1 {
			t.Fatalf("auth = %+v, want one 401 round", m.Auth)
		}
		if m.Call == nil || m.Call.SDPAnswer == nil || m.Call.SDPAnswer.Codec != "PCMA/8000" || m.Call.Teardown != "bye_ok" {
			t.Fatalf("call = %+v", m.Call)
		}
		uas.requireCallCompleted(t)
	})

	// A wrong password fails verifyauth: SIPp answers 403, which the probe
	// ACKs and reports as an auth failure.
	t.Run("digest/wrong-password", func(t *testing.T) {
		uas := startSIPpUAS(t, "uas_digest.xml", "udp")
		cfg := uas.config(map[string]any{"number": "1000"}, map[string]any{"username": "probara", "password": "not-" + sippPassword})
		result, _ := runInvite(t, newInviteTestChecker(), cfg, 10)
		requireStatus(t, result, "failure", "auth:")
		requireHTTPStatus(t, result, 403)
		uas.requireCallCompleted(t)
	})

	// Ring mode: success on the 180, then CANCEL; SIPp confirms it, ends the
	// INVITE with 487 and requires the probe's ACK of it.
	t.Run("ring", func(t *testing.T) {
		uas := startSIPpUAS(t, "uas_ring.xml", "udp")
		result, m := runInvite(t, newInviteTestChecker(), uas.config(map[string]any{"number": "1000", "mode": "ring"}, nil), 10)
		requireStatus(t, result, "success")
		requireHTTPStatus(t, result, 180)
		if m.Call == nil || m.Call.Answered || m.Call.Teardown != "cancelled" || m.Call.TeardownStatus != 487 {
			t.Fatalf("call = %+v, want a cancelled ring", m.Call)
		}
		uas.requireCallCompleted(t)
	})

}
