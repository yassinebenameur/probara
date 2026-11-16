import { formatLossPct, formatRoundTrip, pluralize } from './format';
import { formatBytes } from './metrics';
import { PING_DEFAULT_COUNT, PING_DEFAULT_INTERVAL_MS } from './monitor-form/ping';
import { certificateExpiryLabel, certificateExpiryWarns } from './tls-cert';
import type {
  CheckResult,
  DBMetricsEnvelope,
  HTTPMetricsEnvelope,
  HTTPMonitorConfig,
  MongoDBMetrics,
  Monitor,
  PingMetricsEnvelope,
  PingMonitorConfig,
  TCPMetricsEnvelope,
  TCPMonitorConfig,
  TLSCertInfo,
} from './types';

/** One row of the monitor page's Server panel; `key` is the React key. */
export interface ServerFact {
  key: string;
  label: string;
  value: string;
  tone?: 'warning' | 'note';
}

const isDatabaseMonitorType = (t: string): boolean =>
  t === 'redis' || t === 'postgres' || t === 'mongodb' || t === 'rabbitmq' || t === 'mysql';

// Type-specific facts for the monitor page's Server panel: what the database
// or broker reported about itself, the TCP TLS version, the certificate an
// HTTPS or TLS-wrapped TCP check saw, and a ping's loss and round trip, read
// from the newest result that carries the type's metrics. SIP facts come from
// lib/sip-metrics.ts (sipFacts); components/monitors/detail/ServerFactRows.tsx
// renders both. Empty when the type has nothing to add.
export function serverFacts(monitor: Monitor, results: CheckResult[]): ServerFact[] {
  const rows: ServerFact[] = [];
  if (isDatabaseMonitorType(monitor.type)) {
    const latest = results.find((r) => {
      const md = r.metrics_data as DBMetricsEnvelope | undefined;
      return Boolean(md && md[monitor.type as keyof DBMetricsEnvelope]);
    });
    const dbMetrics = latest
      ? (latest.metrics_data as DBMetricsEnvelope)[monitor.type as keyof DBMetricsEnvelope]
      : null;
    if (dbMetrics) {
      const server = [
        [dbMetrics.product, dbMetrics.server_version].filter(Boolean).join(' '),
        dbMetrics.role,
        dbMetrics.replica_set ? `set ${dbMetrics.replica_set}` : '',
      ]
        .filter(Boolean)
        .join(', ');
      const mongo = monitor.type === 'mongodb' ? (dbMetrics as MongoDBMetrics) : null;
      const repl = mongo?.replication;
      const mongoRows = mongo
        ? [
            mongo.connected_clients != null && mongo.connections_available != null
              ? { label: 'Connections', value: `${mongo.connected_clients} current, ${mongo.connections_available} available` }
              : null,
            mongo.used_memory_bytes != null || mongo.mem_virtual_bytes != null
              ? {
                  label: 'Memory',
                  value: [
                    mongo.used_memory_bytes != null ? `${formatBytes(mongo.used_memory_bytes)} resident` : '',
                    mongo.mem_virtual_bytes != null ? `${formatBytes(mongo.mem_virtual_bytes)} virtual` : '',
                  ].filter(Boolean).join(', '),
                }
              : null,
            mongo.cache_used_bytes != null && mongo.cache_max_bytes != null
              ? {
                  label: 'Cache',
                  value: `${formatBytes(mongo.cache_used_bytes)} / ${formatBytes(mongo.cache_max_bytes)}${
                    mongo.cache_dirty_bytes != null ? `, ${formatBytes(mongo.cache_dirty_bytes)} dirty` : ''
                  }`,
                }
              : null,
            mongo.network_bytes_in != null && mongo.network_bytes_out != null
              ? { label: 'Network', value: `${formatBytes(mongo.network_bytes_in)} in, ${formatBytes(mongo.network_bytes_out)} out` }
              : null,
            repl
              ? {
                  label: 'Replication',
                  value: `${repl.members_healthy}/${repl.members_total} healthy${
                    repl.max_lag_seconds != null ? `, lag ${repl.max_lag_seconds.toFixed(1)}s` : ''
                  }${repl.primary ? `, primary ${repl.primary}` : ''}`,
                }
              : null,
          ].filter((row): row is { label: string; value: string } => row !== null)
        : [];
      const mongoWarnings = mongo
        ? [
            mongo.replication_lag_warn_seconds != null
              ? `replication lag over ${mongo.replication_lag_warn_seconds}s threshold`
              : null,
            repl && !repl.primary ? 'replica set has no primary' : null,
            repl && repl.members_healthy < repl.members_total ? 'unhealthy replica set member(s)' : null,
          ].filter((w): w is string => w !== null)
        : [];
      const mongoHints = (mongo?.unavailable ?? []).map((u) =>
        u.reason === 'unauthorized'
          ? 'Cluster checks skipped: grant clusterMonitor'
          : u.reason === 'not_replica_set'
            ? 'Replication: not a replica set'
            : 'Cluster checks temporarily unavailable'
      );
      // "Version", not "Server": the panel around it is already titled Server.
      if (server) rows.push({ key: 'server', label: 'Version', value: server });
      for (const row of mongoRows) {
        rows.push({ key: row.label, label: row.label, value: row.value });
      }
      if (dbMetrics.latency_warn_ms) {
        rows.push({
          key: 'latency-warn',
          label: 'Warning',
          tone: 'warning',
          value: `latency over ${dbMetrics.latency_warn_ms}ms threshold`,
        });
      }
      for (const warning of mongoWarnings) {
        rows.push({ key: warning, label: 'Warning', tone: 'warning', value: warning });
      }
      for (const hint of mongoHints) {
        rows.push({ key: hint, label: 'Note', tone: 'note', value: hint });
      }
    }
  }
  if (monitor.type === 'tcp') {
    const cfg = monitor.config as TCPMonitorConfig | undefined;
    const latest = results.find((r) => {
      const md = r.metrics_data as TCPMetricsEnvelope | undefined;
      return Boolean(md && md.tcp);
    });
    const tcpMetrics = latest ? (latest.metrics_data as TCPMetricsEnvelope).tcp : null;
    if (cfg?.use_tls) {
      rows.push({ key: 'tls', label: 'TLS', value: tcpMetrics?.tls_version || 'Enabled' });
    }
    const cert = certificateFact(tcpMetrics?.tls, cfg?.tls_min_days_valid);
    if (cert) rows.push(cert);
  }
  if (monitor.type === 'http') {
    const cfg = monitor.config as HTTPMonitorConfig | undefined;
    const latest = results.find((r) => Boolean((r.metrics_data as HTTPMetricsEnvelope | undefined)?.http?.tls));
    const cert = certificateFact((latest?.metrics_data as HTTPMetricsEnvelope | undefined)?.http?.tls, cfg?.tls_min_days_valid);
    if (cert) rows.push(cert);
  }
  if (monitor.type === 'ping') {
    const latest = results.find((r) => Boolean((r.metrics_data as PingMetricsEnvelope | undefined)?.ping));
    const ping = (latest?.metrics_data as PingMetricsEnvelope | undefined)?.ping;
    if (ping) rows.push(...pingFacts(ping, monitor.config as PingMonitorConfig | undefined));
  }
  return rows;
}

