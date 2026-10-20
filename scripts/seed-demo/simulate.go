package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yassinebenameur/probara/shared/models"
	"github.com/yassinebenameur/probara/shared/monitorstate"
)

// flakyRate is the per-check failure probability of a Flaky target, live and
// in history.
const flakyRate = 0.02

// hit is one scripted failure window on one monitor.
type hit struct {
	Key     string
	Off     time.Duration // from the event start
	Dur     time.Duration
	Status  string // failure | error
	HTTP    int
	Err     string // "" = derived from the type
	Planned bool
	Event   string // event id (links alerts to incidents)
}

type window struct {
	start, end time.Time
	status     string
	http       int
	err        string
	event      string
	planned    bool
}

type checkRow struct {
	t       time.Time
	status  string
	http    int // 0 = NULL
	latency int // -1 = NULL
	errMsg  string
	metrics []byte
	loc     int // index into spec.Locations, -1 = none
}

type ivl struct {
	state, reason string
	start         time.Time
	end           *time.Time
}

type hourAgg struct {
	total, success, errors int
	latSum                 float64
	latCount               int
	latestStatus           string
	latestAt               time.Time
}

type downPeriod struct {
	start    time.Time
	end      *time.Time
	lastErr  string
	failures int
	event    string
	planned  bool
}

type locFinal struct {
	state       monitorstate.State
	cf          int
	lastLatency int
	lastAt      time.Time
	since       time.Time
}

type simResult struct {
	intervals  []ivl
	hours      map[time.Time]*hourAgg
	raw        []checkRow
	state      monitorstate.State
	cf         int
	lastAt     time.Time
	stateSince time.Time
	downs      []downPeriod
	locs       []locFinal
}

type simInput struct {
	spec       *Spec
	env        Env
	start, now time.Time // now plans the scripted timeline
	end        time.Time // checks run until end-2s (end >= now)
	latSpec    *Spec     // whose latency profile the mock serves on this path (nil = spec)
	rawStart   time.Time
	windows    []window
	pauses     [][2]time.Time // historical pause..resume
	sinSince   time.Time      // the Singapore PoP serves 504s for sinEdgeHosts from here on
	mods       *histMods      // the scripted past (nil = none)
	maintDown  time.Time      // MaintDown: fails from here on
}

func rngFor(key string, salt uint64) *rand.Rand {
	h := fnv.New64a()
	h.Write([]byte(key))
	return rand.New(rand.NewPCG(h.Sum64(), salt))
}

// latency draws a check latency (ms) from the origin's side: a stable
// per-monitor base inside the spec's band, the UK traffic load (weekday
// business hours and the evening peak, quieter weekends), a slow per-monitor
// drift, hour-to-hour wander, lognormal jitter and rare spikes. Everything
// but the jitter is a function of (key, time), so the mock sleeps the same
// curve live and live checks continue the seeded one. Location network time
// (locLatency) and the scripted past (histMods) are added by the callers.
func latency(s *Spec, t time.Time, r *rand.Rand) int {
	frac := unit(s.Key, "base")
	base := float64(s.LatMin) + frac*float64(s.LatMax-s.LatMin)
	base *= 1 + loadSensitivity(s)*trafficLoad(t)
	base *= slowFactor(s.Key, t)
	sigma := 0.10
	if s.Profile == Slow {
		sigma = 0.22
	}
	base *= math.Exp(r.NormFloat64() * sigma)
	if r.Float64() < 0.003 {
		base *= 1.8 + r.Float64()
	}
	return int(math.Max(0, math.Round(base)))
}

// resolveMs is the history's share of name resolution, which the live
// checks pay but the mock cannot see. Every mock target is a
// *.localtest.me name with a 300s TTL: most lookups hit the system cache,
// but a few percent of checks resolve over the network (20-110 ms). A TCP
// check also pays its cached lookup (1-2 ms), since it times only the
// dial. Internet targets (ping and DNS take IPs, third-party URLs) already
// include it in their profile.
func resolveMs(s *Spec, r *rand.Rand) int {
	switch s.Type {
	case "ping", "dns", "push", "agent", "group":
		return 0
	}
	if s.Profile == External {
		return 0
	}
	ms := 0
	if s.Type == "tcp" {
		ms = 1 + r.IntN(2)
	}
	if r.Float64() < 0.07 {
		f := r.Float64()
		ms += 20 + int(90*f*f)
	}
	return ms
}

