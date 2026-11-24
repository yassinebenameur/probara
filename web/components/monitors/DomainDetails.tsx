import type { CheckResult, DomainInfo, DomainMonitorConfig, Monitor } from '@/lib/types';
import CopyableTarget from '@/components/ui/CopyableTarget';
import RelativeTime from '@/components/ui/RelativeTime';
import { domainExpiryState, domainFacts, domainWarnDays } from '@/lib/domain-expiry';
import { UPTIME_TONE_TEXT } from '@/lib/status-tones';
import { pluralize } from '@/lib/format';

interface DomainDetailsProps {
  monitor: Monitor;
  /** Newest first, as the results endpoint returns them. */
  results: CheckResult[];
  /** `compact` matches the text-xs detail panels; `default` matches the text-sm configuration card. */
  size?: 'default' | 'compact';
}

/** The newest result that carries a registration snapshot. */
function latestDomainResult(results: CheckResult[]): { result: CheckResult; info: DomainInfo } | null {
  for (const result of results) {
    const info = (result.metrics_data as { domain?: DomainInfo } | undefined)?.domain;
    if (info) return { result, info };
  }
  return null;
}

// Registration summary for a domain monitor: what it watches, the warning
// window, and what the newest lookup found (expiry, registrar, source, or why
// the expiry is unknown). Label/value rows, so it slots into the
// configuration cards like PrometheusDetails.
export default function DomainDetails({ monitor, results, size = 'default' }: DomainDetailsProps) {
  const config = monitor.config as DomainMonitorConfig;
  const warnDays = domainWarnDays(config);
  const latest = latestDomainResult(results);
  const rowClass = 'flex justify-between gap-3';
  const labelClass = 'shrink-0 text-slate-400';
  const valueClass = 'min-w-0 text-right text-slate-200';
  const state = latest ? domainExpiryState(latest.info, warnDays) : null;
  const lookedUp = latest?.info.domain;

  return (
    <div className={size === 'compact' ? 'space-y-2 text-xs' : 'space-y-2.5 text-sm'}>
      <div className={rowClass}>
        <span className={labelClass}>Domain</span>
        <CopyableTarget value={config.domain} label="domain" size="sm" className="min-w-0 justify-end" />
      </div>
      {lookedUp && lookedUp !== config.domain.trim().toLowerCase().replace(/\.$/, '') && (
        <div className={rowClass}>
          <span className={labelClass}>Registered name</span>
          <span className={`${valueClass} font-mono`}>{lookedUp}</span>
        </div>
      )}
      <div className={rowClass}>
        <span className={labelClass}>Warns</span>
        <span className={valueClass}>{warnDays === 0 ? 'once expired' : `${pluralize(warnDays, 'day')} before expiry`}</span>
      </div>
      {latest && state ? (
        <>
          <div className={rowClass}>
            <span className={labelClass}>Registration</span>
            <span className={`${valueClass} ${UPTIME_TONE_TEXT[state.tone]}`}>{state.headline}</span>
          </div>
          {state.reason && (
            <p className={`${state.tone === 'none' ? 'text-slate-400' : 'text-amber-300'} ${size === 'compact' ? 'text-2xs' : 'text-xs'}`}>
              {state.reason}
            </p>
          )}
          {domainFacts(latest.info).map((fact) => (
            <div key={fact.label} className={rowClass}>
              <span className={labelClass}>{fact.label}</span>
              <span className={`${fact.warning ? 'min-w-0 text-right text-amber-300' : valueClass} break-words`}>{fact.value}</span>
            </div>
          ))}
          <div className={rowClass}>
            <span className={labelClass}>Looked up</span>
            <span className={valueClass}>
              <RelativeTime value={latest.result.created_at} />
            </span>
          </div>
        </>
      ) : (
        <div className={rowClass}>
          <span className={labelClass}>Registration</span>
          <span className="text-right text-slate-500">no lookup yet</span>
        </div>
      )}
    </div>
  );
}
