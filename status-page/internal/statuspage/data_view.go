package statuspage

// publicPageData is the /data JSON for a page: the page data with every
// monitor field the page's settings hide left out, the same rule as
// summary.json. /data is public and readable by any client, so a setting
// that hides a figure on the page hides it here too:
//
//	show_monitor_uptime off  -> no uptime_1h / uptime_24h
//	show_monitor_url off     -> no url
//	show_latency_charts off  -> no avg_latency_1h / avg_latency_24h
//	show_monitor_tags off    -> no tags
//	show_monitor_tls off     -> no tls_days_until_expiry / tls_not_after
//
// The last check's latency stays: the page prints it on every row. data is
// not modified; the returned copy shares nothing that is changed.
func publicPageData(data *StatusPageData) *StatusPageData {
	out := *data
	out.Monitors = redactMonitors(data, data.Monitors)
	if data.Sections != nil {
		out.Sections = make([]StatusPageSectionData, len(data.Sections))
		for i, section := range data.Sections {
			section.Monitors = redactMonitors(data, section.Monitors)
			out.Sections[i] = section
		}
	}
	return &out
}

func redactMonitors(data *StatusPageData, monitors []MonitorStatus) []MonitorStatus {
	if monitors == nil {
		return nil
	}
	out := make([]MonitorStatus, len(monitors))
	for i, m := range monitors {
		if !data.ShowMonitorUptime {
			m.Uptime1h, m.Uptime24h = nil, nil
		}
		if !data.ShowMonitorURL {
			m.URL = ""
		}
		if !data.ShowLatencyCharts {
			m.AvgLatency1h, m.AvgLatency24h = nil, nil
		}
		if !data.ShowMonitorTags {
			m.Tags = nil
		}
		if !data.ShowMonitorTLS {
			m.TLSDaysUntilExpiry, m.TLSNotAfter = nil, ""
		}
		out[i] = m
	}
	return out
}
