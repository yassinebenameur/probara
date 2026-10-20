package main

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// Host metrics for the agent monitors, shared by the 24h backfill and the
// live OTLP loop. Every value is a pure function of (host, time), like the
// check latencies, so the live series continues the seeded one.

type diskProfile struct {
	total     float64       // bytes
	low, high float64       // utilisation: fills from low to high, then a cleanup
	cycle     time.Duration // fill + cleanup period
	phase     float64       // 0..1 offset into the cycle
}

type hostProfile struct {
	cores    int
	memTotal float64
	// CPU utilisation: idle floor plus the share that follows traffic, and
	// how high an occasional spike (a cron, a GC storm, a deploy) reaches.
	cpuIdle, cpuPeak, spikeMax float64
	// Memory: ratio after a (re)start and how much it grows before the next
	// one. Restarts are deploys, shared by a restartGroup and staggered by
	// restartLag; 0 = a long-lived process (page cache, no leak).
	memBase, memGrowth float64
	restartEvery       time.Duration
	restartGroup       string
	restartLag         time.Duration
	swapTotal          float64
	swapRatio          float64
	disk               map[string]diskProfile
	netRate, diskRate  float64 // average bytes/s
	nightly            bool    // 02:00 backup + vacuum (CPU, disk reads, network out)
	processes          int
	bootDaysAgo        float64
}

var hosts = map[string]hostProfile{
	"host-web-01": {cores: 8, memTotal: 32 << 30, cpuIdle: 0.07, cpuPeak: 0.46, spikeMax: 0.35,
		memBase: 0.46, memGrowth: 0.19, restartEvery: 61 * time.Hour, restartGroup: "web", swapTotal: 4 << 30, swapRatio: 0.03,
		disk:    map[string]diskProfile{"/": {total: 200 << 30, low: 0.39, high: 0.44, cycle: 7 * 24 * time.Hour, phase: 0.3}},
		netRate: 9e6, diskRate: 2e6, processes: 214, bootDaysAgo: 41},
	"host-web-02": {cores: 8, memTotal: 32 << 30, cpuIdle: 0.06, cpuPeak: 0.43, spikeMax: 0.35,
		memBase: 0.44, memGrowth: 0.2, restartEvery: 61 * time.Hour, restartGroup: "web", restartLag: 7 * time.Minute, swapTotal: 4 << 30, swapRatio: 0.025,
		disk:    map[string]diskProfile{"/": {total: 200 << 30, low: 0.37, high: 0.42, cycle: 7 * 24 * time.Hour, phase: 0.32}},
		netRate: 8.5e6, diskRate: 1.8e6, processes: 209, bootDaysAgo: 41},
	// The orders DB keeps its WAL/data volume just under the 85% rule
	// (system.filesystem.utilization on /var/lib/postgresql): it fills over
	// the week and the Sunday partition drop brings it back.
	"host-db-orders": {cores: 16, memTotal: 128 << 30, cpuIdle: 0.13, cpuPeak: 0.33, spikeMax: 0.24,
		memBase: 0.79, memGrowth: 0.025, swapTotal: 8 << 30, swapRatio: 0.008,
		disk: map[string]diskProfile{
			"/":                   {total: 100 << 30, low: 0.31, high: 0.34, cycle: 3 * 24 * time.Hour, phase: 0.6},
			"/var/lib/postgresql": {total: 2 << 40, low: 0.806, high: 0.843, cycle: 7 * 24 * time.Hour, phase: 0.55},
		},
		netRate: 22e6, diskRate: 40e6, nightly: true, processes: 388, bootDaysAgo: 96},
	"host-sbc-01": {cores: 4, memTotal: 16 << 30, cpuIdle: 0.05, cpuPeak: 0.3, spikeMax: 0.2,
		memBase: 0.58, memGrowth: 0.15, restartEvery: 84 * time.Hour, restartGroup: "sbc", swapTotal: 2 << 30, swapRatio: 0.06,
		disk:    map[string]diskProfile{"/": {total: 80 << 30, low: 0.5, high: 0.55, cycle: 24 * time.Hour, phase: 0.1}},
		netRate: 3e6, diskRate: 0.4e6, processes: 141, bootDaysAgo: 12},
}

