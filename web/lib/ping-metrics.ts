import { formatLatency } from './format';
import type { CheckResult, PingMetrics, PingMetricsEnvelope } from './types';

/** One detail-page fact about the latest ping check. */
export interface PingFact {
  key: string;
  label: string;
  value: string;
  tone?: 'warning';
}

/** The newest result that carries a ping envelope (older results have none). */
export function latestPingMetrics(results: CheckResult[]): PingMetrics | null {
  for (const result of results) {
    const ping = (result.metrics_data as PingMetricsEnvelope | undefined)?.ping;
    if (ping) return ping;
  }
  return null;
}

// Sub-millisecond round trips are common on a LAN; formatLatency rounds to
// whole ms, so keep one decimal below 10 ms.
const rtt = (ms: number) => (ms < 10 ? `${ms.toFixed(1)} ms` : formatLatency(ms));

/** `2/3 answered (33.3% loss)`, round trips, and the warn flag. */
export function pingFacts(m: PingMetrics, maxLossPct?: number): PingFact[] {
  const facts: PingFact[] = [];
  if (m.ip && m.ip !== m.host) facts.push({ key: 'ip', label: 'Address', value: m.ip });
  const loss = Math.round(m.loss_pct * 10) / 10;
  const lossWarn = m.received === 0 || (maxLossPct !== undefined && m.loss_pct > maxLossPct);
  facts.push({
    key: 'loss',
    label: 'Packet loss',
    value: `${loss}% (${m.received}/${m.sent} answered)`,
    tone: lossWarn ? 'warning' : undefined,
  });
  if (m.rtt_avg_ms !== undefined) {
    const parts = [
      m.rtt_min_ms !== undefined ? `min ${rtt(m.rtt_min_ms)}` : '',
      `avg ${rtt(m.rtt_avg_ms)}`,
      m.rtt_max_ms !== undefined ? `max ${rtt(m.rtt_max_ms)}` : '',
    ].filter(Boolean);
    facts.push({ key: 'rtt', label: 'Round trip', value: parts.join(' · ') });
  }
  if (m.rtt_warn_ms) {
    facts.push({ key: 'rtt-warn', label: 'Warning', value: `average round trip over ${m.rtt_warn_ms} ms`, tone: 'warning' });
  }
  return facts;
}