// target is the host (FQDN) and port a non-HTTP spec dials.
func (s *Spec) target(e Env) (string, int) {
	host, port := e.fqdn(s.Host), 0
	if s.Config != nil {
		if h, ok := s.Config["host"].(string); ok {
			host = h
		}
		if p, ok := s.Config["port"].(int); ok {
			port = p
		}
	}
	if port == 0 && s.Port != 0 {
		port = e.port(s.Port)
	}
	if s.Type == "websocket" {
		port = e.port(offHTTP)
	}
	return host, port
}

// dialedAddr is what a dial error names: Go reports the resolved address it
// tried, and *.localtest.me resolves to ::1 first.
func dialedAddr(port int) string { return fmt.Sprintf("[::1]:%d", port) }

func pgTimeoutErr(e Env, s *Spec) string {
	host, port := s.target(e)
	return fmt.Sprintf("timeout: failed to connect to `user=probara database=probara`: %s (%s): dial error: timeout: context deadline exceeded", dialedAddr(port), host)
}

// failure text for a spec, matching what the real checkers write.
func failureFor(s *Spec, e Env, kind string, httpCode int) (status string, code int, msg string) {
	u := s.monitorURL(e)
	host, port := s.target(e)
	addr := dialedAddr(port)
	switch s.Type {
	case "http":
		if kind == "refused" {
			pu, _ := url.Parse(u)
			_, p, _ := strings.Cut(pu.Host, ":")
			return "error", 0, fmt.Sprintf("connect: Get %q: dial tcp [::1]:%s: connect: connection refused", u, p)
		}
		if kind == "timeout" {
			return "error", 0, fmt.Sprintf("timeout: Get %q: context deadline exceeded (Client.Timeout exceeded while awaiting headers)", u)
		}
		if httpCode == 0 {
			httpCode = 503
		}
		msg := fmt.Sprintf("status_code: got %d", httpCode)
		if s.Keyword != "" {
			msg += fmt.Sprintf("; body: missing %q", s.Keyword)
		} else if s.JSON != nil {
			msg += "; json: invalid JSON response"
		}
		return "failure", httpCode, msg
	case "synthetic_api":
		return "failure", 0, "step session: status: expected 201, got 503"
	case "tcp":
		// The TCP checker reports an unreachable target as a failure (only
		// an egress-policy refusal is an error).
		if kind == "timeout" {
			return "failure", 0, fmt.Sprintf("timeout: dial tcp %s: i/o timeout", addr)
		}
		return "failure", 0, fmt.Sprintf("connect: dial tcp %s: connect: connection refused", addr)
	case "redis", "rabbitmq", "websocket":
		if kind == "timeout" {
			return "error", 0, fmt.Sprintf("timeout: dial tcp %s: i/o timeout", addr)
		}
		return "error", 0, fmt.Sprintf("connect: dial tcp %s: connect: connection refused", addr)
	case "postgres":
		if kind == "timeout" {
			return "error", 0, pgTimeoutErr(e, s)
		}
		return "error", 0, fmt.Sprintf("connect: failed to connect to `user=probara database=probara`: %s (%s): dial error: dial tcp %s: connect: connection refused", addr, host, addr)
	case "grpc":
		return "error", 0, "grpc health: rpc error: code = Unavailable desc = connection error: desc = \"transport: Error while dialing: dial tcp " + addr + ": connect: connection refused\""
	case "sip":
		return "error", 0, fmt.Sprintf("timeout: no SIP response from %s:%d within %ds", host, port, s.Timeout)
	case "dns":
		return "error", 0, fmt.Sprintf("dns lookup failed: lookup %v on %v:53: i/o timeout", s.Config["host"], s.Config["nameserver"])
	case "ping":
		return "failure", 0, "no response received"
	case "push":
		return "failure", 0, "No push received within expected interval + grace period"
	case "agent":
		return "failure", 0, "Agent has not reported metrics within the freshness window"
	}
	return "error", 0, "check failed"
}

