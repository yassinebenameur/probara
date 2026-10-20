package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/jackc/pgx/v5"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// targets keeps the Northwind world alive: mock endpoints whose behaviour
// matches the seeded history, push heartbeats, OTLP host metrics, and real
// private-location workers.
type targets struct {
	o     options
	env   Env
	specs []Spec
	pool  *pgx.Conn

	mu         sync.Mutex
	routes     map[string]*Spec
	maintUntil map[string]time.Time // path -> serve 503 until
	pushes     map[string]*pushState
	agents     map[string]string // spec key -> agent id
	otlpKey    string
	workers    map[string]*locWorker // location key -> process

	callers      callers
	locatedHosts map[string]bool // host labels some multi-location monitor checks
}

type pushState struct {
	spec  *Spec
	token string
	next  time.Time
}

type locWorker struct {
	id, cred string
	cmd      *exec.Cmd
	done     chan struct{}
}

func runTargets(ctx context.Context, o options) error {
	if err := writePID(o.pidFile); err != nil {
		return err
	}
	defer os.Remove(o.pidFile)

	pool, err := pgx.Connect(ctx, o.dbURL)
	if err != nil {
		return err
	}
	defer pool.Close(context.Background())
	t := &targets{o: o, env: Env{Base: o.mockPort}, pool: pool, routes: map[string]*Spec{}, maintUntil: map[string]time.Time{},
		pushes: map[string]*pushState{}, agents: map[string]string{}, workers: map[string]*locWorker{}}
	t.specs = catalog(t.env)
	specDefaults(t.specs)
	t.locatedHosts = map[string]bool{}
	for i := range t.specs {
		s := &t.specs[i]
		if s.Path != "" && t.routes[s.Path] == nil {
			t.routes[s.Path] = s
		}
		if len(s.Locations) > 0 {
			t.locatedHosts[s.Host] = true
		}
	}

	if o.docker {
		t.ensureContainers(ctx)
	}
	errc := make(chan error, 8)
	go func() { errc <- t.serveHTTP(ctx) }()
	go func() { errc <- t.serveGRPC(ctx) }()
	t.serveTCP(ctx)
	t.serveSIP(ctx)
	t.serveMeshProxies(ctx)
	logf("mock targets up on *.%s: http/ws :%d, grpc :%d, tcp/sip on %d-%d, mesh endpoints %d-%d, dead port %d", Domain, t.env.port(offHTTP),
		t.env.port(offGRPC), t.env.port(offTCP), t.env.port(offSIP+10), t.env.port(offMeshProxy), t.env.port(offMeshProxy+2), t.env.port(offDead))

	// seed sends SIGHUP right after it commits, so a re-seed is picked up
	// before the first live dispatch rather than on the next 20s tick.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	// Everything below is scheduled on the WALL clock, polled every second.
	// Go timers and time.Time comparisons use the monotonic clock, which
	// stops while macOS sleeps: a 30s ticker armed before a sleep fires 30s
	// after the wake, and a deadline computed with time.Now() still looks
	// "in the future" after hours asleep, so push monitors stayed down long
	// after the machine woke. wallNow strips the monotonic reading, so a
	// missed beat is simply overdue and goes out on the first tick after
	// the wake.
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	now := wallNow()
	nextReload, nextOTLP := now.Add(reloadEvery), now
	if due := t.reload(ctx); !due.IsZero() {
		nextOTLP = due
	}
	for {
		select {
		case <-ctx.Done():
			t.stopWorkers()
			logf("targets stopped")
			return nil
		case err := <-errc:
			t.stopWorkers()
			return err
		case <-hup:
			if due := t.reload(ctx); !due.IsZero() {
				nextOTLP = due
			}
			nextReload = wallNow().Add(reloadEvery)
		case <-tick.C:
			now := wallNow()
			if !now.Before(nextReload) {
				if due := t.reload(ctx); !due.IsZero() {
					nextOTLP = due
				}
				nextReload = wallNow().Add(reloadEvery)
			}
			t.sendPushes(ctx)
			if !now.Before(nextOTLP) {
				if now.Sub(nextOTLP) > otlpEvery {
					logf("clock jumped %s (machine asleep?): catching up", now.Sub(nextOTLP).Round(time.Second))
				}
				t.sendOTLP(ctx)
				nextOTLP = wallNow().Add(otlpEvery)
			}
		}
	}
}

