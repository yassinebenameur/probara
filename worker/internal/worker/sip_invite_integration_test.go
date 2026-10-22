//go:build !unit

package worker

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
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
//
// The media subtests answer with uas_echo.xml.tmpl and run SIPp's
// -rtp_echo, which sends each datagram on its -mp socket back to its source
// unchanged. SIPp's SDP would name its container address, which a macOS
// host cannot reach, so the media port is published on the host's
// 127.0.0.1 under the same number and the templated SDP names that. The
// probe sends first (symmetric RTP from the offered port), and the
// forwarder that serves loopback-published UDP (Docker Desktop's on macOS,
// docker-proxy or DNAT/conntrack on Linux) carries the echo back from
// 127.0.0.1:P, the address the SDP named, so no latch is involved. That
// needs a daemon publishing on this machine's loopback, so a remote
// DOCKER_HOST fails the media subtests (startSIPpUASMedia) rather than
// skipping them: CI fails every skip, subtests included. One-way streams,
// loss and jitter stay with fakeMediaPeer in sip_media_test.go: anything
// SIPp originates goes to the container's own view of the probe's address.

const (
	sippImageRepo = "probara-sipp-test"
	// sippImageTag is a label only, mirroring scripts/sipp/Dockerfile's
	// sip-tester pin (Docker tags allow no ':' or '+'): the build follows the
	// Dockerfile (layer-cached), so a stale tag never runs a stale SIPp.
	sippImageTag = "3.6.1-1"
	sippPassword = "probara-secret" // uas_digest.xml's verifyauth password
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
	scenarioPath, err := filepath.Abs(filepath.Join("testdata", "sipp", scenario))
	if err != nil {
		t.Fatal(err)
	}
	s, err := startSIPp(t, scenarioPath, transport, nil)
	if err != nil {
		t.Fatalf("start SIPp: %v", err)
	}
	return s
}

// sippMedia is a SIPp UAS's media socket: -mp port, published on the
// host's 127.0.0.1 under the same number, echoing with -rtp_echo.
type sippMedia struct {
	port int
	echo bool
}

// hostAddr is where the probe sends: the published port, which the
// templated SDP names.
func (m *sippMedia) hostAddr() string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(m.port)) }

// sippCodecs are the payload types uas_echo.xml.tmpl can answer with.
var sippCodecs = map[int]string{0: "PCMU", 8: "PCMA"}

// startSIPpUASMedia runs one SIPp UAS call of uas_echo.xml.tmpl over
// transport, answering payload type pt with its media socket on a fixed
// host loopback port, echoing RTP when echo is set (see the file comment).
func startSIPpUASMedia(t *testing.T, transport string, pt int, echo bool) (*sippUAS, *sippMedia) {
	t.Helper()
	codec, ok := sippCodecs[pt]
	if !ok {
		t.Fatalf("uas_echo.xml.tmpl answers PT 0 or 8, not %d", pt)
	}
	tmpl, err := os.ReadFile(filepath.Join("testdata", "sipp", "uas_echo.xml.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	// A port taken between the pick and the container start (another
	// subtest's, a host process's) fails the start; the second pick runs
	// against the ports in use by then.
	for attempt := 1; ; attempt++ {
		media := &sippMedia{port: freeSIPpMediaPort(t), echo: echo}
		scenario := strings.NewReplacer(
			"__MEDIA_ADDR__", "127.0.0.1",
			"__MEDIA_PORT__", strconv.Itoa(media.port),
			"__PT__", strconv.Itoa(pt),
			"__CODEC__", codec,
		).Replace(string(tmpl))
		path := filepath.Join(t.TempDir(), "uas_echo.xml")
		if err := os.WriteFile(path, []byte(scenario), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := startSIPp(t, path, transport, media)
		if err != nil && attempt == 1 && sippPortTaken(err) {
			t.Logf("SIPp media port %d taken at start; picking another: %v", media.port, err)
			continue
		}
		if err != nil {
			t.Fatalf("start SIPp with media on %s (readiness waits for SIP on 5060, the media socket on a non-loopback address and a SIPp build with -rtp_echo): %v", media.hostAddr(), err)
		}
		// The binding is on this machine's loopback, which only a local
		// daemon publishes on; never a skip (CI fails those, subtests too).
		if s.host != "127.0.0.1" {
			t.Fatalf("SIPp media tests need a local Docker daemon (Docker Desktop or the runner's dockerd): the media port is published on this machine's 127.0.0.1, but the daemon serves %s (DOCKER_HOST?)", s.host)
		}
		return s, media
	}
}

// freeSIPpMediaPort picks an even port P with P and P+1 both free on
// 127.0.0.1: P is published to SIPp's media socket, and P+1 is where the
// probe sends its RTCP (nothing listens there, and nothing else should).
func freeSIPpMediaPort(t *testing.T) int {
	t.Helper()
	loopback := net.IPv4(127, 0, 0, 1)
	for range 100 {
		rtpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: loopback})
		if err != nil {
			t.Fatal(err)
		}
		port := rtpConn.LocalAddr().(*net.UDPAddr).Port
		if port%2 != 0 {
			rtpConn.Close()
			continue
		}
		rtcpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: loopback, Port: port + 1})
		rtpConn.Close()
		if err == nil {
			rtcpConn.Close()
			return port
		}
	}
	t.Fatal("no free even/odd UDP port pair on 127.0.0.1")
	return 0
}