// successMetrics is the metrics_data of a passing check (nil when the real
// checker writes none worth faking).
func successMetrics(s *Spec, e Env) []byte {
	switch s.Type {
	case "http":
		m := map[string]any{"final_url": s.monitorURL(e)}
		if len(s.Display) > 0 {
			var dv []map[string]any
			for _, d := range s.Display {
				v := displayValue(s, d[0])
				if v == "" {
					continue
				}
				dv = append(dv, map[string]any{"label": d[1], "path": d[0], "value": v})
			}
			if len(dv) > 0 {
				m["display_values"] = dv
			}
		}
		b, _ := json.Marshal(map[string]any{"http": m})
		return b
	case "tcp":
		host, port := s.target(e)
		b, _ := json.Marshal(map[string]any{"tcp": map[string]any{"host": host, "port": port, "use_tls": false}})
		return b
	case "postgres":
		b, _ := json.Marshal(map[string]any{"postgres": map[string]any{"server_version": "16.14"}})
		return b
	case "dns":
		b, _ := json.Marshal(map[string]any{"dns": map[string]any{"nameserver": s.Config["nameserver"], "record_type": s.Config["record_type"]}})
		return b
	case "push":
		if len(s.PushPayload) == 0 {
			return nil
		}
		m := map[string]any{}
		for k, v := range s.PushPayload {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				m[k] = f
			} else {
				m[k] = v
			}
		}
		b, _ := json.Marshal(m)
		return b
	}
	return nil
}

func displayValue(s *Spec, path string) string {
	if strings.HasPrefix(path, "status.") {
		return "All Systems Operational"
	}
	if v, ok := s.JSON[path]; ok {
		return fmt.Sprint(v)
	}
	return ""
}

func failureMetrics(s *Spec, e Env, msg string) []byte {
	if s.Type != "http" {
		return nil
	}
	m := map[string]any{"final_url": s.monitorURL(e)}
	if strings.HasPrefix(msg, "status_code") {
		m["assertions_failed"] = strings.Split(msg, "; ")
	}
	b, _ := json.Marshal(map[string]any{"http": m})
	return b
}

