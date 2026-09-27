package otlp

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/yassinebenameur/probara/shared/models"
)

// buildLegacySnapshot renders the curated metrics of one accepted OTLP batch
// into the legacy AgentMetrics JSON shape stored on the heartbeat
// check_results row. TRANSITIONAL: it exists so the pre-OTel alerter
// (host_metrics.go DISTINCT ON metrics_data) and the current UI keep working
// mid-rollout; it is deleted once alerting/UI read the metric store. Fields
// whose series are absent from the batch stay zero (CPUPercent uses the
// legacy -1 "unavailable" sentinel).
func buildLegacySnapshot(points []seriesPoint, receivedAt time.Time) json.RawMessage {
	m := models.AgentMetrics{CPUPercent: -1, Timestamp: receivedAt}

	type fsTotals struct{ used, total float64 }
	fs := map[string]*fsTotals{}
	var memStates, swapStates map[string]float64

	maxTS := time.Time{}
	for _, p := range points {
		if p.TS.After(maxTS) {
			maxTS = p.TS
		}
		switch p.MetricName {
		case "system.cpu.utilization":
			if p.Attributes["state"] == "used" {
				m.CPUPercent = p.Value * 100
			}
		case "system.cpu.logical.count":
			m.CPUCores = int(p.Value)
		case "system.memory.usage":
			if memStates == nil {
				memStates = map[string]float64{}
			}
			memStates[p.Attributes["state"]] += p.Value
		case "system.paging.usage":
			if swapStates == nil {
				swapStates = map[string]float64{}
			}
			swapStates[p.Attributes["state"]] += p.Value
		case "system.filesystem.usage":
			mp := p.Attributes["mountpoint"]
			if fs[mp] == nil {
				fs[mp] = &fsTotals{}
			}
			if p.Attributes["state"] == "used" {
				fs[mp].used += p.Value
			}
			fs[mp].total += p.Value
		case "system.disk.io":
			switch p.Attributes["direction"] {
			case "read":
				m.DiskReadBytes += uint64(p.Value)
			case "write":
				m.DiskWriteBytes += uint64(p.Value)
			}
		case "system.network.io":
			switch p.Attributes["direction"] {
			case "receive":
				m.NetworkBytesIn += uint64(p.Value)
			case "transmit":
				m.NetworkBytesOut += uint64(p.Value)
			}
		case "system.cpu.load_average.1m":
			m.LoadAvg1 = p.Value
		case "system.cpu.load_average.5m":
			m.LoadAvg5 = p.Value
		case "system.cpu.load_average.15m":
			m.LoadAvg15 = p.Value
		case "system.processes.count":
			m.ProcessCount += int(p.Value)
		case "system.uptime":
			m.UptimeSeconds = uint64(p.Value)
		}
	}
	if !maxTS.IsZero() {
		m.Timestamp = maxTS
	}

	for state, v := range memStates {
		if state == "used" {
			m.MemoryUsed = uint64(v)
		}
		m.MemoryTotal += uint64(v)
	}
	for state, v := range swapStates {
		if state == "used" {
			m.SwapUsed = uint64(v)
		}
		m.SwapTotal += uint64(v)
	}
	mounts := make([]string, 0, len(fs))
	for mp := range fs {
		mounts = append(mounts, mp)
	}
	sort.Strings(mounts)
	for _, mp := range mounts {
		t := fs[mp]
		m.DiskUsed += uint64(t.used)
		m.DiskTotal += uint64(t.total)
		m.DiskMounts = append(m.DiskMounts, models.DiskMount{
			Path: mp, Used: uint64(t.used), Total: uint64(t.total),
		})
	}

	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return b
}
