package main

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"time"
)

// Everything here is a pure function of (key, time), so the live mock and
// the history draw the same curve: load and drift continue across the seam
// between backfilled and live checks. The scripted past (regressions, the
// sale, bad days) lives in histMods and only reaches the history.

var london = func() *time.Location {
	if l, err := time.LoadLocation("Europe/London"); err == nil {
		return l
	}
	return time.UTC
}()

// hash64 hashes its parts (strings and integers) with FNV-1a, finished
// with the splitmix64 mixer: FNV alone barely moves its high bits when only
// the tail of the input changes (day 20341 vs 20342). It is on the hot path
// of the history (several calls per simulated check), hence no fmt.
func hash64(parts ...any) uint64 {
	const prime = 1099511628211
	x := uint64(14695981039346656037)
	num := func(v uint64) {
		for i := 0; i < 8; i++ {
			x ^= v & 0xff
			x *= prime
			v >>= 8
		}
	}
	for _, p := range parts {
		switch v := p.(type) {
		case string:
			for i := 0; i < len(v); i++ {
				x ^= uint64(v[i])
				x *= prime
			}
		case int:
			num(uint64(v))
		case int64:
			num(uint64(v))
		case uint64:
			num(v)
		case time.Duration:
			num(uint64(v))
		default:
			panic(fmt.Sprintf("hash64: unsupported %T", p))
		}
		x ^= 0xff // part separator
		x *= prime
	}
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	return x ^ x>>31
}

// unit is a deterministic uniform draw in [0,1).
func unit(parts ...any) float64 { return float64(hash64(parts...)>>11) / (1 << 53) }

// gauss is a deterministic standard-normal draw (Box-Muller on two
// uniforms derived from one hash).
func gauss(parts ...any) float64 {
	h := hash64(parts...)
	h2 := h*0x9e3779b97f4a7c15 + 0x632be59bd9b4e019
	h2 ^= h2 >> 29
	h2 *= 0xbf58476d1ce4e5b9
	h2 ^= h2 >> 32
	u1 := (float64(h>>11) + 0.5) / (1 << 53)
	u2 := float64(h2>>11) / (1 << 53)
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}

// valueNoise is smooth noise (about N(0,1)) with knots every period: the
// shape of a slow random walk that can be evaluated at any instant.
func valueNoise(key string, t time.Time, period time.Duration) float64 {
	x := float64(t.Unix()) / period.Seconds()
	k := math.Floor(x)
	f := x - k
	f = f * f * (3 - 2*f)
	a, b := gauss(key, period, int64(k)), gauss(key, period, int64(k)+1)
	return a + (b-a)*f
}

// dayShape is UK retail traffic over a day (0..1): overnight trough, a
// lunchtime peak and the evening shopping peak.
func dayShape(t time.Time) float64 {
	lt := t.In(london)
	h := float64(lt.Hour()) + float64(lt.Minute())/60
	v := 0.06 + 0.8*math.Exp(-math.Pow((h-13)/4.2, 2)) + 0.6*math.Exp(-math.Pow((h-20.5)/2.2, 2))
	return math.Min(1, v)
}

// weekdayFactor is how busy a day is: weekdays carry the business-hours
// load, weekends are quieter (Sunday evening shopping aside).
func weekdayFactor(t time.Time) float64 {
	switch t.In(london).Weekday() {
	case time.Saturday:
		return 0.62
	case time.Sunday:
		return 0.7
	case time.Friday:
		return 0.92
	case time.Monday:
		return 1.05
	}
	return 1
}

// trafficLoad is the customer load at t (0..~1.05).
func trafficLoad(t time.Time) float64 { return dayShape(t) * weekdayFactor(t) }

// loadSensitivity is how much a target slows down under full load.
func loadSensitivity(s *Spec) float64 {
	switch {
	case s.Profile == External:
		return 0.05
	case s.Profile == Slow:
		return 0.55
	}
	switch s.Type {
	case "http", "synthetic_api", "websocket", "synthetic_browser":
		return 0.35
	case "postgres", "mysql", "mongodb", "redis", "rabbitmq", "grpc":
		return 0.22
	case "sip":
		return 0.15
	case "dns", "ping":
		return 0.05
	}
	return 0
}

// walkFactor is a monitor's slow drift: a few-day wander, a two-week
// wander, and a small fleet-wide component (shared infrastructure).
func walkFactor(key string, t time.Time) float64 {
	return math.Exp(0.07*valueNoise(key+"/w3", t, 3*24*time.Hour) +
		0.09*valueNoise(key+"/w13", t, 13*24*time.Hour) +
		0.045*valueNoise("fleet", t, 6*24*time.Hour))
}

// slowFactor is a monitor's drift times its hour-to-hour wander, at hour
// resolution. One memo entry is enough: the history asks for one monitor's
// hours in order, and the live mock rarely asks at all.
func slowFactor(key string, t time.Time) float64 {
	h := t.Unix() / 3600
	slowMemo.Lock()
	if slowMemo.key == key && slowMemo.hour == h {
		v := slowMemo.v
		slowMemo.Unlock()
		return v
	}
	slowMemo.Unlock()
	v := walkFactor(key, time.Unix(h*3600+1800, 0)) * math.Exp(gauss(key, "hour", h)*0.06)
	slowMemo.Lock()
	slowMemo.key, slowMemo.hour, slowMemo.v = key, h, v
	slowMemo.Unlock()
	return v
}

