import type { CheckResult, Monitor, VaultMetrics, VaultMonitorConfig } from '@/lib/types';
import CopyableTarget from '@/components/ui/CopyableTarget';

const ROLE_LABELS: Record<NonNullable<VaultMetrics['role']>, string> = {
  active: 'Active',
  standby: 'Standby',
  performance_standby: 'Performance standby',
  dr_secondary: 'DR secondary',
};

const rowClass = 'flex justify-between gap-3';
const labelClass = 'shrink-0 text-slate-400';
const valueClass = 'min-w-0 text-right text-slate-200';

function formatTTL(seconds: number): string {
  if (seconds <= 0) return 'No expiry';
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  if (days > 0) return `${days}d ${hours}h`;
  const minutes = Math.floor((seconds % 3600) / 60);
  return hours > 0 ? `${hours}h ${minutes}m` : `${minutes}m`;
}

/** Latest-check rows for a Vault result; shared by the details card and the form's test preview. */
export function VaultLatestCheck({ metrics, size = 'default' }: { metrics: VaultMetrics; size?: 'default' | 'compact' }) {
  const probe = metrics.token_probe;
  return (
    <div className={size === 'compact' ? 'space-y-2 text-xs' : 'space-y-2.5 text-sm'}>
      <div className={rowClass}>
        <span className={labelClass}>Seal</span>
        <span className={metrics.sealed ? 'text-rose-300' : metrics.sealed === false ? 'text-emerald-300' : valueClass}>
          {metrics.sealed === undefined ? '—' : metrics.sealed ? 'Sealed' : 'Unsealed'}
          {metrics.initialized === false && ' · not initialized'}
        </span>
      </div>
      {metrics.role && (
        <div className={rowClass}>
          <span className={labelClass}>Role</span>
          <span className={valueClass}>{ROLE_LABELS[metrics.role] ?? metrics.role}</span>
        </div>
      )}
      {(metrics.version || metrics.cluster_name) && (
        <div className={rowClass}>
          <span className={labelClass}>Version</span>
          <span className={`${valueClass} font-mono`}>
            {metrics.version || '—'}
            {metrics.cluster_name && <span className="text-slate-400"> · {metrics.cluster_name}</span>}
          </span>
        </div>
      )}
      <div className={rowClass}>
        <span className={labelClass}>Audited request</span>
        {probe ? (
          <span className={probe.ok ? 'text-emerald-300' : 'text-rose-300'}>
            {probe.ok ? 'OK' : `HTTP ${probe.http_status}`}
            <span className="ml-2 text-slate-400">{probe.latency_ms} ms</span>
          </span>
        ) : (
          <span className="text-slate-500">Not run</span>
        )}
      </div>
      {probe?.ok && probe.ttl_seconds !== undefined && (
        <div className={rowClass}>
          <span className={labelClass}>Token TTL</span>
          <span className={valueClass}>{formatTTL(probe.ttl_seconds)}</span>
        </div>
      )}
    </div>
  );
}

interface VaultDetailsProps {
  monitor: Monitor;
  result?: CheckResult;
  /** `compact` matches the text-xs detail panels; `default` matches the text-sm configuration card. */
  size?: 'default' | 'compact';
}

// Configuration + latest-check summary for a Vault monitor, in the same
// two-column row layout as the other configuration cards.
export default function VaultDetails({ monitor, result, size = 'default' }: VaultDetailsProps) {
  const config = monitor.config as VaultMonitorConfig;
  const metrics = (result?.metrics_data as { vault?: VaultMetrics } | undefined)?.vault;
  const hasToken = Boolean(config.token);

  return (
    <div className={size === 'compact' ? 'space-y-2 text-xs' : 'space-y-2.5 text-sm'}>
      <div className={rowClass}>
        <span className={labelClass}>Address</span>
        <CopyableTarget value={config.url} label="Vault address" size="sm" className="min-w-0 justify-end" />
      </div>
      <div className={rowClass}>
        <span className={labelClass}>Checks</span>
        <span className={valueClass}>{hasToken ? 'sys/health + audited token lookup' : 'sys/health only'}</span>
      </div>
      {!hasToken && (
        <p className="text-amber-300">No token: audit-device failures are not detected.</p>
      )}
      {config.namespace && (
        <div className={rowClass}>
          <span className={labelClass}>Namespace</span>
          <span className={`${valueClass} font-mono`}>{config.namespace}</span>
        </div>
      )}
      <div className={rowClass}>
        <span className={labelClass}>Standby nodes</span>
        <span className={valueClass}>{config.require_active ? 'Down (active required)' : 'Healthy'}</span>
      </div>
      {config.token_min_ttl_seconds ? (
        <div className={rowClass}>
          <span className={labelClass}>Token expiry warning</span>
          <span className={valueClass}>under {formatTTL(config.token_min_ttl_seconds)}</span>
        </div>
      ) : null}
      {metrics && <VaultLatestCheck metrics={metrics} size={size} />}
    </div>
  );
}
