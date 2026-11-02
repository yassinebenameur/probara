'use client';

import { CSSProperties, Fragment } from 'react';
import Link from 'next/link';
import { ChevronRight, Globe, Link2, Plus, Server, Unlink } from 'lucide-react';
import IconButton from '@/components/ui/IconButton';
import Pill from '@/components/ui/Pill';
import CopyableTarget from '@/components/ui/CopyableTarget';
import TypeBadge from '@/components/ui/TypeBadge';
import SortableHeader from '@/components/ui/SortableHeader';
import { ElapsedTime } from '@/components/ui/RelativeTime';
import { FOCUS_RING } from '@/components/ui/focus';
import { formatDateTimeFull, pluralize } from '@/lib/format';
import type { SortState } from '@/lib/sort';
import { MONITOR_STATUS_DOT, MONITOR_STATUS_LABEL, MONITOR_STATUS_TONE, TONE_TEXT } from '@/lib/status-tones';
import { gapInfo, pendingSuggestionCount, serviceSuggestions, suggestionCover, suggestionTarget, type DiscoveryMonitor, type DiscoveryResource, type DiscoveryService, type DiscoverySort, type DiscoverySuggestion } from '@/lib/discovery';
import { displayStatusFromState } from '@/lib/monitor-utils';

interface ServiceTableProps {
  items: DiscoveryService[];
  expanded: string | null;
  onToggle: (id: string) => void;
  writable: boolean;
  stale: boolean;
  onApprove: (service: DiscoveryService, suggestion: DiscoverySuggestion) => void;
  onLink: (service: DiscoveryService, suggestion?: DiscoverySuggestion, monitorId?: string) => void;
  onUnlink: (service: DiscoveryService, monitor: DiscoveryMonitor) => void;
  /** Server-side order; each key has one direction (see the page's SORT_DIR). */
  sort: SortState<DiscoverySort>;
  onSort: (next: SortState<DiscoverySort>) => void;
  /** Live refresh: `animate-row-flash` plus its colour for a service whose coverage just changed. */
  rowFlash?: (service: DiscoveryService) => { className: string; style?: CSSProperties };
}

// The page-level banner already reports stale inventory; repeating it on every
// row would bury the gaps that differ between services.
export const rowGaps = (s: DiscoveryService) => s.gaps.filter((g) => g !== 'inventory_stale');

