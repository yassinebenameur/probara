'use client';

import { FormEvent, useEffect, useId, useMemo, useState } from 'react';
import { Search } from 'lucide-react';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Dialog from '@/components/ui/Dialog';
import Pill from '@/components/ui/Pill';
import Radio from '@/components/ui/Radio';
import ScrollList from '@/components/ui/ScrollList';
import TableSkeleton from '@/components/ui/TableSkeleton';
import TypeBadge from '@/components/ui/TypeBadge';
import { linkDiscoveryMonitor } from '@/lib/api';
import { getAllMonitors } from '@/lib/monitor-list';
import { errorMessage, suggestionTarget, type DiscoveryMonitor, type DiscoveryService, type DiscoverySuggestion } from '@/lib/discovery';
import { displayStatusFromState, monitorTarget } from '@/lib/monitor-utils';
import { MONITOR_STATUS_DOT } from '@/lib/status-tones';
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

export default function LinkMonitorModal({ service, suggestion, candidates, initialMonitorId, onLinked, onClose }: LinkMonitorModalProps) {
  const formId = useId();
  const radioName = useId();
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
    return (monitors || []).filter((m) => !candidateIds.has(m.id) && (!q || m.name.toLowerCase().includes(q) || (monitorTarget(m) ?? '').toLowerCase().includes(q)));
  }, [monitors, query, candidateIds]);

  const canSubmit = Boolean(selected) && !saving && !linkedIds.has(selected);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!canSubmit) return;
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
    <Dialog
      title={`Link a monitor to ${service.name}`}
      description={suggestion ? <>For <span className="break-all font-mono">{suggestionTarget(suggestion)}</span></> : 'Counts toward this service’s coverage. Nothing about the monitor changes.'}
      onClose={onClose}
      busy={saving}
      footer={<>
        <Button variant="ghost" size="sm" onClick={onClose} disabled={saving}>Cancel</Button>
        <Button type="submit" form={formId} variant="accent" size="sm" disabled={!canSubmit} loading={saving}>Link monitor</Button>
      </>}
    >
      <form id={formId} noValidate onSubmit={(e) => void submit(e)} className="space-y-5">
        {error && <Callout tone="danger">{error}</Callout>}
        {candidates.length > 0 && (
          <section>
            <h3 className="text-ui font-medium text-slate-300">Checks the same target</h3>
            <p className="mb-2 mt-0.5 text-xs text-slate-500">Verify its assertions and location before linking.</p>
            <div role="radiogroup" aria-label="Monitors that check the same target" className="glass-well space-y-1 p-1.5">
              {candidates.map((m) => <MonitorOption key={m.id} name={radioName} id={m.id} label={m.name} type={m.type} state={m.state} paused={!m.enabled} selected={selected === m.id} linked={linkedIds.has(m.id)} onSelect={setSelected} />)}
            </div>
          </section>
        )}
        <section>
          {candidates.length > 0 && <h3 className="mb-2 text-ui font-medium text-slate-300">Any monitor</h3>}
          <div className="relative mb-2">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-500" strokeWidth={1.75} aria-hidden="true" />
            <input type="search" className="input !pl-9" aria-label="Search monitors" placeholder="Search by name or target" value={query} onChange={(e) => setQuery(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter') e.preventDefault(); }} autoFocus={candidates.length === 0} />
          </div>
          {loadError ? <Callout tone="danger">{loadError}</Callout> : monitors === null ? (
            <div className="glass-well overflow-hidden"><TableSkeleton bare rows={4} columns={1} label="Loading monitors" /></div>
          ) : matches.length === 0 ? (
            <p className="glass-well px-3 py-6 text-center text-xs text-slate-500">{query ? 'No monitor matches.' : 'No other monitors in this workspace.'}</p>
          ) : (
            <div className="glass-well relative p-1.5">
              <ScrollList maxHeight="18rem" label="Monitors" className="dashboard-scroll">
                <div role="radiogroup" aria-label="Monitors" className="space-y-1">
                  {matches.slice(0, MAX_ROWS).map((m) => <MonitorOption key={m.id} name={radioName} id={m.id} label={m.name} type={m.type} state={m.current_state} target={monitorTarget(m) ?? ''} paused={!m.enabled} selected={selected === m.id} linked={linkedIds.has(m.id)} onSelect={setSelected} />)}
                </div>
              </ScrollList>
            </div>
          )}
          {matches.length > MAX_ROWS && <p className="mt-2 text-xs text-slate-500">Showing {MAX_ROWS} of {matches.length}. Refine the search.</p>}
        </section>
      </form>
    </Dialog>
  );
}

function MonitorOption({ name, id, label, type, state, target, paused, selected, linked, onSelect }: {
  name: string; id: string; label: string; type: string; state?: MonitorState; target?: string; paused: boolean; selected: boolean; linked: boolean; onSelect: (id: string) => void;
}) {
  const dot = MONITOR_STATUS_DOT[displayStatusFromState({ enabled: !paused, current_state: state })];
  // The whole row is the radio's label; its text column stretches so the
  // pills and type badge sit right. Checked rows take well-strong.
  return (
    <Radio
      name={name}
      value={id}
      checked={selected}
      disabled={linked}
      onChange={() => onSelect(id)}
      stretch
      className={`w-full rounded-lg px-3 !py-2 transition-colors ${selected ? 'bg-well-strong' : linked ? '' : 'hover:bg-well-hover'}`}
      label={
        <span className="flex items-center gap-3">
          <span className={`h-2 w-2 shrink-0 rounded-full ${dot}`} aria-hidden="true" />
          <span className="min-w-0 flex-1">
            <span className="block truncate">{label}</span>
            {target && <span className="block truncate font-mono text-2xs text-slate-500">{target}</span>}
          </span>
          {linked ? <Pill size="xs">Linked</Pill> : paused ? <Pill size="xs">Paused</Pill> : null}
          <TypeBadge type={type} />
        </span>
      }
    />
  );
}
