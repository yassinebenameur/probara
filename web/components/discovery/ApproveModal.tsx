'use client';

import { useState } from 'react';
import Link from 'next/link';
import { Link2 } from 'lucide-react';
import Button from '@/components/ui/Button';
import FormField from '@/components/ui/FormField';
import FilterChip from '@/components/ui/FilterChip';
import Pill from '@/components/ui/Pill';
import DiscoveryModal from './DiscoveryModal';
import { approveDiscoverySuggestion } from '@/lib/api';
import { errorMessage, isDuplicateTarget, suggestionTarget, type DiscoveryMonitor, type DiscoveryService, type DiscoverySuggestion } from '@/lib/discovery';
import type { AlertChannel, Location } from '@/lib/types';

const INTERVALS = [30, 60, 120, 300, 600, 900, 1800, 3600];
const intervalLabel = (s: number) => (s < 60 ? `${s} seconds` : s < 3600 ? `${s / 60} minute${s === 60 ? '' : 's'}` : `${s / 3600} hour`);

interface ApproveModalProps {
  service: DiscoveryService;
  suggestion: DiscoverySuggestion;
  candidates: DiscoveryMonitor[];
  locations: Location[];
  channels: AlertChannel[];
  defaultLocationId: string | null;
  stale: boolean;
  onCreated: (monitorId: string, name: string) => void;
  onLinkInstead: (monitorId?: string) => void;
  onClose: () => void;
}

