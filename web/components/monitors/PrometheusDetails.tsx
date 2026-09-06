import type { CheckResult, Monitor, PrometheusMonitorConfig, PrometheusMetrics } from '@/lib/types';
import CopyableTarget from '@/components/ui/CopyableTarget';

const OPERATORS: Record<PrometheusMonitorConfig['operator'], string> = {
  gt: '>', gte: '≥', lt: '<', lte: '≤', eq: '=', ne: '≠',
};

const NO_DATA_LABELS: Record<NonNullable<PrometheusMonitorConfig['no_data_status']>, string> = {
  failure: 'Down', error: 'Error', success: 'Up',
};

const AUTH_LABELS: Record<NonNullable<PrometheusMonitorConfig['auth_type']>, string> = {
  none: 'None', basic: 'Basic', bearer: 'Bearer token',
};

interface PrometheusDetailsProps {
  monitor: Monitor;
  result?: CheckResult;
  /** `compact` matches the text-xs detail panels; `default` matches the text-sm configuration card. */
  size?: 'default' | 'compact';
}

// Configuration + latest-sample summary for a Prometheus monitor. Rendered as
// label/value rows so it slots into the existing configuration cards, which
// all use the same two-column layout.
export default function PrometheusDetails({ monitor, result, size = 'default' }: PrometheusDetailsProps) {
  const config = monitor.config as PrometheusMonitorConfig;
  const metrics = (result?.metrics_data as { prometheus?: PrometheusMetrics } | undefined)?.prometheus;
  const rowClass = 'flex justify-between gap-3';
  const labelClass = 'shrink-0 text-slate-500';
  const valueClass = 'min-w-0 text-right text-slate-300';
  const truncated = metrics ? metrics.sample_count > metrics.values.length : false;

  return (
    <div className={`space-y-2 ${size === 'compact' ? 'text-xs' : 'text-sm'}`}>
      <div className={rowClass}>
        <span className={labelClass}>Endpoint</span>
        <CopyableTarget value={config.url} label="Prometheus URL" size={size === 'compact' ? 'xs' : 'sm'} className="min-w-0 justify-end" />
      </div>
      <div>
        <span className={labelClass}>Query</span>
        <pre className="mt-1 whitespace-pre-wrap break-all rounded-lg border border-white/[0.06] bg-slate-900/60 p-2.5 font-mono text-xs text-slate-200">
          {config.query}
        </pre>
      </div>
      <div className={rowClass}>
        <span className={labelClass}>Healthy when</span>
        <span className={valueClass}>
          every value <span className="font-mono">{OPERATORS[config.operator] ?? config.operator} {config.threshold}</span>
        </span>
      </div>
      <div className={rowClass}>
        <span className={labelClass}>No samples</span>
        <span className={valueClass}>{NO_DATA_LABELS[config.no_data_status ?? 'failure']}</span>
      </div>
      <div className={rowClass}>
        <span className={labelClass}>Authentication</span>
        <span className={valueClass}>{AUTH_LABELS[config.auth_type ?? 'none']}</span>
      </div>
      {metrics && (
        <>
          <div className={rowClass}>
            <span className={labelClass}>Latest check</span>
            <span className={valueClass}>
              {metrics.sample_count} {metrics.sample_count === 1 ? 'sample' : 'samples'}
              {' · '}
              <span className={metrics.failed_count > 0 ? 'text-red-400' : 'text-emerald-400'}>
                {metrics.failed_count} outside threshold
              </span>
            </span>
          </div>
          {metrics.values.length > 0 && (
            <div>
              <span className={labelClass}>Values{truncated ? ` (first ${metrics.values.length} of ${metrics.sample_count})` : ''}</span>
              <p className="mt-1 break-all font-mono text-xs text-slate-300">{metrics.values.join(', ')}</p>
            </div>
          )}
        </>
      )}
    </div>
  );
}