// sippPortTaken says a container start failed because the fixed host port
// was bound in the meantime (dockerd: "port is already allocated"; Docker
// Desktop: "address already in use").
func sippPortTaken(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "port is already allocated") || strings.Contains(msg, "address already in use")
}

// startSIPp starts SIPp on scenarioPath (a host file) over transport, with
// a media socket when media is set. The container is cleaned up whether or
// not it started.
func startSIPp(t *testing.T, scenarioPath, transport string, media *sippMedia) (*sippUAS, error) {
	t.Helper()
	ctx := context.Background()

	sippTransport, port := "u1", "5060/udp"
	if transport == "tcp" {
		sippTransport, port = "t1", "5060/tcp"
	}
	cmd := []string{
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
	}
	// 5060 is 13C4 in /proc/net: ready once SIPp has bound its socket,
	// for UDP as well as TCP.
	ready := []wait.Strategy{wait.ForExec([]string{"sh", "-c", "grep -qi ':13C4 ' /proc/net/udp /proc/net/tcp /proc/net/udp6 /proc/net/tcp6"})}
	exposed := []string{port}
	var hostConfig func(*container.HostConfig)
	if media != nil {
		cmd = append(cmd, "-mp", strconv.Itoa(media.port))
		if media.echo {
			cmd = append(cmd, "-rtp_echo")
		}
		mediaPort := network.MustParsePort(fmt.Sprintf("%d/udp", media.port))
		exposed = append(exposed, mediaPort.String())
		// Host port = container port, so the SDP can name it. Added to
		// the map, never replacing it: the random 5060 mapping the SIP
		// flow dials lives there too, whichever of this modifier and
		// testcontainers' own binding of ExposedPorts runs first.
		hostConfig = func(hc *container.HostConfig) {
			if hc.PortBindings == nil {
				hc.PortBindings = network.PortMap{}
			}
			hc.PortBindings[mediaPort] = []network.PortBinding{{
				HostIP:   netip.AddrFrom4([4]byte{127, 0, 0, 1}),
				HostPort: strconv.Itoa(media.port),
			}}
		}
		// SIPp binds media on -mi, which defaults to the container's
		// primary address. Bound to its loopback instead, the forwarder
		// could not deliver to it and every case would read "no echo";
		// /proc/net/udp's local address is little-endian hex, so a
		// 127/8 address ends in 7F. A pinned build without -rtp_echo
		// fails here too, instead of as a missing echo.
		ready = append(ready,
			wait.ForExec([]string{"sh", "-c", fmt.Sprintf(
				`awk 'NR > 1 { split($2, a, ":"); if (a[2] == "%04X" && substr(a[1], 7, 2) != "7F") found = 1 } END { exit !found }' /proc/net/udp`,
				media.port)}),
			wait.ForExec([]string{"sh", "-c", "sipp -h 2>&1 | grep -q -- -rtp_echo"}),
		)
	}

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:   filepath.Join("..", "..", "..", "scripts", "sipp"),
			Repo:      sippImageRepo,
			Tag:       sippImageTag,
			KeepImage: true,
		},
		ExposedPorts:       exposed,
		HostConfigModifier: hostConfig,
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      scenarioPath,
			ContainerFilePath: "/sipp/scenario.xml",
			FileMode:          0o644,
		}},
		Cmd:        cmd,
		WaitingFor: wait.ForAll(ready...).WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		return nil, err
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
	return s, nil
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

	t.Run("media", func(t *testing.T) { testSIPpMedia(t) })
}