export default function ApproveModal({ service, suggestion, candidates, locations, channels, defaultLocationId, stale, onCreated, onLinkInstead, onClose }: ApproveModalProps) {
  const enabledLocations = locations.filter((l) => l.enabled);
  const activeChannels = channels.filter((c) => c.is_active);
  const [name, setName] = useState(`${service.name}${suggestion.internal ? ' (internal)' : ''}`);
  const [interval, setInterval] = useState(60);
  const [status, setStatus] = useState(200);
  const [locationId, setLocationId] = useState(defaultLocationId && enabledLocations.some((l) => l.id === defaultLocationId) ? defaultLocationId : '');
  const [mode, setMode] = useState<'default' | 'custom'>('default');
  const [channelIds, setChannelIds] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<{ message: string; duplicate: boolean } | null>(null);

  const needsLocation = suggestion.internal && !locationId;
  const canSubmit = !saving && !stale && name.trim() !== '' && !needsLocation && (mode === 'default' || channelIds.length > 0) && (suggestion.type !== 'http' || (status >= 100 && status <= 599));

  const submit = async () => {
    setSaving(true); setError(null);
    try {
      const result = await approveDiscoverySuggestion(service.id, {
        key: suggestion.key, name: name.trim(), interval_seconds: interval, expected_status: suggestion.type === 'http' ? status : 0,
        location_id: locationId || null, notification_mode: mode, channel_ids: mode === 'custom' ? channelIds : [],
      });
      onCreated(result.monitor_id, name.trim());
    } catch (e) {
      setError({ message: errorMessage(e), duplicate: isDuplicateTarget(e) });
    } finally {
      setSaving(false);
    }
  };

  return (
    <DiscoveryModal
      title="Create monitor"
      subtitle={<span className="break-all font-mono text-slate-400">{suggestionTarget(suggestion)}</span>}
      onClose={onClose}
      locked={saving}
      footer={<>
        <Button variant="ghost" onClick={onClose} disabled={saving}>Cancel</Button>
        <Button variant="accent" onClick={() => void submit()} disabled={!canSubmit} loading={saving}>Create monitor</Button>
      </>}
    >
      <div className="space-y-5">
        {stale && <Notice tone="warning">Inventory is stale, so new monitors can’t be approved until the collector reports again.</Notice>}
        {candidates.length > 0 && (
          <Notice tone="warning" action={<Button size="xs" variant="ghost" icon={<Link2 strokeWidth={1.75} />} onClick={() => onLinkInstead(candidates[0].id)}>Link instead</Button>}>
            {candidates.length === 1 ? <>An existing monitor already checks this target: <strong>{candidates[0].name}</strong>.</> : <>{candidates.length} existing monitors already check this target.</>}
          </Notice>
        )}
        {error && (
          <Notice tone="danger" action={error.duplicate ? <Button size="xs" variant="ghost" icon={<Link2 strokeWidth={1.75} />} onClick={() => onLinkInstead()}>Link existing</Button> : undefined}>
            {error.message}
          </Notice>
        )}

        <div className="flex flex-wrap items-center gap-1.5 text-xs text-slate-500">
          <Pill size="xs">{suggestion.type.toUpperCase()}</Pill>
          {suggestion.internal && <Pill size="xs" tone="info">Cluster-internal</Pill>}
          <span>{suggestion.reason}</span>
        </div>

        <FormField label="Name" required>
          <input className="input" value={name} maxLength={200} onChange={(e) => setName(e.target.value)} />
        </FormField>
        <div className="grid gap-4 sm:grid-cols-2">
          <FormField label="Check every">
            <select className="input" value={interval} onChange={(e) => setInterval(Number(e.target.value))}>
              {INTERVALS.map((s) => <option key={s} value={s}>{intervalLabel(s)}</option>)}
            </select>
          </FormField>
          {suggestion.type === 'http' && (
            <FormField label="Expected HTTP status" required>
              <input className="input" type="number" min={100} max={599} value={status} onChange={(e) => setStatus(Number(e.target.value))} />
            </FormField>
          )}
        </div>
        <FormField
          label="Run from"
          required={suggestion.internal}
          description={suggestion.internal ? 'Cluster-internal target: pick a private location whose worker can reach it.' : undefined}
        >
          {suggestion.internal && enabledLocations.length === 0 ? (
            <p className="text-xs text-amber-300">No private location yet. <Link href="/locations" className="underline hover:text-amber-200">Create one and deploy a worker</Link> in or near the cluster.</p>
          ) : (
            <select className="input" value={locationId} onChange={(e) => setLocationId(e.target.value)}>
              <option value="">{suggestion.internal ? 'Choose a private location' : 'Default worker fleet'}</option>
              {enabledLocations.map((l) => <option key={l.id} value={l.id}>{l.name}</option>)}
            </select>
          )}
        </FormField>
        <FormField label="Alerts">
          <div className="flex flex-wrap gap-2">
            <FilterChip selected={mode === 'default'} onClick={() => setMode('default')}>Workspace defaults</FilterChip>
            <FilterChip selected={mode === 'custom'} onClick={() => setMode('custom')}>Specific channels</FilterChip>
          </div>
          {mode === 'custom' && (
            <div className="mt-3 space-y-1.5">
              {activeChannels.length === 0 ? (
                <p className="text-xs text-amber-300">No active channels. <Link href="/settings?tab=channels" className="underline hover:text-amber-200">Add one in Settings</Link>.</p>
              ) : activeChannels.map((c) => (
                <label key={c.id} className="flex items-center gap-2 text-sm text-slate-300">
                  <input type="checkbox" checked={channelIds.includes(c.id)} onChange={(e) => setChannelIds(e.target.checked ? [...channelIds, c.id] : channelIds.filter((id) => id !== c.id))} />
                  {c.name}
                  <span className="text-xs text-slate-500">{c.type}</span>
                </label>
              ))}
            </div>
          )}
        </FormField>
        <p className="text-xs text-slate-500">Created enabled with a 10-second timeout and two-failure confirmation. Edit the monitor afterwards for paths, assertions or credentials.</p>
      </div>
    </DiscoveryModal>
  );
}

export function Notice({ tone, action, children }: { tone: 'warning' | 'danger' | 'info'; action?: React.ReactNode; children: React.ReactNode }) {
  const palette = tone === 'danger'
    ? 'border-rose-500/25 bg-rose-500/10 text-rose-200'
    : tone === 'warning'
      ? 'border-amber-500/25 bg-amber-500/[0.08] text-amber-200'
      : 'border-cyan-500/25 bg-cyan-500/[0.08] text-cyan-200';
  return (
    <div role={tone === 'danger' ? 'alert' : 'status'} className={`flex flex-wrap items-center justify-between gap-3 rounded-lg border px-3 py-2.5 text-xs ${palette}`}>
      <div className="min-w-0 flex-1">{children}</div>
      {action}
    </div>
  );
}
