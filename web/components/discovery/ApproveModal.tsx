'use client';

import { FormEvent, useId, useMemo, useRef, useState } from 'react';
import Link from 'next/link';
import { Link2 } from 'lucide-react';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Checkbox from '@/components/ui/Checkbox';
import Dialog from '@/components/ui/Dialog';
import FormField from '@/components/ui/FormField';
import Input from '@/components/ui/Input';
import Pill from '@/components/ui/Pill';
import SegmentedControl from '@/components/ui/SegmentedControl';
import Select from '@/components/ui/Select';
import TypeBadge from '@/components/ui/TypeBadge';
import { revealFirstError, useClearErrorsOnEdit } from '@/components/monitors/form-kit';
import { approveDiscoverySuggestion } from '@/lib/api';
import { formatDuration } from '@/lib/format';
import {
  approveFormErrors, isDuplicateTarget, parseExpectedStatus, suggestionTarget,
  type ApproveFormValues, type DiscoveryMonitor, type DiscoveryService, type DiscoverySuggestion,
} from '@/lib/discovery';
import { errorMessage } from '@/lib/errors';
import { TONE_TEXT } from '@/lib/status-tones';
import type { AlertChannel, Location } from '@/lib/types';
import { INLINE_LINK } from './styles';

const INTERVALS = [30, 60, 120, 300, 600, 900, 1800, 3600];

