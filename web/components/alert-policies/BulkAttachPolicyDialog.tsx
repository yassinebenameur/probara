'use client';

import { useEffect, useMemo, useState } from 'react';
import type { AlertPolicy, BulkAlertPolicyOp, Monitor } from '@/lib/types';
import { bulkUpdateMonitorAlertPolicy, getAlertPolicies } from '@/lib/api';

type Mode =
  | { kind: 'pick-policy'; monitors: Monitor[] }
  | { kind: 'pick-monitors'; policy: AlertPolicy };

interface BaseProps {
  open: boolean;
  onClose: () => void;
  onDone: (result: {
    updated: number;
    unchanged: number;
    op: BulkAlertPolicyOp;
    policyName: string;
  }) => void;
}

type Props = BaseProps & Mode;

export default function BulkAttachPolicyDialog(props: Props) {
  const { open, onClose, onDone } = props;
  const [op, setOp] = useState<BulkAlertPolicyOp>('attach');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // pick-policy state
  const [policies, setPolicies] = useState<AlertPolicy[]>([]);
  const [policiesLoading, setPoliciesLoading] = useState(false);
  const [policyQuery, setPolicyQuery] = useState('');
  const [selectedPolicyId, setSelectedPolicyId] = useState<string>('');

  // pick-monitors state (selected ids)
  const [selectedMonitorIds, setSelectedMonitorIds] = useState<string[]>([]);
  const [allMonitors, setAllMonitors] = useState<Monitor[]>([]);
  const [monitorsLoading, setMonitorsLoading] = useState(false);

  useEffect(() => {
    if (!open) {
      setOp('attach');
      setError(null);
      setPolicyQuery('');
      setSelectedPolicyId('');
      setSelectedMonitorIds([]);
      return;
    }
    if (props.kind === 'pick-policy') {
      setPoliciesLoading(true);
      getAlertPolicies({ page_size: 100 })
        .then((res) => setPolicies(res?.items ?? []))
        .catch((e: any) => setError(e?.message ?? 'Failed to load policies'))
        .finally(() => setPoliciesLoading(false));
    } else {
      // pick-monitors: load monitors. Use existing helper if available.
      setMonitorsLoading(true);
      import('@/lib/monitor-list')
        .then(({ getAllMonitors }) => getAllMonitors())
        .then((items) => setAllMonitors(items))
        .catch((e: any) => setError(e?.message ?? 'Failed to load monitors'))
        .finally(() => setMonitorsLoading(false));
    }
  }, [open, props.kind]);

  const filteredPolicies = useMemo(() => {
    const q = policyQuery.trim().toLowerCase();
    if (!q) return policies;
    return policies.filter((p) => p.name.toLowerCase().includes(q));
  }, [policies, policyQuery]);

  if (!open) return null;

  const canConfirm =
    !submitting &&
    (props.kind === 'pick-policy' ? selectedPolicyId : selectedMonitorIds.length > 0);

  const handleConfirm = async () => {
    setSubmitting(true);
    setError(null);
    try {
      let monitorIds: string[];
      let policyId: string;
      let policyName: string;
      if (props.kind === 'pick-policy') {
        monitorIds = props.monitors.map((m) => m.id);
        policyId = selectedPolicyId;
        policyName = policies.find((p) => p.id === selectedPolicyId)?.name ?? 'policy';
      } else {
        monitorIds = selectedMonitorIds;
        policyId = props.policy.id;
        policyName = props.policy.name;
      }
      const result = await bulkUpdateMonitorAlertPolicy(monitorIds, policyId, op);
      onDone({
        updated: result.updated,
        unchanged: result.unchanged,
        op,
        policyName,
      });
    } catch (e: any) {
      setError(e?.message ?? 'Operation failed');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/70 backdrop-blur-sm">
      <div className="w-full max-w-2xl rounded-xl border border-white/[0.08] bg-slate-900/95 p-6 shadow-2xl">
        <div className="mb-4 flex items-start justify-between">
          <div>
            <h2 className="text-base font-semibold text-white">
              {props.kind === 'pick-policy' ? 'Attach a policy to monitors' : 'Attach to monitors'}
            </h2>
            <p className="mt-1 text-xs text-slate-500">
              {props.kind === 'pick-policy'
                ? `${props.monitors.length} monitor${props.monitors.length === 1 ? '' : 's'} selected`
                : `Policy: ${props.policy.name}`}
            </p>
          </div>
          <button onClick={onClose} className="text-slate-400 hover:text-white" aria-label="Close">
            ×
          </button>
        </div>

        {/* Op switch */}
        <div className="mb-4 flex gap-2 rounded-lg border border-white/[0.06] bg-slate-950/40 p-1">
          {(['attach', 'detach'] as BulkAlertPolicyOp[]).map((v) => (
            <button
              key={v}
              type="button"
              onClick={() => setOp(v)}
              className={`flex-1 rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                op === v ? 'bg-cyan-500/15 text-cyan-200' : 'text-slate-400 hover:text-white'
              }`}
            >
              {v === 'attach' ? 'Attach' : 'Detach'}
            </button>
          ))}
        </div>

        {/* Body */}
        {props.kind === 'pick-policy' ? (
          <div>
            <input
              type="search"
              value={policyQuery}
              onChange={(e) => setPolicyQuery(e.target.value)}
              placeholder="Search policies…"
              className="input input-sm mb-2 w-full"
            />
            <div className="max-h-72 overflow-y-auto rounded-lg border border-white/[0.06]">
              {policiesLoading ? (
                <p className="p-4 text-xs text-slate-500">Loading…</p>
              ) : filteredPolicies.length === 0 ? (
                <p className="p-4 text-xs text-slate-500">No policies match.</p>
              ) : (
                <ul>
                  {filteredPolicies.map((p) => (
                    <li key={p.id}>
                      <button
                        type="button"
                        onClick={() => setSelectedPolicyId(p.id)}
                        className={`flex w-full items-center justify-between border-b border-white/[0.04] px-3 py-2 text-left text-xs last:border-b-0 ${
                          selectedPolicyId === p.id
                            ? 'bg-cyan-500/10 text-cyan-100'
                            : 'text-slate-200 hover:bg-white/[0.04]'
                        }`}
                      >
                        <span>
                          <span className="font-medium">{p.name}</span>
                          {p.description ? (
                            <span className="ml-2 text-slate-500">{p.description}</span>
                          ) : null}
                        </span>
                        <span className="text-[10px] uppercase tracking-wider text-slate-500">
                          {p.failure_threshold} fail · {Math.floor(p.failure_window_seconds / 60)}m
                        </span>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>
        ) : (
          <PickMonitorsList
            monitors={allMonitors}
            loading={monitorsLoading}
            selected={selectedMonitorIds}
            onChange={setSelectedMonitorIds}
          />
        )}

        {error && (
          <p className="mt-3 rounded border border-rose-500/20 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
            {error}
          </p>
        )}

        <div className="mt-5 flex items-center justify-end gap-2">
          <button
            type="button"
            onClick={onClose}
            disabled={submitting}
            className="btn btn-secondary btn-sm"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={handleConfirm}
            disabled={!canConfirm}
            className="btn btn-primary btn-sm"
          >
            {submitting ? 'Working…' : op === 'attach' ? 'Attach' : 'Detach'}
          </button>
        </div>
      </div>
    </div>
  );
}

interface PickMonitorsListProps {
  monitors: Monitor[];
  loading: boolean;
  selected: string[];
  onChange: (next: string[]) => void;
}

function PickMonitorsList({ monitors, loading, selected, onChange }: PickMonitorsListProps) {
  const [query, setQuery] = useState('');
  const [tag, setTag] = useState<string>('');
  const allTags = useMemo(() => {
    const tags = new Set<string>();
    monitors.forEach((m) => (m.tags ?? []).forEach((t) => tags.add(t)));
    return Array.from(tags).sort();
  }, [monitors]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return monitors.filter((m) => {
      if (tag && !(m.tags ?? []).includes(tag)) return false;
      if (!q) return true;
      return (
        m.name.toLowerCase().includes(q) ||
        (m.url ?? '').toLowerCase().includes(q) ||
        (m.tags ?? []).some((t) => t.toLowerCase().includes(q))
      );
    });
  }, [monitors, query, tag]);

  const selectedSet = useMemo(() => new Set(selected), [selected]);
  const allFilteredSelected =
    filtered.length > 0 && filtered.every((m) => selectedSet.has(m.id));

  const toggle = (id: string) => {
    onChange(selectedSet.has(id) ? selected.filter((x) => x !== id) : [...selected, id]);
  };

  const toggleAllFiltered = () => {
    if (allFilteredSelected) {
      onChange(selected.filter((id) => !filtered.some((m) => m.id === id)));
    } else {
      const next = new Set(selected);
      filtered.forEach((m) => next.add(m.id));
      onChange(Array.from(next));
    }
  };

  return (
    <div>
      <div className="mb-2 flex items-center gap-2">
        <input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search monitors by name, URL, tag…"
          className="input input-sm flex-1"
        />
        <select
          value={tag}
          onChange={(e) => setTag(e.target.value)}
          className="input input-sm h-8 w-32 text-xs"
        >
          <option value="">All tags</option>
          {allTags.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </select>
      </div>
      <div className="mb-2 flex items-center justify-between text-[11px] text-slate-500">
        <span>
          {selected.length} selected · {filtered.length} of {monitors.length} shown
        </span>
        <button
          type="button"
          onClick={toggleAllFiltered}
          className="text-cyan-300 hover:text-cyan-200"
          disabled={filtered.length === 0}
        >
          {allFilteredSelected ? 'Clear selection' : 'Select all in view'}
        </button>
      </div>
      <div className="max-h-72 overflow-y-auto rounded-lg border border-white/[0.06]">
        {loading ? (
          <p className="p-4 text-xs text-slate-500">Loading…</p>
        ) : filtered.length === 0 ? (
          <p className="p-4 text-xs text-slate-500">No monitors match.</p>
        ) : (
          <ul>
            {filtered.map((m) => {
              const checked = selectedSet.has(m.id);
              return (
                <li key={m.id}>
                  <button
                    type="button"
                    onClick={() => toggle(m.id)}
                    className={`flex w-full items-center gap-3 border-b border-white/[0.04] px-3 py-2 text-left text-xs last:border-b-0 ${
                      checked
                        ? 'bg-cyan-500/10 text-cyan-100'
                        : 'text-slate-200 hover:bg-white/[0.04]'
                    }`}
                  >
                    <span
                      className={`flex h-3.5 w-3.5 flex-shrink-0 items-center justify-center rounded border ${
                        checked ? 'border-cyan-400 bg-cyan-500' : 'border-white/20'
                      }`}
                    >
                      {checked && (
                        <svg className="h-2 w-2 text-white" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={3}>
                          <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                        </svg>
                      )}
                    </span>
                    <span className="flex-1">
                      <span className="font-medium">{m.name}</span>
                      <span className="ml-2 text-[10px] uppercase text-slate-500">{m.type}</span>
                    </span>
                    {m.url ? <span className="truncate text-slate-500">{m.url}</span> : null}
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </div>
  );
}