// hostSample is one deterministic reading of a host at t.
type hostSample struct {
	cpu, memUsed, memTotal, swapUsed, swapTotal float64
	load1, load5, load15                        float64
	fs                                          map[string][2]float64 // used, free
	netRx, netTx, diskR, diskW                  float64               // cumulative
	procs                                       int
	cores                                       int
	uptime                                      float64
}

func frac(x float64) float64 { return x - math.Floor(x) }

// nightlyJob is 1 while the 02:00 (London) backup runs, ramping at the ends.
func nightlyJob(t time.Time) float64 {
	lt := t.In(london)
	m := float64(lt.Hour()*60+lt.Minute()) + float64(lt.Second())/60
	switch {
	case m < 120 || m > 168:
		return 0
	case m < 124:
		return (m - 120) / 4
	case m > 160:
		return (168 - m) / 8
	}
	return 1
}

// spikeAt is the height (0..1) of a short CPU spike at t, if one runs:
// about one every six hours, 2-7 minutes long.
func spikeAt(key string, t time.Time) float64 {
	const bucket = 15 * 60
	sec := t.Unix()
	for b := sec/bucket - 1; b <= sec/bucket; b++ {
		if unit(key, "spike", b) >= 0.04 {
			continue
		}
		from := b*bucket + int64(unit(key, "spike-at", b)*bucket)
		dur := int64(120 + unit(key, "spike-len", b)*300)
		if sec >= from && sec < from+dur {
			x := float64(sec-from) / float64(dur)
			return (0.5 + 0.5*unit(key, "spike-amp", b)) * math.Min(1, 4*x) * math.Min(1, 3*(1-x)+0.2)
		}
	}
	return 0
}

func cpuAt(key string, p hostProfile, t time.Time) float64 {
	v := p.cpuIdle + p.cpuPeak*trafficLoad(t)
	v *= math.Exp(0.12 * valueNoise(key+"/cpu", t, 40*time.Minute))
	v += 0.018 * gauss(key, "cpu", t.Unix()/60)
	v += p.spikeMax * spikeAt(key, t)
	if p.nightly {
		v += 0.22 * nightlyJob(t)
	}
	return math.Min(0.97, math.Max(0.01, v))
}

func memAt(key string, p hostProfile, t time.Time) float64 {
	v := p.memBase
	sec := float64(t.Unix())
	if p.restartEvery > 0 {
		// A deploy restarts the app: memory drops back to the base and
		// creeps up again (caches warming, a slow leak).
		ph := frac((sec-p.restartLag.Seconds())/p.restartEvery.Seconds() + unit(p.restartGroup, "deploy"))
		v += p.memGrowth * math.Pow(ph, 0.8)
	} else {
		v += p.memGrowth * (0.5 + 0.5*valueNoise(key+"/cache", t, 9*time.Hour))
	}
	// Garbage collection: a small sawtooth every ten-odd minutes.
	gc := 600 + unit(key, "gc")*420
	v += 0.02 * frac(sec/gc+unit(key, "gc-phase"))
	v += 0.03 * trafficLoad(t)
	v += 0.003 * gauss(key, "mem", t.Unix()/60)
	return math.Min(0.97, v)
}