// simulate runs every check of a monitor's history through the real state
// machine (monitorstate.Apply / AggregateLocations), so intervals, rollups,
// raw rows and the final monitor row agree by construction.
func simulate(in simInput) *simResult {
	s := in.spec
	r := rngFor(s.Key, 1)
	res := &simResult{hours: map[time.Time]*hourAgg{}}
	succMetrics := successMetrics(s, in.env)
	latSpec := s
	if in.latSpec != nil {
		latSpec = in.latSpec
	}

	nLoc := len(s.Locations)
	open := func(state monitorstate.State, reason string, at time.Time) {
		if n := len(res.intervals); n > 0 {
			end := at
			res.intervals[n-1].end = &end
		}
		res.intervals = append(res.intervals, ivl{state: string(state), reason: reason, start: at})
		res.stateSince = at
	}

	open(monitorstate.StateUnknown, "created", in.start)
	res.state = monitorstate.StateUnknown

	// Paused for good?
	var pausedAt time.Time
	if s.Profile == Paused {
		if s.PausedAgo == 0 {
			res.intervals[0].reason = "pause"
			return res
		}
		pausedAt = in.now.Add(-s.PausedAgo)
	}

	locStates := make([]monitorstate.Snapshot, nLoc)
	res.locs = make([]locFinal, nLoc)
	for i := range locStates {
		locStates[i] = monitorstate.Snapshot{State: monitorstate.StateUnknown}
	}

	interval := time.Duration(s.Interval) * time.Second
	if s.Type == "agent" {
		interval = otlpEvery // one heartbeat per accepted OTLP export
	}
	t := in.start.Add(time.Duration(r.Int64N(int64(interval))))
	if s.Type == "push" && s.Interval >= 3600 {
		// Batch jobs report at a fixed time of day / past the hour.
		t = in.start.Add(time.Duration(s.Interval/3600*7+13) * time.Minute)
	}
	end := in.end.Add(-2 * time.Second)

	var curDown *downPeriod
	pauseIdx := 0
	var lastWindow *window

	record := func(row checkRow) {
		h := row.t.UTC().Truncate(time.Hour)
		a := res.hours[h]
		if a == nil {
			a = &hourAgg{}
			res.hours[h] = a
		}
		a.total++
		switch row.status {
		case "success":
			a.success++
			if row.latency >= 0 {
				a.latSum += float64(row.latency)
				a.latCount++
			}
		case "error":
			a.errors++
		}
		if !row.t.Before(a.latestAt) {
			a.latestAt = row.t
			a.latestStatus = row.status
		}
		if !row.t.Before(in.rawStart) {
			res.raw = append(res.raw, row)
		}
	}

	for ; t.Before(end); t = t.Add(interval) {
		// historical pause/resume
		if pauseIdx < len(in.pauses) && !t.Before(in.pauses[pauseIdx][0]) {
			p := in.pauses[pauseIdx]
			open(monitorstate.StateUnknown, "pause", p[0])
			open(monitorstate.StateUnknown, "resume", p[1])
			res.state, res.cf = monitorstate.StateUnknown, 0
			for i := range locStates {
				locStates[i] = monitorstate.Snapshot{State: monitorstate.StateUnknown}
			}
			if curDown != nil {
				e := p[0]
				curDown.end = &e
				res.downs = append(res.downs, *curDown)
				curDown = nil
			}
			for !t.After(p[1]) {
				t = t.Add(interval)
			}
			pauseIdx++
		}
		if !pausedAt.IsZero() && !t.Before(pausedAt) {
			break
		}

		// Which scripted window (if any) covers this check?
		var w *window
		for i := range in.windows {
			if !t.Before(in.windows[i].start) && t.Before(in.windows[i].end) {
				w = &in.windows[i]
				break
			}
		}
		profileFail := false
		profileKind := ""
		switch s.Profile {
		case Down:
			profileFail = !t.Before(in.now.Add(-s.DownAgo))
		case Refused:
			profileFail = !t.Before(in.now.Add(-s.DownAgo))
			profileKind = "refused"
		case MaintDown:
			profileFail = !in.maintDown.IsZero() && !t.Before(in.maintDown)
		case Flaky:
			profileFail = r.Float64() < flakyRateAt(latSpec.Key, t)
		}
		if p := in.mods.extraFailRate(s.Key, t); p > 0 && r.Float64() < p {
			profileFail = true
		}

		mkRow := func(at time.Time, loc int, fail bool, forceKind string) checkRow {
			lat := latency(latSpec, at, r)
			if f := in.mods.latFactor(latSpec, at); f != 1 {
				lat = int(math.Round(float64(lat) * f))
			}
			lat += resolveMs(s, r)
			net := 0 // network time from the location (the origin's side is 0)
			if loc >= 0 {
				net = locLatency(s.Locations[loc], r)
			}
			row := checkRow{t: at, loc: loc, latency: lat + net}
			if s.Type == "push" {
				row.latency = -1
			}
			if !fail {
				row.status = "success"
				if s.Type == "http" && s.Profile != External {
					row.http = 200
				} else if s.Type == "http" {
					row.http = 200
				}
				row.metrics = succMetrics
				if s.Type == "agent" && !at.Before(in.rawStart) {
					// Only raw rows carry metrics; the rest only feed rollups.
					row.metrics = agentSnapshot(s.Key, at)
				}
				return row
			}
			kind, code := profileKind, 0
			status, msg := "", ""
			if forceKind != "" {
				kind = forceKind
			} else if w != nil {
				code = w.http
				if w.err != "" {
					status, msg = w.status, w.err
				}
				if w.status == "error" && w.err == "" {
					kind = "timeout"
				}
			}
			if msg == "" {
				status, code, msg = failureFor(s, in.env, kind, code)
			}
			row.status, row.http, row.errMsg = status, code, msg
			switch {
			case kind == "timeout":
				row.latency = s.Timeout * 1000
			case status == "error" || s.Type == "tcp":
				row.latency = int(math.Min(float64(lat), 3)) + net
			case s.Type == "http":
				row.latency = 4 + r.IntN(25) + net
			}
			if s.Type == "push" || s.Type == "agent" {
				row.latency = -1
			}
			row.metrics = failureMetrics(s, in.env, msg)
			return row
		}

		jitter := time.Duration(r.Int64N(int64(900 * time.Millisecond)))
		var newState monitorstate.State
		var cf int
		if nLoc == 0 {
			fail := profileFail || w != nil
			row := mkRow(t.Add(jitter), -1, fail, "")
			record(row)
			tr := monitorstate.Apply(monitorstate.Snapshot{State: res.state, ConsecutiveFailures: res.cf}, fail, s.Threshold)
			newState, cf = tr.To, tr.ConsecutiveFailures
			res.lastAt = row.t
			if fail {
				lastWindow = w
				if curDown != nil {
					curDown.lastErr = row.errMsg
				}
			}
			if newState != res.state {
				open(newState, "result", row.t)
			}
			if newState == monitorstate.StateDown {
				if curDown == nil {
					curDown = &downPeriod{start: row.t, lastErr: row.errMsg}
					if lastWindow != nil {
						curDown.event, curDown.planned = lastWindow.event, lastWindow.planned
					}
				}
				curDown.failures = cf
			} else if curDown != nil {
				e := row.t
				curDown.end = &e
				res.downs = append(res.downs, *curDown)
				curDown = nil
			}
			res.state, res.cf = newState, cf
			continue
		}

		// Multi-location: one result per location, per-location machines,
		// then the spatial aggregate.
		var counts monitorstate.LocationCounts
		counts.Selected = nLoc
		var lastRow checkRow
		for li := 0; li < nLoc; li++ {
			fail := profileFail || w != nil
			loc := s.Locations[li]
			// A location that cannot reach anything times out on every
			// check, whatever the target does.
			locOut := in.mods.locDown(loc, t)
			edgeFail := !locOut && loc == "sin" && s.sinEdge() && !in.sinSince.IsZero() && !t.Before(in.sinSince)
			if edgeFail || locOut {
				fail = true
			}
			at := t.Add(jitter + time.Duration(li)*137*time.Millisecond)
			forceKind := ""
			if locOut {
				forceKind = "timeout"
			}
			row := mkRow(at, li, fail, forceKind)
			if edgeFail && w == nil && !profileFail {
				// What the mock's Singapore PoP serves live: a slow 504.
				row.status, row.http, row.latency = "failure", http.StatusGatewayTimeout, sinEdgeLatency(r)
				_, _, row.errMsg = failureFor(s, in.env, "", http.StatusGatewayTimeout)
				row.metrics = failureMetrics(s, in.env, row.errMsg)
			}
			record(row)
			tr := monitorstate.Apply(locStates[li], fail, s.Threshold)
			if tr.To != locStates[li].State {
				res.locs[li].since = at
			}
			locStates[li] = monitorstate.Snapshot{State: tr.To, ConsecutiveFailures: tr.ConsecutiveFailures}
			res.locs[li].state, res.locs[li].cf, res.locs[li].lastAt = tr.To, tr.ConsecutiveFailures, at
			if row.latency >= 0 {
				res.locs[li].lastLatency = row.latency
			}
			switch tr.To {
			case monitorstate.StateDown:
				counts.Down++
			case monitorstate.StateSuspect:
				counts.Suspect++
			case monitorstate.StateUp:
				counts.Up++
			}
			lastRow = row
			if fail {
				lastWindow = w
			}
		}
		newState = monitorstate.AggregateLocations(counts, max(1, s.Quorum))
		res.lastAt = lastRow.t
		if newState != res.state {
			open(newState, "result", lastRow.t)
		}
		if newState == monitorstate.StateDown {
			if curDown == nil {
				curDown = &downPeriod{start: lastRow.t, lastErr: lastRow.errMsg}
				if lastWindow != nil {
					curDown.event, curDown.planned = lastWindow.event, lastWindow.planned
				}
			}
			curDown.failures = counts.Down
			if lastRow.errMsg != "" {
				curDown.lastErr = lastRow.errMsg
			}
		} else if curDown != nil {
			e := lastRow.t
			curDown.end = &e
			res.downs = append(res.downs, *curDown)
			curDown = nil
		}
		res.state, res.cf = newState, 0
	}

	if !pausedAt.IsZero() && pausedAt.Before(in.now) {
		if curDown != nil {
			e := pausedAt
			curDown.end = &e
			res.downs = append(res.downs, *curDown)
			curDown = nil
		}
		open(monitorstate.StateUnknown, "pause", pausedAt)
		res.state, res.cf = monitorstate.StateUnknown, 0
	}
	if curDown != nil {
		res.downs = append(res.downs, *curDown)
	}
	return res
}

