'use client';

import { useEffect, useMemo, useState } from 'react';
import { Search } from 'lucide-react';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import DiscoveryModal from './DiscoveryModal';
import { Notice } from './ApproveModal';
import { linkDiscoveryMonitor } from '@/lib/api';
import { getAllMonitors } from '@/lib/monitor-list';
import { errorMessage, suggestionTarget, type DiscoveryMonitor, type DiscoveryService, type DiscoverySuggestion } from '@/lib/discovery';
import { monitorStateColors } from '@/lib/monitor-utils';
import type { Monitor, MonitorState } from '@/lib/types';

const MAX_ROWS = 100;

interface LinkMonitorModalProps {
  service: DiscoveryService;
  suggestion?: DiscoverySuggestion;
  candidates: DiscoveryMonitor[];
  initialMonitorId?: string;
  onLinked: (name: string) => void;
  onClose: () => void;
}

function monitorTarget(m: Monitor): string {
  const c = (m.config || {}) as unknown as Record<string, unknown>;
  if (typeof c.url === 'string') return c.url;
  if (typeof c.host === 'string') return c.port ? `${c.host}:${c.port}` : c.host;
  return '';
}

export default function LinkMonitorModal({ service, suggestion, candidates, initialMonitorId, onLinked, onClose }: LinkMonitorModalProps) {
  const [monitors, setMonitors] = useState<Monitor[] | null>(null);
  const [loadError, setLoadError] = useState('');
  const [query, setQuery] = useState('');
  const [selected, setSelected] = useState(initialMonitorId || (candidates.length === 1 ? candidates[0].id : ''));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const linkedIds = useMemo(() => new Set(service.monitors.map((m) => m.id)), [service.monitors]);
  const candidateIds = useMemo(() => new Set(candidates.map((m) => m.id)), [candidates]);

  useEffect(() => {
    let active = true;
    getAllMonitors().then((all) => { if (active) setMonitors(all); }).catch((e) => { if (active) setLoadError(errorMessage(e)); });
    return () => { active = false; };
  }, []);

  const matches = useMemo(() => {
    const q = query.trim().toLowerCase();
    return (monitors || []).filter((m) => !candidateIds.has(m.id) && (!q || m.name.toLowerCase().includes(q) || monitorTarget(m).toLowerCase().includes(q)));
  }, [monitors, query, candidateIds]);

  const submit = async () => {
    const name = candidates.find((m) => m.id === selected)?.name || monitors?.find((m) => m.id === selected)?.name || 'Monitor';
    setSaving(true); setError('');
    try {
      await linkDiscoveryMonitor(service.id, selected, suggestion?.key);
      onLinked(name);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <DiscoveryModal
      title={`Link a monitor to ${service.name}`}
      subtitle={suggestion ? <>For <span className="break-all font-mono text-slate-400">{suggestionTarget(suggestion)}</span></> : 'Counts toward this service’s coverage. Nothing about the monitor changes.'}
      onClose={onClose}
      locked={saving}
      footer={<>
        <Button variant="ghost" onClick={onClose} disabled={saving}>Cancel</Button>
        <Button variant="accent" onClick={() => void submit()} disabled={!selected || saving || linkedIds.has(selected)} loading={saving}>Link monitor</Button>
      </>}
    >
      <div className="space-y-4">
        {error && <Notice tone="danger">{error}</Notice>}
        {candidates.length > 0 && (
          <section>
            <h3 className="mb-2 text-xs font-medium text-slate-400">Checks the same target</h3>
            <p className="mb-2 text-xs text-slate-500">Verify its assertions and location before linking.</p>
            <ul className="divide-y divide-white/[0.04] rounded-lg border border-amber-500/20 bg-amber-500/[0.04]">
              {candidates.map((m) => <MonitorOption key={m.id} id={m.id} name={m.name} type={m.type} state={m.state} paused={!m.enabled} selected={selected === m.id} linked={linkedIds.has(m.id)} onSelect={setSelected} />)}
            </ul>
          </section>
        )}
        <section>
          {candidates.length > 0 && <h3 className="mb-2 text-xs font-medium text-slate-400">Any monitor</h3>}
          <div className="relative mb-2">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" aria-hidden="true" />
            <input className="input !pl-8" aria-label="Search monitors" placeholder="Search by name or target" value={query} onChange={(e) => setQuery(e.target.value)} autoFocus={candidates.length === 0} />
          </div>
          {loadError ? <p className="text-xs text-rose-300">{loadError}</p> : monitors === null ? (
            <p className="py-6 text-center text-xs text-slate-500">Loading monitors…</p>
          ) : matches.length === 0 ? (
            <p className="py-6 text-center text-xs text-slate-500">{query ? 'No monitor matches.' : 'No other monitors in this workspace.'}</p>
          ) : (
            <ul className="dashboard-scroll max-h-72 divide-y divide-white/[0.04] overflow-y-auto rounded-lg border border-white/[0.06]">
              {matches.slice(0, MAX_ROWS).map((m) => <MonitorOption key={m.id} id={m.id} name={m.name} type={m.type} state={m.current_state} target={monitorTarget(m)} paused={!m.enabled} selected={selected === m.id} linked={linkedIds.has(m.id)} onSelect={setSelected} />)}
            </ul>
          )}
          {matches.length > MAX_ROWS && <p className="mt-2 text-xs text-slate-500">Showing {MAX_ROWS} of {matches.length}. Refine the search.</p>}
        </section>
      </div>
    </DiscoveryModal>
  );
}

function MonitorOption({ id, name, type, state, target, paused, selected, linked, onSelect }: {
  id: string; name: string; type: string; state?: string; target?: string; paused: boolean; selected: boolean; linked: boolean; onSelect: (id: string) => void;
}) {
  const colors = monitorStateColors(state as MonitorState | undefined);
  return (
    <li>
      <label className={`flex cursor-pointer items-center gap-3 px-3 py-2 text-sm transition-colors ${selected ? 'bg-cyan-500/[0.08]' : 'hover:bg-white/[0.03]'} ${linked ? 'cursor-not-allowed opacity-50' : ''}`}>
        <input type="radio" name="link-monitor" className="shrink-0" checked={selected} disabled={linked} onChange={() => onSelect(id)} />
        <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${paused ? 'bg-slate-600' : colors.dot}`} aria-hidden="true" />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-slate-200">{name}</span>
          {target && <span className="block truncate font-mono text-[11px] text-slate-500">{target}</span>}
        </span>
        {linked ? <Pill size="xs">Linked</Pill> : paused ? <Pill size="xs">Paused</Pill> : null}
        <Pill size="xs">{type}</Pill>
      </label>
    </li>
  );
}