const (
	reloadEvery = 20 * time.Second
	// otlpEvery is the host-metric export cadence. Each export is one audit
	// row (the audit middleware records every non-GET request), so it is
	// kept at the collector's usual 60s rather than anything faster. The
	// history simulates one agent heartbeat per export at the same cadence.
	otlpEvery = 60 * time.Second
)

// wallNow is time.Now without its monotonic reading, so comparisons and
// arithmetic use the wall clock (which keeps running while the machine
// sleeps).
func wallNow() time.Time { return time.Now().Round(0) }

func writePID(path string) error {
	if b, err := os.ReadFile(path); err == nil {
		if pid, _ := strconv.Atoi(strings.TrimSpace(string(b))); pid > 0 && pid != os.Getpid() {
			if p, err := os.FindProcess(pid); err == nil && p.Signal(syscall.Signal(0)) == nil {
				return fmt.Errorf("targets already running (pid %d, %s)", pid, path)
			}
		}
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
}

// ── reload from the DB ───────────────────────────────────────────────────

// reload re-reads the tenant. When the set of agents changed (a re-seed),
// it returns when the next OTLP export is due: one export period after the
// newest seeded heartbeat, so live heartbeats continue the seeded cadence
// instead of leaving a gap until the old schedule comes round. New push
// monitors are scheduled the same way from their last seeded heartbeat, so
// a nightly job does not report at whatever time targets reloaded.
func (t *targets) reload(ctx context.Context) (otlpDue time.Time) {
	byName := map[string]*Spec{}
	for i := range t.specs {
		byName[t.specs[i].Name] = &t.specs[i]
	}
	rows, err := t.pool.Query(ctx, `SELECT id, name, type, COALESCE(push_token,''), COALESCE(agent_id,''), enabled, last_result_at FROM monitors
		WHERE tenant_id = $1 AND deleted_at IS NULL AND type IN ('push','agent') OR (tenant_id = $1 AND deleted_at IS NULL AND name = ANY($2))`, TenantID, t.maintDownNames())
	if err != nil {
		logf("reload: %v", err)
		return otlpDue
	}
	pushes := map[string]*pushState{}
	agents := map[string]string{}
	maintIDs := map[string]string{} // path -> monitor id
	var lastAgentBeat time.Time
	for rows.Next() {
		var id, name, typ, token, agent string
		var enabled bool
		var last *time.Time
		if err := rows.Scan(&id, &name, &typ, &token, &agent, &enabled, &last); err != nil {
			logf("reload scan: %v", err)
			continue
		}
		s := byName[name]
		if s == nil || !enabled {
			continue
		}
		switch {
		case typ == "push" && token != "":
			p := &pushState{spec: s, token: token}
			if last != nil {
				p.next = last.Add(time.Duration(s.Interval) * time.Second)
			}
			pushes[token] = p
		case typ == "agent" && agent != "":
			agents[s.Key] = agent
			if last != nil && last.After(lastAgentBeat) {
				lastAgentBeat = *last
			}
		case s.Profile == MaintDown:
			maintIDs[s.Path] = id
		}
	}
	rows.Close()

	maint := map[string]time.Time{}
	for path, id := range maintIDs {
		var ends *time.Time
		if err := t.pool.QueryRow(ctx, `SELECT max(mw.ends_at) FROM maintenance_windows mw JOIN maintenance_window_monitors m ON m.maintenance_window_id = mw.id
			WHERE m.monitor_id = $1 AND mw.starts_at <= now() AND mw.ends_at > now()`, id).Scan(&ends); err == nil && ends != nil {
			maint[path] = ends.Add(-10 * time.Minute)
		}
	}

	key := ""
	if b, err := os.ReadFile(t.o.keyFile); err == nil {
		key = strings.TrimSpace(string(b))
	}

	t.mu.Lock()
	for tok, p := range pushes {
		if old := t.pushes[tok]; old != nil {
			p.next = old.next
		}
	}
	changed := len(pushes) != len(t.pushes) || len(agents) != len(t.agents)
	agentsChanged := len(agents) != len(t.agents)
	for k, id := range agents {
		if t.agents[k] != id {
			agentsChanged = true
		}
	}
	if agentsChanged && len(agents) > 0 {
		otlpDue = wallNow()
		if !lastAgentBeat.IsZero() && lastAgentBeat.Add(otlpEvery).After(otlpDue) {
			otlpDue = lastAgentBeat.Add(otlpEvery)
		}
	}
	t.pushes, t.agents, t.maintUntil, t.otlpKey = pushes, agents, maint, key
	t.mu.Unlock()
	if changed {
		logf("tenant loaded: %d push monitors, %d agents, %d maintenance-bound targets", len(pushes), len(agents), len(maint))
	}
	t.superviseWorkers(ctx)
	return otlpDue
}

func (t *targets) maintDownNames() []string {
	var out []string
	for _, s := range t.specs {
		if s.Profile == MaintDown {
			out = append(out, s.Name)
		}
	}
	return out
}

// ── HTTP + websocket mock ────────────────────────────────────────────────

func (t *targets) serveHTTP(ctx context.Context) error {
	srv := &http.Server{Addr: fmt.Sprintf(":%d", t.env.port(offHTTP)), Handler: http.HandlerFunc(t.handle), ReadHeaderTimeout: 10 * time.Second}
	go func() { <-ctx.Done(); srv.Close() }()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("mock http: %w", err)
	}
	return nil
}