export default function ServiceTable({ items, expanded, onToggle, writable, stale, onApprove, onLink, onUnlink, sort, onSort, rowFlash }: ServiceTableProps) {
  return (
    // The page wraps this in a padding-less `.glass` container (as /slas
    // does), so the row dividers already run edge to edge. Below xl the
    // Monitors/Coverage/Suggestions columns fold under the service name (as
    // /monitors does on phones) and the table is fixed-layout, so a long
    // suggestion URL truncates inside the detail instead of widening the row.
    // The colgroup sizes tracks by POSITION, and display:none cells take no
    // track, so below xl the visible action cell lands in column 2: that col
    // carries the action width there, and columns 3-5 (which only the detail
    // row's colSpan reaches) are zeroed so a fixed table can't share width
    // out to them. From xl every column is back in place, unchanged.
    <div className="xl:overflow-x-auto">
      <table className="data-table table-fixed text-sm xl:min-w-[720px] xl:table-auto">
        <colgroup>
          <col />
          <col className="w-12 xl:w-auto" />
          <col className="w-0 xl:w-auto" />
          <col className="w-0 xl:w-auto" />
          <col className="w-0 xl:w-12" />
        </colgroup>
        <thead>
          <tr>
            <SortableHeader label="Service" sortKey="name" sort={sort} onSort={onSort} title="By namespace, then name" />
            <th className="hidden xl:table-cell">Monitors</th>
            <SortableHeader label="Coverage" sortKey="gaps" sort={sort} onSort={onSort} title="Most gaps first" className="hidden xl:table-cell" />
            <th className="hidden xl:table-cell">Suggestions</th>
            <th className="w-12"><span className="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          {items.map((s) => {
            const open = expanded === s.id;
            const gaps = rowGaps(s);
            const pending = pendingSuggestionCount(s);
            const flash = rowFlash?.(s);
            return (
              <Fragment key={s.id}>
                {/* An open row hands its divider to the detail row below, so the
                    pair reads as one block (as on /audit). */}
                <tr className={`align-top ${open ? '[&>td]:border-b-0' : ''} ${flash?.className ?? ''}`} style={flash?.style}>
                  <td>
                    <button type="button" onClick={() => onToggle(s.id)} aria-expanded={open} className={`group -m-1 flex items-start gap-2 rounded-lg p-1 text-left ${FOCUS_RING}`}>
                      <ChevronRight className={`mt-0.5 h-4 w-4 shrink-0 text-slate-500 transition-transform duration-base ease-standard group-hover:text-slate-300 ${open ? 'rotate-90' : ''}`} aria-hidden="true" />
                      <span className="min-w-0">
                        <span className="block font-medium text-white transition-colors group-hover:text-cyan-300">{s.name}</span>
                        <span className="mt-0.5 flex flex-wrap items-center gap-x-3 text-xs text-slate-500">
                          <span className="whitespace-nowrap font-mono">{s.namespace}</span>
                          <ResourceKinds resources={s.resources} />
                        </span>
                      </span>
                    </button>
                    {/* Phones: the hidden columns' facts, folded under the name
                        and indented past the chevron. Outside the toggle button,
                        since the monitor badges are links. */}
                    <div className="mt-2 space-y-1.5 pl-6 xl:hidden">
                      <div className="flex flex-wrap items-center gap-1">
                        <CoveragePills gaps={gaps} />
                        {pending > 0 && <PendingPill pending={pending} />}
                      </div>
                      {s.monitors.length === 0 ? <p className="text-xs text-slate-500">No monitor linked</p> : (
                        <div className="flex min-w-0 items-center gap-2">
                          <span className="min-w-0"><MonitorBadge monitor={s.monitors[0]} /></span>
                          {s.monitors.length > 1 && <span className="shrink-0 text-xs tabular-nums text-slate-500">+{s.monitors.length - 1} more</span>}
                        </div>
                      )}
                    </div>
                  </td>
                  <td className="hidden xl:table-cell">
                    {s.monitors.length === 0 ? <span className="whitespace-nowrap text-xs text-slate-500">None linked</span> : (
                      <ul className="space-y-1">
                        {s.monitors.slice(0, 2).map((m) => <li key={m.id}><MonitorBadge monitor={m} /></li>)}
                        {s.monitors.length > 2 && <li className="text-xs tabular-nums text-slate-500">+{s.monitors.length - 2} more</li>}
                      </ul>
                    )}
                  </td>
                  <td className="hidden xl:table-cell">
                    {gaps.length === 0 ? <CoveragePills gaps={gaps} /> : (
                      <div className="flex max-w-xs flex-wrap gap-1">
                        <CoveragePills gaps={gaps} />
                      </div>
                    )}
                  </td>
                  <td className="hidden text-xs xl:table-cell">
                    {pending > 0 ? <PendingPill pending={pending} /> : <span className="text-slate-500">—</span>}
                  </td>
                  <td className="!py-2.5">
                    {writable && <div className="flex justify-end"><IconButton icon={<Link2 strokeWidth={1.75} />} label={`Link a monitor to ${s.name}`} onClick={() => onLink(s)} /></div>}
                  </td>
                </tr>
                {open && (
                  <tr>
                    <td colSpan={5} className="!pt-0">
                      <ServiceDetail service={s} writable={writable} stale={stale} onApprove={onApprove} onLink={onLink} onUnlink={onUnlink} />
                    </td>
                  </tr>
                )}
              </Fragment>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// The pill shows the short name; the full explanation is read out with it
// (sr-only) and is listed in the open row's detail for touch and keyboard
// users, so the hover title is only a shortcut.
function CoveragePills({ gaps }: { gaps: string[] }) {
  if (gaps.length === 0) return <Pill size="xs" tone="success" dot>Covered</Pill>;
  return <>{gaps.map((g) => {
    const info = gapInfo(g);
    return <span key={g} title={info.label}><Pill size="xs" tone={info.tone} className="whitespace-nowrap">{info.short}<span className="sr-only">: {info.label}</span></Pill></span>;
  })}</>;
}

function PendingPill({ pending }: { pending: number }) {
  return <Pill size="xs" tone="info" className="whitespace-nowrap tabular-nums">{pending} to review</Pill>;
}

function ResourceKinds({ resources }: { resources: DiscoveryResource[] }) {
  const ingress = resources.filter((r) => r.kind === 'Ingress').length;
  const service = resources.filter((r) => r.kind === 'Service').length;
  return <>
    {service > 0 && <span className="inline-flex items-center gap-1 whitespace-nowrap tabular-nums"><Server className="h-3 w-3" aria-hidden="true" />{service > 1 ? pluralize(service, 'Service') : 'Service'}</span>}
    {ingress > 0 && <span className="inline-flex items-center gap-1 whitespace-nowrap tabular-nums"><Globe className="h-3 w-3" aria-hidden="true" />{ingress > 1 ? pluralize(ingress, 'Ingress', 'Ingresses') : 'Ingress'}</span>}
  </>;
}

function MonitorBadge({ monitor: m }: { monitor: DiscoveryMonitor }) {
  // An enabled check without fresh results is not evidence of coverage, so its
  // stored state is not shown (groups have no results of their own).
  const stale = m.enabled && m.type !== 'group' && !m.fresh;
  const status = stale ? 'unknown' : displayStatusFromState({ enabled: m.enabled, current_state: m.state });
  return (
    <Link href={`/monitors/${m.id}`} className={`group inline-flex max-w-[16rem] items-center gap-2 rounded text-xs ${FOCUS_RING}`}>
      <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${MONITOR_STATUS_DOT[status]}`} aria-hidden="true" />
      <span className="truncate text-slate-200 transition-colors group-hover:text-cyan-300">{m.name}</span>
      <span className={`shrink-0 whitespace-nowrap ${stale ? 'text-slate-500' : TONE_TEXT[MONITOR_STATUS_TONE[status]]}`}>{stale ? 'No recent results' : MONITOR_STATUS_LABEL[status]}</span>
    </Link>
  );
}

function ServiceDetail({ service: s, writable, stale, onApprove, onLink, onUnlink }: Omit<ServiceTableProps, 'items' | 'expanded' | 'onToggle' | 'sort' | 'onSort' | 'rowFlash'> & { service: DiscoveryService }) {
  const suggestions = serviceSuggestions(s);
  const gaps = rowGaps(s);
  // Kept only by its links: its resources all moved to another service (the
  // probara.io/service grouping changed). Suggestions and resources would
  // both be empty, so say what happened instead.
  const emptied = s.resources.length === 0;
  return (
    <div className="glass-well grid gap-4 p-4 lg:grid-cols-[minmax(0,1fr)_18rem]">
      <section className="min-w-0">
        <h4 className="mb-2 text-ui font-medium text-slate-300">Suggested checks</h4>
        {emptied ? (
          <p className="text-xs text-slate-500">No resources left: they moved to another service when the <span className="font-mono">probara.io/service</span> grouping changed. Link these monitors to that service and unlink them here — this service is deleted, with its links, once it has been absent for 7 days.</p>
        ) : suggestions.length === 0 ? (
          <p className="text-xs text-slate-500">No check can be inferred. Link an existing monitor, or add a <span className="font-mono">probara.io/health-path</span> annotation to the Service.</p>
        ) : (
          <ul className="divide-y divide-divider">
            {suggestions.map(({ suggestion: p, resource }) => {
              // Also a monitor linked to the service without a key that checks
              // this target: it already covers the suggestion.
              const linked = suggestionCover(s, p.key);
              const candidates = s.candidates[p.key] || [];
              return (
                <li key={p.key} className="flex flex-wrap items-start gap-3 py-2.5 first:pt-0 last:pb-0">
                  {/* Phones: a floor on the text column wraps a wide "Linked"
                      pill onto its own line instead of squeezing the target. */}
                  <div className="min-w-[12rem] flex-1 xl:min-w-0">
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      <TypeBadge type={p.type} />
                      <CopyableTarget value={suggestionTarget(p)} label="target" size="sm" href={p.internal ? null : undefined} textClassName="font-mono text-slate-200" />
                      {p.internal && <span title="Cluster-internal target: runs from a private location"><Pill size="xs" tone="info" className="whitespace-nowrap">Private location</Pill></span>}
                    </div>
                    <p className="mt-1 text-xs text-slate-500">{resource.kind} {resource.name} · {p.reason}</p>
                    {!linked && candidates.length > 0 && (
                      <p className={`mt-1 text-xs ${TONE_TEXT.warning}`}>
                        {candidates.length === 1 ? <>Already checked by <strong className="font-medium">{candidates[0].name}</strong> — link it instead of creating a duplicate.</> : <>{candidates.length} existing monitors check this target — link one instead of creating a duplicate.</>}
                      </p>
                    )}
                  </div>
                  <div className="-my-1 flex shrink-0 items-center gap-0.5">
                    {linked ? <Pill size="xs" tone="success">Linked · {linked.name}</Pill> : writable && <>
                      {candidates.length === 0 && <IconButton icon={<Plus strokeWidth={1.75} />} label={stale ? 'Inventory is stale — approval paused' : 'Create a monitor from this suggestion'} disabled={stale} onClick={() => onApprove(s, p)} />}
                      <IconButton icon={<Link2 strokeWidth={1.75} />} label={candidates.length ? `Link ${candidates[0].name}` : 'Link an existing monitor'} onClick={() => onLink(s, p, candidates[0]?.id)} />
                      {candidates.length > 0 && <IconButton icon={<Plus strokeWidth={1.75} />} label={stale ? 'Inventory is stale — approval paused' : 'Create another monitor anyway'} disabled={stale} onClick={() => onApprove(s, p)} />}
                    </>}
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </section>
      <div className="space-y-4 border-t border-divider pt-4 lg:border-l lg:border-t-0 lg:pl-4 lg:pt-0">
        {gaps.length > 0 && (
          <section>
            <h4 className="mb-2 text-ui font-medium text-slate-300">Gaps</h4>
            <ul className="space-y-1.5">
              {gaps.map((g) => {
                const info = gapInfo(g);
                return (
                  <li key={g} className="flex items-start gap-2 text-xs">
                    <Pill size="xs" tone={info.tone} className="shrink-0 whitespace-nowrap">{info.short}</Pill>
                    <span className="min-w-0 text-slate-400">{info.label}</span>
                  </li>
                );
              })}
            </ul>
          </section>
        )}
        <section>
          <h4 className="mb-2 text-ui font-medium text-slate-300">Resources</h4>
          {emptied ? <p className="text-xs text-slate-500">None — moved to another service.</p> : (
            <ul className="space-y-1.5">
              {s.resources.map((r) => (
                <li key={r.uid} className="flex items-center gap-2 text-xs">
                  {r.kind === 'Ingress' ? <Globe className="h-3.5 w-3.5 shrink-0 text-slate-500" aria-hidden="true" /> : <Server className="h-3.5 w-3.5 shrink-0 text-slate-500" aria-hidden="true" />}
                  <span className="min-w-0 flex-1 truncate" title={`${r.kind}/${r.name}`}><span className="font-mono text-slate-300">{r.name}</span> <span className="text-slate-500">{r.kind}</span></span>
                  {r.missing_since && <span title={`Absent from complete scans since ${formatDateTimeFull(r.missing_since)}`}><Pill size="xs" tone="warning" className="whitespace-nowrap tabular-nums">Missing <ElapsedTime start={r.missing_since} /></Pill></span>}
                </li>
              ))}
            </ul>
          )}
        </section>
        <section>
          <h4 className="mb-2 text-ui font-medium text-slate-300">Linked monitors</h4>
          {s.monitors.length === 0 ? <p className="text-xs text-slate-500">None yet.</p> : (
            <ul className="space-y-1">
              {s.monitors.map((m) => (
                <li key={m.id} className="flex items-center gap-2">
                  <span className="min-w-0 flex-1"><MonitorBadge monitor={m} /></span>
                  {m.enabled && !m.routed && <span title="No reachable alert channel"><Pill size="xs" tone="warning" className="whitespace-nowrap">No route</Pill></span>}
                  {writable && <IconButton icon={<Unlink strokeWidth={1.75} />} size="sm" label={`Unlink ${m.name}`} danger onClick={() => onUnlink(s, m)} />}
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
    </div>
  );
}