type RoutingMode = 'default' | 'custom';
const ROUTING_OPTIONS = [
  { value: 'default', label: 'Workspace defaults' },
  { value: 'custom', label: 'Specific channels' },
] as const;
// Switching to workspace defaults resolves a "select a channel" error.
const RELATED_FIELDS: Record<string, string[]> = { channelIds: ['mode'] };

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
  const formId = useId();
  const enabledLocations = locations.filter((l) => l.enabled);
  const activeChannels = channels.filter((c) => c.is_active);
  const [name, setName] = useState(`${service.name}${suggestion.internal ? ' (internal)' : ''}`);
  const [interval, setInterval] = useState(60);
  // Kept as typed, so the field can be cleared and retyped; parsed on submit.
  const [status, setStatus] = useState('200');
  const [locationId, setLocationId] = useState(defaultLocationId && enabledLocations.some((l) => l.id === defaultLocationId) ? defaultLocationId : '');
  const [mode, setMode] = useState<RoutingMode>('default');
  const [channelIds, setChannelIds] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<{ message: string; duplicate: boolean } | null>(null);
  // The monitor forms' pattern (components/monitors/form-kit.tsx): rules run
  // on submit and render inline; editing a field clears its message.
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const values: ApproveFormValues = useMemo(() => ({ name, status, locationId, mode, channelIds }), [name, status, locationId, mode, channelIds]);
  useClearErrorsOnEdit(values, setFieldErrors, RELATED_FIELDS);
  const formRef = useRef<HTMLFormElement>(null);
  const channelsErrorId = `${formId}-channels-error`;

  const canSubmit = !saving && !stale;

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!canSubmit) return;
    const invalid = approveFormErrors(values, suggestion);
    if (Object.keys(invalid).length > 0) {
      setFieldErrors(invalid as Record<string, string>);
      revealFirstError(formRef.current);
      return;
    }
    setSaving(true); setError(null);
    try {
      const result = await approveDiscoverySuggestion(service.id, {
        key: suggestion.key, name: name.trim(), interval_seconds: interval, expected_status: suggestion.type === 'http' ? parseExpectedStatus(status) ?? 0 : 0,
        location_id: locationId || null, notification_mode: mode, channel_ids: mode === 'custom' ? channelIds : [],
      });
      onCreated(result.monitor_id, name.trim());
    } catch (e) {
      setError({ message: errorMessage(e), duplicate: isDuplicateTarget(e) });
    } finally {
      setSaving(false);
    }
  };

  const toggleChannel = (id: string, checked: boolean) =>
    setChannelIds((current) => (checked ? [...current, id] : current.filter((c) => c !== id)));

  return (
    <Dialog
      title="Create monitor"
      description={<span className="break-all font-mono">{suggestionTarget(suggestion)}</span>}
      onClose={onClose}
      busy={saving}
      bodyClassName="dashboard-scroll space-y-5"
      footer={<>
        <Button variant="ghost" size="sm" onClick={onClose} disabled={saving}>Cancel</Button>
        <Button type="submit" form={formId} variant="accent" size="sm" disabled={!canSubmit} loading={saving}>Create monitor</Button>
      </>}
    >
      {stale && <Callout tone="warning" live="polite">Inventory is stale, so new monitors can’t be approved until the collector reports again.</Callout>}
      {candidates.length > 0 && (
        <Callout tone="warning" live="polite" action={<Button size="xs" variant="ghost" icon={<Link2 strokeWidth={1.75} />} onClick={() => onLinkInstead(candidates[0].id)}>Link instead</Button>}>
          {candidates.length === 1 ? <>An existing monitor already checks this target: <strong className="font-medium text-white">{candidates[0].name}</strong>.</> : <>{candidates.length} existing monitors already check this target.</>}
        </Callout>
      )}
      {error && (
        <Callout tone="danger" action={error.duplicate ? <Button size="xs" variant="ghost" icon={<Link2 strokeWidth={1.75} />} onClick={() => onLinkInstead()}>Link existing</Button> : undefined}>
          {error.message}
        </Callout>
      )}

      <form ref={formRef} id={formId} noValidate onSubmit={(e) => void submit(e)} className="space-y-5">
        <div className="flex flex-wrap items-center gap-2 text-xs text-slate-500">
          <TypeBadge type={suggestion.type} />
          {suggestion.internal && <Pill size="xs" tone="info">Cluster-internal</Pill>}
          <span className="min-w-0">{suggestion.reason}</span>
        </div>

        <FormField label="Name" required error={fieldErrors.name || undefined}>
          <Input value={name} maxLength={200} onChange={(e) => setName(e.target.value)} />
        </FormField>
        <div className="grid gap-4 sm:grid-cols-2">
          <FormField label="Check every">
            <Select value={interval} onChange={(e) => setInterval(Number(e.target.value))}>
              {INTERVALS.map((s) => <option key={s} value={s}>{formatDuration(s)}</option>)}
            </Select>
          </FormField>
          {suggestion.type === 'http' && (
            <FormField label="Expected HTTP status" required error={fieldErrors.status || undefined}>
              <Input className="tabular-nums" type="number" inputMode="numeric" min={100} max={599} step={1} value={status} onChange={(e) => setStatus(e.target.value)} />
            </FormField>
          )}
        </div>
        <FormField
          label="Run from"
          required={suggestion.internal}
          description={suggestion.internal ? 'Cluster-internal target: pick a private location whose worker can reach it.' : undefined}
          error={fieldErrors.locationId || undefined}
        >
          {suggestion.internal && enabledLocations.length === 0 ? (
            <Callout tone="warning">
              No private location yet. <Link href="/locations" className={INLINE_LINK}>Create one and deploy a worker</Link> in or near the cluster.
            </Callout>
          ) : (
            <Select value={locationId} onChange={(e) => setLocationId(e.target.value)}>
              <option value="">{suggestion.internal ? 'Choose a private location' : 'Default worker fleet'}</option>
              {enabledLocations.map((l) => <option key={l.id} value={l.id}>{l.name}</option>)}
            </Select>
          )}
        </FormField>
        <div>
          <span className="mb-1.5 block text-ui font-medium text-slate-300">Alerts</span>
          <SegmentedControl options={ROUTING_OPTIONS} value={mode} onChange={setMode} label="Alert routing" />
          {mode === 'custom' && (
            activeChannels.length === 0 ? (
              <Callout tone="warning" className="mt-3">
                No active channels. <Link href="/settings?tab=channels" className={INLINE_LINK}>Add one in Settings</Link>.
              </Callout>
            ) : (
              <div
                role="group"
                aria-label="Alert channels"
                aria-describedby={fieldErrors.channelIds ? channelsErrorId : undefined}
                className="glass-well mt-3 space-y-1 p-1.5"
              >
                {activeChannels.map((c) => {
                  const checked = channelIds.includes(c.id);
                  return (
                    <Checkbox
                      key={c.id}
                      checked={checked}
                      onChange={(next) => toggleChannel(c.id, next)}
                      stretch
                      className={`w-full rounded-lg px-3 !py-2 transition-colors ${checked ? 'bg-well-strong' : 'hover:bg-well-hover'}`}
                      label={
                        <span className="flex items-center gap-3">
                          <span className="min-w-0 flex-1 truncate">{c.name}</span>
                          <span className="flex-shrink-0 text-xs text-slate-500">{c.type}</span>
                        </span>
                      }
                    />
                  );
                })}
              </div>
            )
          )}
          {fieldErrors.channelIds && <p id={channelsErrorId} className={`mt-1 text-xs ${TONE_TEXT.danger}`}>{fieldErrors.channelIds}</p>}
        </div>
        <p className="text-xs text-slate-500">Created enabled with a 10-second timeout and two-failure confirmation. Edit the monitor afterwards for paths, assertions or credentials.</p>
      </form>
    </Dialog>
  );
}
