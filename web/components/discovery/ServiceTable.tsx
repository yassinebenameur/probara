'use client';

import { Fragment } from 'react';
import Link from 'next/link';
import { ChevronRight, Globe, Link2, Plus, Server, Unlink } from 'lucide-react';
import IconButton from '@/components/ui/IconButton';
import Pill from '@/components/ui/Pill';
import CopyableTarget from '@/components/ui/CopyableTarget';
import { formatTimeAgo, monitorStateColors } from '@/lib/monitor-utils';
import { gapInfo, serviceSuggestions, suggestionTarget, type DiscoveryMonitor, type DiscoveryResource, type DiscoveryService, type DiscoverySuggestion } from '@/lib/discovery';
import type { MonitorState } from '@/lib/types';

interface ServiceTableProps {
  items: DiscoveryService[];
  expanded: string | null;
  onToggle: (id: string) => void;
  writable: boolean;
  stale: boolean;
  onApprove: (service: DiscoveryService, suggestion: DiscoverySuggestion) => void;
  onLink: (service: DiscoveryService, suggestion?: DiscoverySuggestion, monitorId?: string) => void;
  onUnlink: (service: DiscoveryService, monitor: DiscoveryMonitor) => void;
}

// The page-level banner already reports stale inventory; repeating it on every
// row would bury the gaps that differ between services.
const rowGaps = (s: DiscoveryService) => s.gaps.filter((g) => g !== 'inventory_stale');