func sampleHost(key string, t time.Time) hostSample {
	p := hosts[key]
	cores := float64(p.cores)
	cpu := cpuAt(key, p, t)
	mean := func(n int) float64 {
		sum := 0.0
		for i := 0; i < n; i++ {
			sum += cpuAt(key, p, t.Add(-time.Duration(i)*time.Minute))
		}
		return sum / float64(n)
	}
	// Load counts runnable and I/O-blocked threads: CPU demand plus the
	// backup's I/O wait on the database host.
	iow := 0.0
	if p.nightly {
		iow = 0.15 * cores * nightlyJob(t)
	}
	mem := memAt(key, p, t)
	s := hostSample{cpu: cpu, memTotal: p.memTotal, memUsed: mem * p.memTotal, swapTotal: p.swapTotal,
		swapUsed: p.swapTotal * p.swapRatio * math.Exp(0.25*valueNoise(key+"/swap", t, 30*time.Hour)),
		procs:    p.processes + int(math.Round(14*trafficLoad(t)+2.5*gauss(key, "procs", t.Unix()/60))),
		cores:    p.cores, fs: map[string][2]float64{}}
	s.load1 = math.Max(0, cpu*cores*1.06+iow+0.35*gauss(key, "load", t.Unix()/60))
	s.load5 = mean(5)*cores*1.04 + iow*0.9
	s.load15 = mean(15)*cores*1.02 + iow*0.7
	for m, d := range p.disk {
		used := d.low + (d.high-d.low)*frac(float64(t.Unix())/d.cycle.Seconds()+d.phase)
		s.fs[m] = [2]float64{used * d.total, (1 - used) * d.total}
	}
	boot := hostBoot(p)
	s.netRx = ioCounter(key, "net-rx", p.netRate*0.6, boot, t, false)
	s.netTx = ioCounter(key, "net-tx", p.netRate*0.4, boot, t, p.nightly)
	s.diskR = ioCounter(key, "disk-r", p.diskRate*0.3, boot, t, p.nightly)
	s.diskW = ioCounter(key, "disk-w", p.diskRate*0.7, boot, t, false)
	s.uptime = t.Sub(boot).Seconds()
	return s
}

func hostBoot(p hostProfile) time.Time {
	return time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC).Add(-time.Duration(p.bootDaysAgo * float64(24*time.Hour)))
}

// ── cumulative I/O counters ──────────────────────────────────────────────

// Monotonic byte counters since boot. Each UTC day carries a fixed total
// (the average rate, weighted by weekday), spread over its minutes by the
// traffic shape, noise and bursts; so the value at any instant is the sum
// of the whole days so far plus today's share, with no integration from
// boot and no step at midnight.

var weekdayWeights = func() [7]float64 {
	var w [7]float64
	sum := 0.0
	for d := 0; d < 7; d++ {
		// 2026-10-04 is a Sunday: day d of that week.
		w[d] = weekdayFactor(time.Date(2026, 10, 4+d, 12, 0, 0, 0, time.UTC))
		sum += w[d]
	}
	for d := range w {
		w[d] *= 7 / sum
	}
	return w
}()

func utcWeekday(day int64) time.Weekday { return time.Unix(day*86400, 0).UTC().Weekday() }

var dayShares sync.Map // "key|name|day" -> []float64 cumulative share at each minute start (len 1441)

func dayCumShares(key, name string, day int64, nightly bool) []float64 {
	ck := fmt.Sprintf("%s|%s|%d", key, name, day)
	if v, ok := dayShares.Load(ck); ok {
		return v.([]float64)
	}
	w := make([]float64, 1440)
	sum := 0.0
	for i := range w {
		at := time.Unix(day*86400+int64(i)*60+30, 0)
		v := (0.22 + dayShape(at)) * math.Exp(0.22*gauss(key, name, day, i))
		if unit(key, name, "burst", day, i) < 0.015 {
			v *= 1.8 + 2.2*unit(key, name, "burst-x", day, i)
		}
		if nightly {
			v *= 1 + 9*nightlyJob(at)
		}
		w[i] = v
		sum += v
	}
	cum := make([]float64, 1441)
	for i, v := range w {
		cum[i+1] = cum[i] + v/sum
	}
	dayShares.Store(ck, cum)
	return cum
}

func ioCounter(key, name string, rate float64, boot, t time.Time, nightly bool) float64 {
	if !t.After(boot) {
		return 0
	}
	daily := rate * 86400
	first, today := boot.Unix()/86400, t.Unix()/86400
	total := 0.0
	n := today - first
	total += float64(n/7) * 7 * daily
	for d := first + n/7*7; d < today; d++ {
		total += daily * weekdayWeights[utcWeekday(d)]
	}
	sec := float64(t.Unix() - today*86400)
	i := int(sec / 60)
	cum := dayCumShares(key, name, today, nightly)
	share := cum[i] + (cum[i+1]-cum[i])*(sec/60-float64(i))
	return total + daily*weekdayWeights[utcWeekday(today)]*share
}