func (t *targets) handle(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	host := hostLabel(r.Host)
	caller := ""
	if t.locatedHosts[host] {
		caller = t.callers.of(r.RemoteAddr, start)
	}
	if caller == "sin" && sinEdgeHosts[host] {
		// The CDN's Singapore PoP is struggling: a slow 504 from the edge.
		sleepUntil(start, sinEdgeLatency(rng))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Served-By", "cache-sin-wsss1830043-SIN")
		w.WriteHeader(http.StatusGatewayTimeout)
		io.WriteString(w, "<html><body><h1>504 Gateway Time-out</h1><p>The edge (SIN) timed out waiting for the origin.</p></body></html>")
		return
	}
	netMs := locLatency(caller, rng)
	// synthetic API journey helpers
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/checkout/session":
		sleepUntil(start, 30+rng.IntN(60)+netMs)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"session_id":"cs_%06x","state":"pending"}`, rng.IntN(1<<24))
		return
	case strings.HasPrefix(r.URL.Path, "/api/checkout/session/"):
		sleepUntil(start, 15+rng.IntN(30)+netMs)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"session_id":%q,"state":"ready","total":"84.97","currency":"GBP"}`, strings.TrimPrefix(r.URL.Path, "/api/checkout/session/"))
		return
	}
	t.mu.Lock()
	s := t.routes[r.URL.Path]
	until := t.maintUntil[r.URL.Path]
	t.mu.Unlock()
	if s == nil {
		http.NotFound(w, r)
		return
	}
	if s.Type == "websocket" {
		t.serveWS(w, r, s, caller)
		return
	}
	fail := false
	switch s.Profile {
	case Down:
		fail = true
	case Flaky:
		fail = rng.Float64() < flakyRateAt(s.Key, time.Now())
	case MaintDown:
		fail = time.Now().Before(until)
	}
	if fail {
		sleepUntil(start, 4+rng.IntN(25)+netMs)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, "<html><body><h1>503 Service Unavailable</h1><p>upstream connect error or disconnect/reset before headers</p></body></html>")
		return
	}
	sleepUntil(start, latency(s, time.Now(), rng)+netMs)
	if s.JSON != nil {
		body := map[string]any{"status": "ok", "service": s.Key, "checked_at": time.Now().UTC().Format(time.RFC3339)}
		for k, v := range s.JSON {
			body[k] = v
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
		return
	}
	switch {
	case strings.HasSuffix(s.Path, ".js"):
		w.Header().Set("Content-Type", "application/javascript")
		fmt.Fprintf(w, "(self.webpackChunk_northwind=self.webpackChunk_northwind||[]).push([[179],{4211:function(e,t,n){}}]);\n%s", strings.Repeat("/* bundle */\n", 40))
	case strings.HasSuffix(s.Path, ".xml"):
		w.Header().Set("Content-Type", "application/xml")
		io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>https://northwind.example/</loc></url></urlset>`)
	case strings.HasSuffix(s.Path, ".txt"):
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "User-agent: *\nDisallow: /checkout\nSitemap: https://northwind.example/sitemap.xml\n")
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html lang="en"><head><title>%s · Northwind</title></head><body><header><a href="/">Northwind</a></header><main><h1>%s</h1><p>%s</p></main><footer>© Northwind Retail Ltd</footer></body></html>`,
			s.Name, s.Name, s.Keyword)
	}
}

// sleepUntil sleeps until ms milliseconds after start, so time spent
// identifying the caller is part of the latency rather than added to it.
func sleepUntil(start time.Time, ms int) {
	time.Sleep(time.Until(start.Add(time.Duration(ms) * time.Millisecond)))
}

// hostLabel is the service label of a request's Host ("www" for
// www.northwind.localtest.me:19100).
func hostLabel(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	if l, ok := strings.CutSuffix(host, "."+Domain); ok {
		return l
	}
	l, _ := strings.CutSuffix(host, ".localtest.me")
	return l
}

// sinEdgeLatency is how long the Singapore PoP takes to give up (ms), live
// and in the history.
func sinEdgeLatency(r *rand.Rand) int { return 2800 + r.IntN(1700) }

func (t *targets) serveWS(w http.ResponseWriter, r *http.Request, s *Spec, caller string) {
	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		return
	}
	go func() {
		defer conn.Close()
		rng := rand.New(rand.NewPCG(rand.Uint64(), 3))
		for {
			_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			msg, op, err := wsutil.ReadClientData(conn)
			if err != nil {
				return
			}
			if op == ws.OpText || op == ws.OpBinary {
				start := time.Now()
				reply := msg
				if strings.TrimSpace(string(msg)) == "ping" {
					reply = []byte("pong")
				}
				sleepUntil(start, latency(s, time.Now(), rng)+locLatency(caller, rng))
				if err := wsutil.WriteServerMessage(conn, op, reply); err != nil {
					return
				}
			}
		}
	}()
}

// ── who is calling ───────────────────────────────────────────────────────

// callers tells which private-location worker opened a connection, so the
// mock can add that location's network distance (and play Singapore's
// struggling CDN PoP). A check carries nothing that names its location: no
// header, and the HTTP checker ignores proxy settings. But the workers are
// this process's children, so the socket's owner does: one lsof over their
// PIDs maps each worker socket's local address (our RemoteAddr) to its
// location, and a scan serves every request that arrived before it began.
// Without lsof, IPv4 means Singapore, whose egress policy (127/8 only)
// keeps it off ::1; Frankfurt and Virginia then look like the platform
// worker.
type callers struct {
	mu     sync.Mutex
	pids   map[int]string // worker pid -> location key
	last   *socketScan
	noLsof bool
}

type socketScan struct {
	at     time.Time
	done   chan struct{}
	owner  map[string]string // "[::1]:54321" -> location key
	failed bool
}

func (c *callers) setPIDs(m map[int]string) {
	c.mu.Lock()
	c.pids = m
	c.last = nil
	c.mu.Unlock()
}

// of names the location whose worker owns the peer address remote of a
// connection that existed at since ("" = the platform worker or unknown).
func (c *callers) of(remote string, since time.Time) string {
	c.mu.Lock()
	if len(c.pids) == 0 {
		c.mu.Unlock()
		return ""
	}
	if c.noLsof {
		c.mu.Unlock()
		return ipv4Caller(remote)
	}
	sc := c.last
	if sc == nil || sc.at.Before(since) {
		sc = &socketScan{at: time.Now(), done: make(chan struct{})}
		c.last = sc
		pids := make(map[int]string, len(c.pids))
		for p, k := range c.pids {
			pids[p] = k
		}
		go c.scan(sc, pids)
	}
	c.mu.Unlock()
	select {
	case <-sc.done:
	case <-time.After(3 * time.Second):
		return ipv4Caller(remote)
	}
	if loc, ok := sc.owner[remote]; ok {
		return loc
	}
	if sc.failed {
		return ipv4Caller(remote)
	}
	return ""
}

func (c *callers) scan(sc *socketScan, pids map[int]string) {
	defer close(sc.done)
	sc.owner = map[string]string{}
	list := make([]string, 0, len(pids))
	for p := range pids {
		list = append(list, strconv.Itoa(p))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// -b/-O skip the kernel calls that can block and the helper process;
	// lsof exits 1 when nothing matches, so read stdout regardless.
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-b", "-O", "-w", "-a", "-p", strings.Join(list, ","), "-i", "-Fpn").Output()
	if err != nil && len(out) == 0 {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			c.mu.Lock()
			if !c.noLsof {
				logf("lsof unavailable (%v): only Singapore's checks are told apart (by IPv4)", err)
			}
			c.noLsof = true
			c.mu.Unlock()
			sc.failed = true
		}
		return
	}
	loc := ""
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid, _ := strconv.Atoi(line[1:])
			loc = pids[pid]
		case strings.HasPrefix(line, "n") && loc != "":
			if local, _, ok := strings.Cut(line[1:], "->"); ok {
				sc.owner[local] = loc
			}
		}
	}
}

func ipv4Caller(remote string) string {
	ap, err := netip.ParseAddrPort(remote)
	if err == nil && ap.Addr().Unmap().Is4() {
		return "sin"
	}
	return ""
}

// serveMeshProxies runs each worker location's mesh endpoint: a proxy to
// the worker's echo listener that holds the reply for the round trip
// between the probing location and this one, so the mesh matrix shows
// inter-region latency instead of loopback's 0 ms.
func (t *targets) serveMeshProxies(ctx context.Context) {
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	for _, l := range locationCatalog() {
		if !l.Worker || l.MeshOff == 0 {
			continue
		}
		dst := l.Key
		echo := fmt.Sprintf("http://127.0.0.1:%d", t.env.port(l.MeshOff))
		srv := &http.Server{Addr: fmt.Sprintf(":%d", t.env.port(l.MeshOff-offMesh+offMeshProxy)), ReadHeaderTimeout: 10 * time.Second,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				start := time.Now()
				src := t.callers.of(r.RemoteAddr, start)
				resp, err := client.Get(echo + r.URL.RequestURI())
				if err != nil {
					http.Error(w, "mesh endpoint: worker echo unreachable", http.StatusBadGateway)
					return
				}
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
				resp.Body.Close()
				if src != "" && src != dst {
					rng := rand.New(rand.NewPCG(rand.Uint64(), 5))
					sleepUntil(start, meshLatency(src, dst, rng))
				}
				if ct := resp.Header.Get("Content-Type"); ct != "" {
					w.Header().Set("Content-Type", ct)
				}
				w.WriteHeader(resp.StatusCode)
				_, _ = w.Write(body)
			})}
		go func() { <-ctx.Done(); srv.Close() }()
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logf("mesh endpoint %s: %v", dst, err)
			}
		}()
	}
}

// meshLatency is one mesh probe's round trip (ms), live and in history.
func meshLatency(src, dst string, r *rand.Rand) int {
	v := float64(meshRTT(src, dst)) * math.Exp(r.NormFloat64()*0.035)
	if r.Float64() < 0.01 {
		v *= 1.4 + r.Float64()
	}
	return int(math.Round(v)) + 1
}

// ── gRPC, TCP, SIP ───────────────────────────────────────────────────────

func (t *targets) serveGRPC(ctx context.Context) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", t.env.port(offGRPC)))
	if err != nil {
		return fmt.Errorf("mock grpc: %w", err)
	}
	srv := grpc.NewServer()
	hs := health.NewServer()
	hs.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	for _, s := range t.specs {
		if s.Type == "grpc" {
			hs.SetServingStatus(s.Service, healthpb.HealthCheckResponse_SERVING)
		}
	}
	healthpb.RegisterHealthServer(srv, hs)
	go func() { <-ctx.Done(); srv.Stop() }()
	return srv.Serve(lis)
}

func (t *targets) serveTCP(ctx context.Context) {
	seen := map[int]bool{}
	for _, s := range t.specs {
		if s.Type != "tcp" || s.Config != nil || s.Port == offDead || seen[s.Port] {
			continue
		}
		seen[s.Port] = true
		lis, err := net.Listen("tcp", fmt.Sprintf(":%d", t.env.port(s.Port)))
		if err != nil {
			logf("tcp %d: %v", t.env.port(s.Port), err)
			continue
		}
		go func() { <-ctx.Done(); lis.Close() }()
		go func() {
			for {
				c, err := lis.Accept()
				if err != nil {
					return
				}
				go func() { time.Sleep(200 * time.Millisecond); c.Close() }()
			}
		}()
	}
}

func (t *targets) serveSIP(ctx context.Context) {
	seen := map[int]bool{}
	for i := range t.specs {
		s := &t.specs[i]
		if s.Type != "sip" || seen[s.Port] {
			continue
		}
		// The first spec on a port owns its latency profile (as in seed).
		seen[s.Port] = true
		located := false
		for _, o := range t.specs {
			if o.Type == "sip" && o.Port == s.Port && len(o.Locations) > 0 {
				located = true
			}
		}
		pc, err := net.ListenPacket("udp", fmt.Sprintf(":%d", t.env.port(s.Port)))
		if err != nil {
			logf("sip %d: %v", t.env.port(s.Port), err)
			continue
		}
		go func() { <-ctx.Done(); pc.Close() }()
		go func() {
			rng := rand.New(rand.NewPCG(rand.Uint64(), 4))
			buf := make([]byte, 4096)
			for {
				n, addr, err := pc.ReadFrom(buf)
				if err != nil {
					return
				}
				start := time.Now()
				req := string(buf[:n])
				go func() {
					caller := ""
					if located {
						caller = t.callers.of(addr.String(), start)
					}
					sleepUntil(start, latency(s, time.Now(), rng)+locLatency(caller, rng))
					_, _ = pc.WriteTo([]byte(sipOK(req)), addr)
				}()
			}
		}()
	}
}

// sipOK answers a request with 200 OK, echoing the dialog headers.
func sipOK(req string) string {
	var b strings.Builder
	b.WriteString("SIP/2.0 200 OK\r\n")
	sc := bufio.NewScanner(strings.NewReader(req))
	for sc.Scan() {
		line := sc.Text()
		l := strings.ToLower(line)
		for _, h := range []string{"via:", "from:", "to:", "call-id:", "cseq:"} {
			if strings.HasPrefix(l, h) {
				if h == "to:" && !strings.Contains(l, "tag=") {
					line += ";tag=nw" + strconv.Itoa(rand.IntN(1e6))
				}
				b.WriteString(line + "\r\n")
			}
		}
	}
	b.WriteString("Server: Northwind-SBC/5.2\r\nAllow: INVITE, ACK, CANCEL, OPTIONS, BYE\r\nContent-Length: 0\r\n\r\n")
	return b.String()
}

// ── push heartbeats + OTLP ───────────────────────────────────────────────

func (t *targets) sendPushes(ctx context.Context) {
	now := wallNow()
	t.mu.Lock()
	var due []*pushState
	for _, p := range t.pushes {
		if !p.next.After(now) {
			// The history beats exactly once per interval (batch jobs at a
			// fixed time of day), and the grace period absorbs the 1s tick,
			// so live beats keep that cadence rather than running early.
			p.next = p.next.Add(time.Duration(p.spec.Interval) * time.Second)
			if !p.next.After(now) {
				p.next = now.Add(time.Duration(p.spec.Interval) * time.Second)
			}
			due = append(due, p)
		}
	}
	t.mu.Unlock()
	for _, p := range due {
		body := map[string]any{"status": "up"}
		for k, v := range p.spec.PushPayload {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				body[k] = f * (0.97 + rand.Float64()*0.06)
			}
		}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.o.apiURL, "/")+"/api/v1/push/"+p.token, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			logf("push %s: %v", p.spec.Key, err)
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			logf("push %s: %s", p.spec.Key, resp.Status)
		}
	}
}

func (t *targets) sendOTLP(ctx context.Context) {
	t.mu.Lock()
	agents := make(map[string]string, len(t.agents))
	for k, v := range t.agents {
		agents[k] = v
	}
	key := t.otlpKey
	t.mu.Unlock()
	if len(agents) == 0 {
		return
	}
	if key == "" {
		logf("otlp: no API key in %s (run seed)", t.o.keyFile)
		return
	}
	now := time.Now()
	ns := strconv.FormatInt(now.UnixNano(), 10)
	var rms []any
	for k, agentID := range agents {
		host := strings.TrimPrefix(t.nameOf(k), "Host — ")
		var metrics []any
		for _, p := range hostPoints(k, now) {
			var attrs []any
			for ak, av := range p.attrs {
				attrs = append(attrs, map[string]any{"key": ak, "value": map[string]any{"stringValue": av}})
			}
			dp := map[string]any{"timeUnixNano": ns, "asDouble": p.v, "attributes": attrs}
			m := map[string]any{"name": p.name, "unit": p.unit}
			if p.typ == "gauge" {
				m["gauge"] = map[string]any{"dataPoints": []any{dp}}
			} else {
				m["sum"] = map[string]any{"aggregationTemporality": 2, "isMonotonic": p.mono, "dataPoints": []any{dp}}
			}
			metrics = append(metrics, m)
		}
		rms = append(rms, map[string]any{
			"resource": map[string]any{"attributes": []any{
				map[string]any{"key": "probara.agent.id", "value": map[string]any{"stringValue": agentID}},
				map[string]any{"key": "host.name", "value": map[string]any{"stringValue": host}},
				map[string]any{"key": "os.type", "value": map[string]any{"stringValue": "linux"}},
			}},
			"scopeMetrics": []any{map[string]any{"scope": map[string]any{"name": "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver", "version": "0.114.0"}, "metrics": metrics}},
		})
	}
	b, _ := json.Marshal(map[string]any{"resourceMetrics": rms})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.o.apiURL, "/")+"/api/v1/otlp/v1/metrics", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("User-Agent", "probara-collector/0.114.0 (linux/amd64)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logf("otlp: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		rb, _ := io.ReadAll(resp.Body)
		logf("otlp: %s %s", resp.Status, truncate(string(rb), 200))
	}
}

func (t *targets) nameOf(key string) string {
	for _, s := range t.specs {
		if s.Key == key {
			return s.Name
		}
	}
	return key
}

// ── private-location workers ─────────────────────────────────────────────

func (t *targets) superviseWorkers(ctx context.Context) {
	want := map[string]struct {
		id, cred string
		spec     LocationSpec
	}{}
	byName := map[string]LocationSpec{}
	for _, l := range locationCatalog() {
		if l.Worker {
			byName[l.Name] = l
		}
	}
	rows, err := t.pool.Query(ctx, `SELECT id, name, COALESCE(worker_credential,'') FROM locations WHERE tenant_id = $1 AND deleted_at IS NULL AND enabled`, TenantID)
	if err != nil {
		logf("locations: %v", err)
		return
	}
	for rows.Next() {
		var id, name, cred string
		if err := rows.Scan(&id, &name, &cred); err != nil {
			continue
		}
		if l, ok := byName[name]; ok && cred != "" && !strings.HasPrefix(cred, "{") {
			want[l.Key] = struct {
				id, cred string
				spec     LocationSpec
			}{id, cred, l}
		}
	}
	rows.Close()

	if _, err := os.Stat(t.o.workerBin); err != nil {
		if len(want) > 0 {
			logf("locations: worker binary %s missing (run make start-all-local); location-bound monitors will go unknown", t.o.workerBin)
		}
		return
	}
	for key, lw := range t.workers {
		w, ok := want[key]
		exited := false
		select {
		case <-lw.done:
			exited = true
		default:
		}
		if !ok || w.id != lw.id || w.cred != lw.cred || exited {
			if !exited {
				_ = lw.cmd.Process.Signal(syscall.SIGTERM)
				<-lw.done
			}
			delete(t.workers, key)
		}
	}
	for key, w := range want {
		if t.workers[key] != nil {
			continue
		}
		logPath := filepath.Join(os.TempDir(), "probara-seed-worker-"+key+".log")
		if strings.HasPrefix(t.o.pidFile, "/tmp/") {
			logPath = "/tmp/probara-seed-worker-" + key + ".log"
		}
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			logf("worker %s: %v", key, err)
			continue
		}
		cmd := exec.Command(t.o.workerBin)
		cmd.Env = append([]string{
			"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"),
			"WORKER_LOCATION_ID=" + w.id, "LOCATION_CREDENTIAL=" + w.cred,
			"NATS_URL=" + t.o.natsURL, "CHECK_JOB_STREAM=" + envOr("CHECK_JOB_STREAM", "CHECK_JOBS"), "CHECK_JOB_SUBJECT=" + envOr("CHECK_JOB_SUBJECT", "check.jobs"),
			"HTTP_PORT=" + strconv.Itoa(t.env.port(w.spec.MeshOff)), "METRICS_PORT=" + strconv.Itoa(t.env.port(w.spec.MeshOff-offMesh+offMet)),
			"WORKER_CONCURRENCY=10", "LOG_LEVEL=info",
		}, w.spec.ExtraEnv...)
		cmd.Stdout, cmd.Stderr = f, f
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			f.Close()
			logf("worker %s: %v", key, err)
			continue
		}
		lw := &locWorker{id: w.id, cred: w.cred, cmd: cmd, done: make(chan struct{})}
		go func() { _ = cmd.Wait(); f.Close(); close(lw.done) }()
		t.workers[key] = lw
		logf("location worker %s started (pid %d, mesh :%d, log %s)", w.spec.Name, cmd.Process.Pid, t.env.port(w.spec.MeshOff), logPath)
	}
	pids := map[int]string{}
	for key, lw := range t.workers {
		pids[lw.cmd.Process.Pid] = key
	}
	t.callers.setPIDs(pids)
}

func (t *targets) stopWorkers() {
	for _, lw := range t.workers {
		_ = lw.cmd.Process.Signal(syscall.SIGTERM)
	}
	for _, lw := range t.workers {
		select {
		case <-lw.done:
		case <-time.After(10 * time.Second):
			_ = lw.cmd.Process.Kill()
		}
	}
}

// ── docker-backed targets ────────────────────────────────────────────────

func (t *targets) ensureContainers(ctx context.Context) {
	if _, err := exec.LookPath("docker"); err != nil {
		logf("docker not found: redis/rabbitmq monitors will be down")
		return
	}
	cs := []struct {
		name, port string
		args       []string
	}{
		{"probara-seed-redis", "6379", []string{"-p", "6379:6379", "redis:7-alpine"}},
		{"probara-seed-rabbitmq", "5672", []string{"-p", "5672:5672", "-e", "RABBITMQ_DEFAULT_USER=northwind", "-e", "RABBITMQ_DEFAULT_PASS=northwind-demo", "rabbitmq:3.13-alpine"}},
	}
	for _, c := range cs {
		out, _ := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", c.name).Output()
		switch strings.TrimSpace(string(out)) {
		case "true":
			continue
		case "false":
			if err := exec.CommandContext(ctx, "docker", "start", c.name).Run(); err != nil {
				logf("docker start %s: %v", c.name, err)
			} else {
				logf("started container %s", c.name)
			}
			continue
		}
		if conn, err := net.DialTimeout("tcp", "127.0.0.1:"+c.port, time.Second); err == nil {
			conn.Close()
			logf("port %s already in use; not starting %s", c.port, c.name)
			continue
		}
		args := append([]string{"run", "-d", "--name", c.name, "--restart", "unless-stopped"}, c.args...)
		if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
			logf("docker run %s: %v: %s", c.name, err, strings.TrimSpace(string(out)))
		} else {
			logf("started container %s", c.name)
		}
	}
}
