'use client';

import Link from 'next/link';
import { Info } from 'lucide-react';
import {
  dayTone, formatSlaDuration, formatSlaPct, formatTarget, slaTone, type SlaReport, type SlaReportOutage,
} from '@/lib/sla';
import Panel from '@/components/ui/Panel';
import { AvailabilityFigure, BudgetBar, SlaStatusPill, TONE_BG, TONE_TEXT } from './SlaVisuals';

function formatInZone(iso: string, timeZone: string, withTime = true): string {
  try {
    return new Intl.DateTimeFormat(undefined, {
      timeZone,
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      ...(withTime ? { hour: '2-digit', minute: '2-digit' } : {}),
    }).format(new Date(iso));
  } catch {
    return new Date(iso).toLocaleString();
  }
}

/** Last included day of a half-open period: its end minus one second. */
function lastDay(iso: string): string {
  return new Date(new Date(iso).getTime() - 1000).toISOString();
}

function Tile({ label, children, hint }: { label: string; children: React.ReactNode; hint?: React.ReactNode }) {
  return (
    <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-4">
      <div className="text-[11px] font-medium uppercase tracking-wider text-slate-500">{label}</div>
      <div className="mt-1.5 text-2xl font-semibold text-white">{children}</div>
      {hint && <div className="mt-1 text-xs text-slate-500">{hint}</div>}
    </div>
  );
}