var slowMemo struct {
	sync.Mutex
	key  string
	hour int64
	v    float64
}

// flakyRateAt is a Flaky target's failure probability on t's day: some
// days are clean, most hover around 2%, a few are bad.
func flakyRateAt(key string, t time.Time) float64 {
	day := t.Unix() / 86400
	if unit(key, "clean", day) < 0.28 {
		return 0
	}
	return math.Min(0.08, flakyRate*math.Exp(0.75*gauss(key, "flaky", day)))
}

// locLatency is the extra network time (ms) of a check from a location.
func locLatency(loc string, r *rand.Rand) int {
	l, ok := locationByKey(loc)
	if !ok || l.OriginRTT == 0 {
		return 0
	}
	v := float64(l.OriginRTT) * math.Exp(r.NormFloat64()*0.06)
	if r.Float64() < 0.01 {
		v += float64(l.OriginRTT) * (0.3 + r.Float64()) // a retransmit
	}
	return int(math.Round(v))
}

// ── the scripted past (history only) ─────────────────────────────────────

type regression struct {
	key      string // route owner (latSpec) key, so every monitor of the path agrees
	from, to time.Time
	factor   float64
}

type elevation struct {
	keys     []string
	from, to time.Time
	rate     float64 // extra per-check failure probability
}

type locOutage struct {
	loc      string
	from, to time.Time
}

type histMods struct {
	regressions []regression
	sale        [2]time.Time // seasonal traffic bump
	saleLoad    float64
	elevated    []elevation
	locOutages  []locOutage
}

// latFactor scales a check's latency for the scripted past.
func (m *histMods) latFactor(s *Spec, t time.Time) float64 {
	if m == nil {
		return 1
	}
	f := 1.0
	for _, g := range m.regressions {
		if g.key == s.Key && !t.Before(g.from) && t.Before(g.to) {
			f *= g.factor
		}
	}
	if !t.Before(m.sale[0]) && t.Before(m.sale[1]) {
		f *= 1 + loadSensitivity(s)*m.saleLoad*dayShape(t)
	}
	return f
}

// extraFailRate is the scripted bad-days failure probability of a monitor.
func (m *histMods) extraFailRate(key string, t time.Time) float64 {
	if m == nil {
		return 0
	}
	for _, e := range m.elevated {
		if t.Before(e.from) || !t.Before(e.to) {
			continue
		}
		for _, k := range e.keys {
			if k == key {
				return e.rate
			}
		}
	}
	return 0
}

// locDown reports whether a location could not reach anything at t.
func (m *histMods) locDown(loc string, t time.Time) bool {
	if m == nil {
		return false
	}
	for _, o := range m.locOutages {
		if o.loc == loc && !t.Before(o.from) && t.Before(o.to) {
			return true
		}
	}
	return false
}

// planMods scripts the past that gives the history its texture. Every
// window ends at least a day before now, so live checks never see it.
func (w *world) planMods() *histMods {
	h := time.Hour
	d := 24 * h
	return &histMods{
		// Deploys that slowed a service down until the fix shipped.
		regressions: []regression{
			{key: "identity-api", from: w.at(58, 11, 5), to: w.at(53, 16, 40), factor: 1.5},
			{key: "recs-api", from: w.at(44, 9, 30), to: w.at(40, 6, 10), factor: 1.4},
			{key: "product-api", from: w.at(37, 14, 20), to: w.at(34, 16, 5), factor: 1.7},
			{key: "search-ranking", from: w.at(27, 13, 0), to: w.at(24, 10, 45), factor: 1.32},
			{key: "search-api", from: w.at(19, 10, 5), to: w.at(15, 15, 30), factor: 1.45},
			{key: "checkout-api", from: w.at(8, 15, 40), to: w.at(6, 16, 50), factor: 1.35},
		},
		// The autumn sale: four days of heavier traffic.
		sale:     [2]time.Time{w.at(31, 6, 0), w.at(31, 6, 0).Add(4 * d)},
		saleLoad: 0.6,
		elevated: []elevation{
			{keys: []string{"carrier-dpd", "carrier-royalmail", "tracking-api"}, from: w.at(16, 7, 0), to: w.at(13, 19, 0), rate: 0.025},
			{keys: []string{"payment-paypal"}, from: w.at(41, 8, 0), to: w.at(39, 22, 0), rate: 0.02},
			{keys: []string{"search-suggest", "sf-search"}, from: w.at(19, 10, 5), to: w.at(17, 9, 0), rate: 0.012},
			{keys: []string{"sf-images", "sf-cdn"}, from: w.at(31, 18, 0), to: w.at(30, 23, 0), rate: 0.015},
		},
		// us-east-1 lost its route to the origin for half an hour: every
		// check from Virginia timed out while the others stayed green.
		locOutages: []locOutage{{loc: "iad", from: w.at(11, 3, 20), to: w.at(11, 3, 58)}},
	}
}