// planWindows builds a monitor's failure windows: scripted events and
// planned maintenance outages, plus random short outages and one-check
// blips.
func planWindows(s *Spec, hits []hit, eventStarts map[string]time.Time, start, now time.Time, quiet [][2]time.Time) []window {
	var ws []window
	for _, h := range hits {
		if h.Key != s.Key {
			continue
		}
		st := eventStarts[h.Event].Add(h.Off)
		ws = append(ws, window{start: st, end: st.Add(h.Dur), status: h.Status, http: h.HTTP, err: h.Err, event: h.Event, planned: h.Planned})
	}
	r := rngFor(s.Key, 2)
	days := now.Sub(start).Hours() / 24
	if s.Type == "group" || s.Profile == Paused && s.PausedAgo == 0 {
		return ws
	}
	interval := time.Duration(s.Interval) * time.Second
	lo := start.Add(24 * time.Hour)
	hi := now.Add(-3 * time.Hour)
	span := hi.Sub(lo)
	okAt := func(t time.Time, d time.Duration) bool {
		for _, q := range quiet {
			if t.Before(q[1]) && t.Add(d).After(q[0]) {
				return false
			}
		}
		for _, w := range ws {
			if t.Before(w.end.Add(time.Hour)) && t.Add(d).After(w.start.Add(-time.Hour)) {
				return false
			}
		}
		return true
	}
	// Batch-style push monitors (hourly and slower) only miss runs in
	// scripted events; a random "outage" would have to span several runs.
	if !s.NoOutages && s.Type != "agent" && !(s.Type == "push" && s.Interval > 900) {
		n := poisson(r, s.OutageRate*days/30)
		for i := 0; i < n; i++ {
			d := time.Duration(3+r.IntN(10)) * time.Minute
			if r.Float64() < 0.35 {
				d = time.Duration(12+r.IntN(24)) * time.Minute
			}
			if minD := time.Duration(s.Threshold+1) * interval; d < minD {
				d = minD + interval
			}
			for try := 0; try < 6; try++ {
				t := lo.Add(time.Duration(r.Int64N(int64(span))))
				if !okAt(t, d) {
					continue
				}
				w := window{start: t, end: t.Add(d)}
				switch r.IntN(4) {
				case 0:
					w.status, w.http = "failure", 502
				case 1:
					w.status = "error" // timeout
				case 2:
					w.status, w.http = "failure", 504
				default:
					w.status, w.http = "failure", 503
				}
				ws = append(ws, w)
				break
			}
		}
	}
	// One-check blips (suspect, never down) give the traces texture.
	if s.Threshold >= 2 && s.Type != "push" && s.Type != "agent" {
		n := poisson(r, 1.2*days/30)
		for i := 0; i < n; i++ {
			t := lo.Add(time.Duration(r.Int64N(int64(span))))
			if !okAt(t, interval) {
				continue
			}
			ws = append(ws, window{start: t, end: t.Add(interval - time.Second), status: "failure", http: 503})
		}
	}
	sort.Slice(ws, func(i, j int) bool { return ws[i].start.Before(ws[j].start) })
	return ws
}