function OutageTable({ outages, timeZone, showMonitor }: { outages: SlaReportOutage[]; timeZone: string; showMonitor: boolean }) {
  if (outages.length === 0) return <p className="text-sm text-slate-500">No outages in this period.</p>;
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[560px] text-sm">
        <thead>
          <tr className="border-b border-white/[0.06] text-left text-[11px] uppercase tracking-wider text-slate-500">
            <th className="py-2 pr-4 font-medium">Start</th>
            <th className="py-2 pr-4 font-medium">End</th>
            <th className="py-2 pr-4 font-medium">Duration</th>
            <th className="py-2 pr-4 font-medium">Planned</th>
            {showMonitor && <th className="py-2 font-medium">Monitor</th>}
          </tr>
        </thead>
        <tbody>
          {outages.map((o, i) => (
            <tr key={`${o.monitor_id ?? 'svc'}-${o.start}-${i}`} className="border-b border-white/[0.04] last:border-0">
              <td className="py-2 pr-4 text-slate-300">
                {formatInZone(o.start, timeZone)}
                {o.started_before && <span className="ml-1 text-xs text-slate-500">(earlier)</span>}
              </td>
              <td className="py-2 pr-4 text-slate-300">
                {o.ongoing ? <span className="text-rose-300">ongoing</span> : formatInZone(o.end, timeZone)}
              </td>
              <td className="py-2 pr-4 font-mono text-slate-200">{formatSlaDuration(o.duration_seconds)}</td>
              <td className="py-2 pr-4 font-mono text-slate-400">
                {o.planned_seconds > 0 ? formatSlaDuration(o.planned_seconds) : '—'}
              </td>
              {showMonitor && (
                <td className="py-2 text-slate-300">
                  {o.monitor_id ? (
                    <Link href={`/monitors/${o.monitor_id}`} className="hover:text-cyan-300">{o.monitor_name}</Link>
                  ) : 'Service'}
                </td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function SlaReportView({ report }: { report: SlaReport }) {
  const { definition: def, summary, budget, period } = report;
  const tz = def.timezone;
  const running = !period.is_closed;
  const tone = slaTone(summary.met, running ? budget.remaining_pct : null);
  const serial = def.aggregation === 'serial';

  return (
    <div className="space-y-4">
      <div className="text-xs text-slate-500">
        {formatInZone(period.start, tz, false)} – {formatInZone(lastDay(period.end), tz, false)} ({tz})
        {running && <> · to {formatInZone(period.effective_end, tz)}</>}
        {report.issued && <> · issued {formatInZone(report.issued.issued_at, tz)} by {report.issued.issued_by}</>}
      </div>

      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Tile label="Availability" hint={serial ? 'Service: any monitor down counts' : 'Mean of monitors'}>
          <AvailabilityFigure value={summary.availability_pct} tone={tone} />
        </Tile>
        <Tile label="Target">
          <span className="font-mono">{formatTarget(def.target_pct)}</span>
        </Tile>
        <Tile label="Status" hint={running ? 'Period in progress' : undefined}>
          <SlaStatusPill met={summary.met} running={running} />
        </Tile>
        <Tile label="Coverage" hint="Observed share of the period">
          <span className="font-mono">{summary.coverage_pct.toFixed(1)}%</span>
        </Tile>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <Panel title="Error budget" subtitle={`${formatSlaDuration(budget.allowed_full_period_seconds)} of downtime allowed per full period`}>
          <BudgetBar remainingPct={budget.remaining_pct} />
          <dl className="mt-4 grid grid-cols-3 gap-3 text-sm">
            <div>
              <dt className="text-xs text-slate-500">Allowed so far</dt>
              <dd className="font-mono text-slate-200">{formatSlaDuration(budget.allowed_seconds)}</dd>
            </div>
            <div>
              <dt className="text-xs text-slate-500">Consumed</dt>
              <dd className="font-mono text-slate-200">{formatSlaDuration(budget.consumed_seconds)}</dd>
            </div>
            <div>
              <dt className="text-xs text-slate-500">Remaining</dt>
              <dd className={`font-mono ${budget.remaining_seconds < 0 ? 'text-rose-300' : 'text-slate-200'}`}>
                {summary.has_data ? formatSlaDuration(budget.remaining_seconds) : '—'}
              </dd>
            </div>
          </dl>
        </Panel>

        <Panel title="Response">
          <dl className="grid grid-cols-2 gap-3 text-sm">
            <div>
              <dt className="text-xs text-slate-500">Outages</dt>
              <dd className="font-mono text-slate-200">{report.response.outage_count}</dd>
            </div>
            <div>
              <dt className="text-xs text-slate-500">Mean time to recover</dt>
              <dd className="font-mono text-slate-200">{formatSlaDuration(report.response.mttr_seconds)}</dd>
            </div>
            <div>
              <dt className="text-xs text-slate-500">Availability alerts</dt>
              <dd className="font-mono text-slate-200">
                {report.response.alert_count}
                <span className="text-slate-500"> ({report.response.acknowledged_count} acked)</span>
              </dd>
            </div>
            <div>
              <dt className="text-xs text-slate-500">Mean time to acknowledge</dt>
              <dd className="font-mono text-slate-200">{formatSlaDuration(report.response.mtta_seconds)}</dd>
            </div>
          </dl>
        </Panel>
      </div>

      {report.daily.length > 0 && (
        <Panel title="Daily availability" subtitle={`Days in ${tz}`}>
          <div className="flex h-9 items-stretch gap-[2px]">
            {report.daily.map((d) => {
              const t = dayTone(d.availability_pct, def.target_pct);
              return (
                <div key={d.date} className={`flex-1 rounded-[2px] ${TONE_BG[t]}`}
                  title={`${d.date}: ${d.has_data ? formatSlaPct(d.availability_pct) : 'no data'}${
                    d.down_seconds > 0 ? ` · ${formatSlaDuration(d.down_seconds)} down` : ''}`} />
              );
            })}
          </div>
          <div className="mt-1.5 flex justify-between font-mono text-[10px] text-slate-500">
            <span>{report.daily[0].date}</span>
            <span>{report.daily[report.daily.length - 1].date}</span>
          </div>
          <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-slate-500">
            {([['good', 'At or above target'], ['warn', 'Below target, ≥ 99%'], ['bad', 'Below 99%'], ['none', 'No data']] as const).map(
              ([t, label]) => (
                <span key={t} className="flex items-center gap-1.5">
                  <span className={`h-2 w-2 rounded-[2px] ${TONE_BG[t]}`} />
                  {label}
                </span>
              )
            )}
          </div>
        </Panel>
      )}

      <Panel title={`Monitors (${report.monitors.length})`}>
        {report.monitors.length === 0 ? (
          <p className="text-sm text-slate-500">No monitors match this SLA.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[640px] text-sm">
              <thead>
                <tr className="border-b border-white/[0.06] text-left text-[11px] uppercase tracking-wider text-slate-500">
                  <th className="py-2 pr-4 font-medium">Monitor</th>
                  <th className="py-2 pr-4 font-medium">Availability</th>
                  <th className="py-2 pr-4 font-medium">Coverage</th>
                  <th className="py-2 pr-4 font-medium">Downtime</th>
                  <th className="py-2 pr-4 font-medium">Planned</th>
                  <th className="py-2 pr-4 font-medium">Paused</th>
                  <th className="py-2 font-medium">Outages</th>
                </tr>
              </thead>
              <tbody>
                {report.monitors.map((m) => {
                  const met = m.availability_pct === null ? null : m.availability_pct + 1e-9 >= def.target_pct;
                  return (
                    <tr key={m.id} className="border-b border-white/[0.04] last:border-0">
                      <td className="py-2 pr-4">
                        <Link href={`/monitors/${m.id}`} className="text-slate-200 hover:text-cyan-300">{m.name}</Link>
                        <span className="ml-1.5 text-xs text-slate-500">{m.type}</span>
                      </td>
                      <td className={`py-2 pr-4 font-mono ${TONE_TEXT[slaTone(met)]}`}>{formatSlaPct(m.availability_pct)}</td>
                      <td className="py-2 pr-4 font-mono text-slate-400">{m.coverage_pct.toFixed(1)}%</td>
                      <td className="py-2 pr-4 font-mono text-slate-300">{formatSlaDuration(m.unplanned_down_seconds)}</td>
                      <td className="py-2 pr-4 font-mono text-slate-400">{formatSlaDuration(m.planned_down_seconds)}</td>
                      <td className="py-2 pr-4 font-mono text-slate-400">{formatSlaDuration(m.paused_seconds)}</td>
                      <td className="py-2 font-mono text-slate-300">{m.outage_count}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </Panel>

      {serial && (
        <Panel title="Service outages" subtitle="Any monitor down outside maintenance">
          <OutageTable outages={report.service_outages} timeZone={tz} showMonitor={false} />
        </Panel>
      )}

      <Panel title="Monitor outages" subtitle={report.outages_truncated ? `First ${report.outages.length} shown — exports carry every outage` : undefined}>
        <OutageTable outages={report.outages} timeZone={tz} showMonitor />
      </Panel>

      {report.notes.length > 0 && (
        <div className="space-y-1.5 rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3">
          {report.notes.map((n) => (
            <p key={n} className="flex items-start gap-2 text-xs text-slate-400">
              <Info className="mt-0.5 h-3.5 w-3.5 shrink-0 text-slate-500" strokeWidth={1.75} />
              {n}
            </p>
          ))}
        </div>
      )}
    </div>
  );
}