export default function ServiceTable({ items, expanded, onToggle, writable, stale, onApprove, onLink, onUnlink }: ServiceTableProps) {
  return (
    <div className="overflow-x-auto">
      <table className="min-w-[48rem] w-full divide-y divide-white/[0.06]">
        <thead>
          <tr className="text-left text-xs text-slate-500">
            <th className="px-3 py-2 font-medium">Service</th>
            <th className="px-3 py-2 font-medium">Monitors</th>
            <th className="px-3 py-2 font-medium">Coverage</th>
            <th className="px-3 py-2 font-medium">Suggestions</th>
            <th className="px-3 py-2"><span className="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody className="divide-y divide-white/[0.04] text-sm text-slate-200">
          {items.map((s) => {
            const open = expanded === s.id;
            const gaps = rowGaps(s);
            const suggestions = serviceSuggestions(s);
            const pending = suggestions.filter(({ suggestion }) => !s.monitors.some((m) => m.suggestion_key === suggestion.key)).length;
            return (
              <Fragment key={s.id}>
                <tr className={`align-top transition-colors ${open ? 'bg-white/[0.02]' : 'hover:bg-white/[0.015]'}`}>
                  <td className="px-3 py-3">
                    <button type="button" onClick={() => onToggle(s.id)} aria-expanded={open} className="group flex items-start gap-2 text-left">
                      <ChevronRight className={`mt-0.5 h-4 w-4 shrink-0 text-slate-500 transition-transform group-hover:text-slate-300 ${open ? 'rotate-90' : ''}`} aria-hidden="true" />
                      <span className="min-w-0">
                        <span className="block font-medium text-white group-hover:text-cyan-300">{s.name}</span>
                        <span className="mt-0.5 flex flex-wrap items-center gap-x-2 text-xs text-slate-500">
                          <span className="whitespace-nowrap font-mono">{s.namespace}</span>
                          <ResourceKinds resources={s.resources} />
                        </span>
                      </span>
                    </button>
                  </td>
                  <td className="px-3 py-3">
                    {s.monitors.length === 0 ? <span className="text-xs text-slate-500">None linked</span> : (
                      <ul className="space-y-1">
                        {s.monitors.slice(0, 2).map((m) => <li key={m.id}><MonitorBadge monitor={m} /></li>)}
                        {s.monitors.length > 2 && <li className="text-xs text-slate-500">+{s.monitors.length - 2} more</li>}
                      </ul>
                    )}
                  </td>
                  <td className="px-3 py-3">
                    {gaps.length === 0 ? <Pill size="xs" tone="success" dot>Covered</Pill> : (
                      <div className="flex max-w-xs flex-wrap gap-1">
                        {gaps.map((g) => { const info = gapInfo(g); return <span key={g} title={info.label}><Pill size="xs" tone={info.tone} className="whitespace-nowrap">{info.short}</Pill></span>; })}
                      </div>
                    )}
                  </td>
                  <td className="px-3 py-3 text-xs">
                    {pending > 0 ? <Pill size="xs" tone="info" className="whitespace-nowrap">{pending} to review</Pill> : <span className="text-slate-500">—</span>}
                  </td>
                  <td className="px-3 py-3">
                    {writable && <div className="flex justify-end"><IconButton icon={<Link2 strokeWidth={1.75} />} label={`Link a monitor to ${s.name}`} onClick={() => onLink(s)} /></div>}
                  </td>
                </tr>
                {open && (
                  <tr className="bg-white/[0.02]">
                    <td colSpan={5} className="px-3 pb-5 pt-1">
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

function ResourceKinds({ resources }: { resources: DiscoveryResource[] }) {
  const ingress = resources.filter((r) => r.kind === 'Ingress').length;
  const service = resources.filter((r) => r.kind === 'Service').length;
  return <>
    {service > 0 && <span className="inline-flex items-center gap-1"><Server className="h-3 w-3" aria-hidden="true" />{service > 1 ? `${service} Services` : 'Service'}</span>}
    {ingress > 0 && <span className="inline-flex items-center gap-1"><Globe className="h-3 w-3" aria-hidden="true" />{ingress > 1 ? `${ingress} Ingresses` : 'Ingress'}</span>}
  </>;
}

function monitorStatus(m: DiscoveryMonitor): string {
  if (!m.enabled) return 'paused';
  if (m.type !== 'group' && !m.fresh) return 'no recent results';
  return m.state;
}

function MonitorBadge({ monitor: m }: { monitor: DiscoveryMonitor }) {
  const evidence = m.enabled && (m.type === 'group' || m.fresh);
  const colors = monitorStateColors((evidence ? m.state : 'unknown') as MonitorState);
  return (
    <Link href={`/monitors/${m.id}`} className="group inline-flex max-w-[16rem] items-center gap-2 text-xs">
      <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${colors.dot}`} aria-hidden="true" />
      <span className="truncate text-slate-200 group-hover:text-cyan-300">{m.name}</span>
      <span className={`shrink-0 ${evidence ? colors.text : 'text-slate-500'}`}>{monitorStatus(m)}</span>
    </Link>
  );
}

function ServiceDetail({ service: s, writable, stale, onApprove, onLink, onUnlink }: Omit<ServiceTableProps, 'items' | 'expanded' | 'onToggle'> & { service: DiscoveryService }) {
  const suggestions = serviceSuggestions(s);
  return (
    <div className="grid gap-4 rounded-lg border border-white/[0.06] bg-slate-950/30 p-4 lg:grid-cols-[minmax(0,1fr)_18rem]">
      <section className="min-w-0">
        <h4 className="mb-2 text-xs font-medium text-slate-400">Suggested checks</h4>
        {suggestions.length === 0 ? (
          <p className="text-xs text-slate-500">No check can be inferred. Link an existing monitor, or add a <span className="font-mono">probara.io/health-path</span> annotation to the Service.</p>
        ) : (
          <ul className="divide-y divide-white/[0.04]">
            {suggestions.map(({ suggestion: p, resource }) => {
              const linked = s.monitors.find((m) => m.suggestion_key === p.key);
              const candidates = s.candidates[p.key] || [];
              return (
                <li key={p.key} className="flex flex-wrap items-start gap-3 py-2.5 first:pt-0 last:pb-0">
                  <div className="min-w-0 flex-1">
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      <Pill size="xs">{p.type.toUpperCase()}</Pill>
                      <CopyableTarget value={suggestionTarget(p)} label="target" size="sm" href={p.internal ? null : undefined} textClassName="text-slate-200" />
                      {p.internal && <span title="Cluster-internal target: runs from a private location"><Pill size="xs" tone="info" className="whitespace-nowrap">Private location</Pill></span>}
                    </div>
                    <p className="mt-1 text-xs text-slate-500">{resource.kind} {resource.name} · {p.reason}</p>
                    {!linked && candidates.length > 0 && (
                      <p className="mt-1 text-xs text-amber-300">
                        {candidates.length === 1 ? <>Already checked by <strong className="font-medium">{candidates[0].name}</strong> — link it instead of creating a duplicate.</> : <>{candidates.length} existing monitors check this target — link one instead of creating a duplicate.</>}
                      </p>
                    )}
                  </div>
                  <div className="flex shrink-0 items-center gap-0.5">
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
      <div className="space-y-4 lg:border-l lg:border-white/[0.06] lg:pl-4">
        <section>
          <h4 className="mb-2 text-xs font-medium text-slate-400">Resources</h4>
          <ul className="space-y-1.5">
            {s.resources.map((r) => (
              <li key={r.uid} className="flex items-center gap-2 text-xs">
                {r.kind === 'Ingress' ? <Globe className="h-3.5 w-3.5 shrink-0 text-slate-500" aria-hidden="true" /> : <Server className="h-3.5 w-3.5 shrink-0 text-slate-500" aria-hidden="true" />}
                <span className="min-w-0 flex-1 truncate" title={`${r.kind}/${r.name}`}><span className="font-mono text-slate-300">{r.name}</span> <span className="text-slate-500">{r.kind}</span></span>
                {r.missing_since && <span title={`Absent from complete scans since ${new Date(r.missing_since).toLocaleString()}`}><Pill size="xs" tone="warning">Missing {formatTimeAgo(r.missing_since).replace(' ago', '')}</Pill></span>}
              </li>
            ))}
          </ul>
        </section>
        <section>
          <h4 className="mb-2 text-xs font-medium text-slate-400">Linked monitors</h4>
          {s.monitors.length === 0 ? <p className="text-xs text-slate-500">None yet.</p> : (
            <ul className="space-y-1">
              {s.monitors.map((m) => (
                <li key={m.id} className="flex items-center gap-2">
                  <span className="min-w-0 flex-1"><MonitorBadge monitor={m} /></span>
                  {m.enabled && !m.routed && <span title="No reachable alert channel"><Pill size="xs" tone="warning">No route</Pill></span>}
                  {writable && <IconButton icon={<Unlink strokeWidth={1.75} />} label={`Unlink ${m.name}`} danger onClick={() => onUnlink(s, m)} />}
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
    </div>
  );
}