func poisson(r *rand.Rand, lambda float64) int {
	l := math.Exp(-lambda)
	k, p := 0, 1.0
	for {
		p *= r.Float64()
		if p <= l {
			return k
		}
		k++
	}
}

// ── agent metrics ─────────────────────────────────────────────────────────

func agentSnapshot(key string, t time.Time) []byte {
	s := sampleHost(key, t)
	m := models.AgentMetrics{CPUPercent: s.cpu * 100, CPUCores: s.cores, MemoryUsed: uint64(s.memUsed), MemoryTotal: uint64(s.memTotal),
		SwapUsed: uint64(s.swapUsed), SwapTotal: uint64(s.swapTotal), LoadAvg1: s.load1, LoadAvg5: s.load5, LoadAvg15: s.load15,
		NetworkBytesIn: uint64(s.netRx), NetworkBytesOut: uint64(s.netTx), DiskReadBytes: uint64(s.diskR), DiskWriteBytes: uint64(s.diskW),
		ProcessCount: s.procs, UptimeSeconds: uint64(math.Max(0, s.uptime)), Timestamp: t.UTC()}
	mounts := make([]string, 0, len(s.fs))
	for mp := range s.fs {
		mounts = append(mounts, mp)
	}
	sort.Strings(mounts)
	for _, mp := range mounts {
		v := s.fs[mp]
		m.DiskUsed += uint64(v[0])
		m.DiskTotal += uint64(v[0] + v[1])
		m.DiskMounts = append(m.DiskMounts, models.DiskMount{Path: mp, Used: uint64(v[0]), Total: uint64(v[0] + v[1])})
	}
	b, _ := json.Marshal(m)
	return b
}