/**
 * The certificate row: when the earliest certificate of the chain expires
 * (an intermediate is named), a warning inside tls_min_days_valid, as the
 * tls_expiry alert sees it.
 */
function certificateFact(tls: TLSCertInfo | undefined, minDaysValid?: number): ServerFact | null {
  const value = tls ? certificateExpiryLabel(tls) : '';
  if (!tls || !value) return null;
  return { key: 'certificate', label: 'Certificate', value, tone: certificateExpiryWarns(tls, minDaysValid) ? 'warning' : undefined };
}

/** A ping's last measurement: loss, round trip, and the echo schedule used. */
function pingFacts(ping: NonNullable<PingMetricsEnvelope['ping']>, cfg?: PingMonitorConfig): ServerFact[] {
  const rows: ServerFact[] = [];
  const maxLoss = cfg?.max_packet_loss_pct;
  const lossFails = ping.received === 0 || (maxLoss !== undefined && ping.loss_pct > maxLoss);
  rows.push({
    key: 'loss',
    label: 'Packet loss',
    value: `${formatLossPct(ping.loss_pct)} (${ping.received}/${pluralize(ping.sent, 'echo', 'echoes')} answered)`,
    tone: lossFails ? 'warning' : undefined,
  });
  if (ping.rtt_avg_ms !== undefined) {
    const slow = cfg?.max_rtt_ms !== undefined && ping.rtt_avg_ms > cfg.max_rtt_ms;
    rows.push({
      key: 'rtt',
      label: 'Round trip',
      value: `min ${formatRoundTrip(ping.rtt_min_ms)} · avg ${formatRoundTrip(ping.rtt_avg_ms)} · max ${formatRoundTrip(ping.rtt_max_ms)}`,
      tone: slow ? 'warning' : undefined,
    });
  }
  const wanted = cfg?.interval_ms ?? PING_DEFAULT_INTERVAL_MS;
  const count = cfg?.count ?? PING_DEFAULT_COUNT;
  if (count > 1 && ping.interval_ms !== undefined && ping.interval_ms < wanted) {
    rows.push({
      key: 'echo-gap',
      label: 'Echo gap',
      value: `${ping.interval_ms} ms, shortened from ${wanted} ms to fit the timeout`,
      tone: 'note',
    });
  }
  return rows;
}