// testSIPpMedia runs the media cases against SIPp, in parallel: each holds
// the call for seconds and mostly waits. mediaTests clears
// SIP_LOCALHOST_AS_HOST_GATEWAY here, which a parallel subtest cannot.
func testSIPpMedia(t *testing.T) {
	// echoed checks a call measured against -rtp_echo: every datagram came
	// back from the address the SDP named, matched by the probe's own SSRC.
	// The forwarders may drop a first datagram and add VM jitter, so the
	// bounds are loose. The quality figures are asserted only when the
	// worker's own timing was reliable, which measure's reruns cannot
	// guarantee on a loaded -race runner; the structure is asserted always.
	echoed := func(t *testing.T, uas *sippUAS, sipp *sippMedia, result CheckResult, m *sipMetrics, media *sipMediaMetrics, codec string, pt int) {
		t.Helper()
		requireStatus(t, result, "success")
		requireHTTPStatus(t, result, 200)
		requireMediaStatus(t, media, sipMediaMeasured, "")
		if media.Codec != codec || media.TxPT == nil || *media.TxPT != pt {
			t.Fatalf("codec = %q tx_payload_type = %v, want %s sent as PT %d", media.Codec, media.TxPT, codec, pt)
		}
		if media.Destination != sipp.hostAddr() || media.Latched != "" || len(media.RX.Sources) != 1 || media.RX.Sources[0] != sipp.hostAddr() {
			t.Fatalf("destination = %q latched = %q sources = %q, want %s both ways and no latch", media.Destination, media.Latched, media.RX.Sources, sipp.hostAddr())
		}
		rt := media.RoundTrip
		if rt == nil || rt.MatchedBy != "ssrc" || rt.Expected < 50 || float64(rt.Matched) < 0.9*float64(rt.Expected) {
			t.Fatalf("round_trip = %+v, want at least 90%% of the expected echoes matched by SSRC", rt)
		}
		if media.TwoWay != "verified" || strings.Join(media.TwoWayEvidence, ",") != "rx,echo" {
			t.Fatalf("two_way = %q %q, want verified by rx and echo", media.TwoWay, media.TwoWayEvidence)
		}
		if media.TX.Reliable {
			if rt.Quality == nil || rt.RTTMs == nil || rt.RTTMs.P50 >= 200 {
				t.Fatalf("round_trip quality = %+v rtt = %+v, want an estimate and a p50 under 200 ms", rt.Quality, rt.RTTMs)
			}
		} else {
			t.Logf("media timing unreliable (%+v); quality not asserted", media.TX)
		}
		if m.Call.Teardown != "bye_ok" || m.Call.TeardownStatus != 200 {
			t.Fatalf("teardown = %q %d, want bye_ok", m.Call.Teardown, m.Call.TeardownStatus)
		}
		uas.requireCallCompleted(t)
	}
	call := func(uas *sippUAS, holdMs int, media map[string]any) map[string]any {
		return uas.config(map[string]any{"number": "1000", "hold_ms": holdMs, "media": media}, nil)
	}
	// measure runs a call against an echoing SIPp, on a fresh one (SIPp
	// takes one call) while the worker's timing reads too loaded to judge.
	measure := func(t *testing.T, transport string, pt, holdMs int, media map[string]any) (*sippUAS, *sippMedia, CheckResult, *sipMetrics, *sipMediaMetrics) {
		t.Helper()
		var (
			uas  *sippUAS
			sipp *sippMedia
		)
		result, m, mm := reliably(t, func() (CheckResult, *sipMetrics, *sipMediaMetrics) {
			uas, sipp = startSIPpUASMedia(t, transport, pt, true)
			return runMediaCall(t, context.Background(), newInviteTestChecker(), call(uas, holdMs, media))
		})
		return uas, sipp, result, m, mm
	}

	mediaTests(t, map[string]func(t *testing.T){
		// The echo test line: the probe's marked tone comes back as sent,
		// over each codec SIPp can answer and with TCP signalling (media is
		// UDP whatever the signalling transport).
		"echo/pcmu/udp": func(t *testing.T) {
			uas, sipp, result, m, media := measure(t, "udp", 0, 3000, map[string]any{"expect": "echo"})
			echoed(t, uas, sipp, result, m, media, "PCMU/8000", 0)
		},
		"echo/pcma/udp": func(t *testing.T) {
			uas, sipp, result, m, media := measure(t, "udp", 8, 3000, map[string]any{"expect": "echo"})
			echoed(t, uas, sipp, result, m, media, "PCMA/8000", 8)
		},
		"echo/pcmu/tcp": func(t *testing.T) {
			uas, sipp, result, m, media := measure(t, "tcp", 0, 3000, map[string]any{"expect": "echo"})
			echoed(t, uas, sipp, result, m, media, "PCMU/8000", 0)
		},
		// The echo is the far end's audio as far as the probe can tell, so
		// "audio" holds; the probe sends silence here, not the tone.
		"audio": func(t *testing.T) {
			uas, sipp, result, m, media := measure(t, "udp", 0, 3000, map[string]any{"expect": "audio"})
			echoed(t, uas, sipp, result, m, media, "PCMU/8000", 0)
		},
		// Two-way proven by the echo (SIPp sends no RTCP; the probe's goes
		// to P+1, where nothing listens), at the 6 s hold two_way needs,
		// with a loss limit applied to the round trip.
		"two_way": func(t *testing.T) {
			uas, sipp, result, m, media := measure(t, "udp", 0, 6000, map[string]any{"expect": "two_way", "max_loss_pct": 10})
			echoed(t, uas, sipp, result, m, media, "PCMU/8000", 0)
			if want := net.JoinHostPort("127.0.0.1", strconv.Itoa(sipp.port+1)); media.TX.RTCP < 2 || media.RTCPDestination != want {
				t.Fatalf("tx.rtcp = %d rtcp_destination = %q, want sender reports to %s", media.TX.RTCP, media.RTCPDestination, want)
			}
		},
		// Without -rtp_echo SIPp keeps its media socket and reads nothing
		// back: one-way audio from the far end's side. Deterministic, since
		// nothing can arrive.
		"echo/off": func(t *testing.T) {
			uas, _ := startSIPpUASMedia(t, "udp", 0, false)
			result, m, media := runMediaCall(t, context.Background(), newInviteTestChecker(), call(uas, 3000, map[string]any{"expect": "echo"}))
			requireStatus(t, result, "failure", "media: no echo of the probe's audio in")
			requireMediaStatus(t, media, sipMediaMeasured, "")
			if media.TX.Packets < 100 || media.RX.Packets != 0 || len(m.AssertionsFailed) != 1 || m.Call.Teardown != "bye_ok" {
				t.Fatalf("tx = %d rx = %d assertions = %q teardown = %q, want audio sent, none back, one assertion and bye_ok",
					media.TX.Packets, media.RX.Packets, m.AssertionsFailed, m.Call.Teardown)
			}
			requireNoAddress(t, result, m)
			uas.requireCallCompleted(t)
		},
		"audio/off": func(t *testing.T) {
			uas, _ := startSIPpUASMedia(t, "udp", 0, false)
			result, m, _ := runMediaCall(t, context.Background(), newInviteTestChecker(), call(uas, 3000, map[string]any{"expect": "audio"}))
			requireStatus(t, result, "failure", "media: no audio received in")
			if len(m.AssertionsFailed) != 1 || m.Call.Teardown != "bye_ok" {
				t.Fatalf("assertions = %q teardown = %q, want one assertion and bye_ok", m.AssertionsFailed, m.Call.Teardown)
			}
			requireNoAddress(t, result, m)
			uas.requireCallCompleted(t)
		},
		// Measure only: the same silence is a warning, never the status.
		"echo/expect-none": func(t *testing.T) {
			uas, _ := startSIPpUASMedia(t, "udp", 0, false)
			result, m, media := runMediaCall(t, context.Background(), newInviteTestChecker(), call(uas, 3000, map[string]any{"expect": "none"}))
			requireStatus(t, result, "success")
			requireMediaStatus(t, media, sipMediaMeasured, "")
			requireWarning(t, m, "media: no audio received")
			if len(m.AssertionsFailed) != 0 || m.Call.Teardown != "bye_ok" {
				t.Fatalf("assertions = %q teardown = %q, want none and bye_ok", m.AssertionsFailed, m.Call.Teardown)
			}
			uas.requireCallCompleted(t)
		},
	})
}
