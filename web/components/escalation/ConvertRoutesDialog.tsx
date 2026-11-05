'use client';

import { useEffect, useState } from 'react';
import { convertRoutesToEscalationPolicy, getMonitors } from '@/lib/api';
import type { EscalationPolicy } from '@/lib/escalation';
import type { Monitor } from '@/lib/types';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Dialog from '@/components/ui/Dialog';

interface Props {
  onDone: (p: EscalationPolicy) => void;
  onCancel: () => void;
}

const WORKSPACE = '';

/**
 * "Convert routes to policy": builds a policy equivalent to a monitor's
 * custom routes, or to the workspace default routes (routes sharing a delay
 * become one step). A monitor can switch to the new policy in the same
 * change; the workspace default policy is set in notification settings.
 */
export function ConvertRoutesDialog({ onDone, onCancel }: Props) {
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [source, setSource] = useState(WORKSPACE);
  const [name, setName] = useState('');
  const [assign, setAssign] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    getMonitors({ page_size: 100 })
      .then((res) => setMonitors((res.items ?? []).filter((m) => m.notification_mode === 'custom')))
      .catch(() => setMonitors([]));
  }, []);

  const convert = async () => {
    setSaving(true);
    setError(null);
    try {
      onDone(await convertRoutesToEscalationPolicy({
        monitor_id: source || undefined,
        name: name.trim() || undefined,
        assign: source !== WORKSPACE && assign,
      }));
    } catch (err: any) {
      setError(err.message || 'Conversion failed');
      setSaving(false);
    }
  };

  return (
    <Dialog
      onClose={onCancel}
      busy={saving}
      title="Convert routes to a policy"
      description="Build an escalation policy that pages exactly what existing routes page. Routes sharing a delay become one step; add people to it afterwards."
      footer={
        <>
          <Button variant="ghost" size="sm" onClick={onCancel} disabled={saving}>Cancel</Button>
          <Button variant="accent" size="sm" onClick={convert} loading={saving}>Convert</Button>
        </>
      }
    >
      <form noValidate className="space-y-4" onSubmit={(e) => { e.preventDefault(); convert(); }}>
        <div>
          <label className="mb-1.5 block text-ui font-medium text-slate-300" htmlFor="convert-source">Routes</label>
          <select id="convert-source" className="input" value={source} onChange={(e) => setSource(e.target.value)}>
            <option value={WORKSPACE}>Workspace default channels</option>
            {monitors.map((m) => <option key={m.id} value={m.id}>{m.name} (custom routes)</option>)}
          </select>
        </div>
        <div>
          <label className="mb-1.5 block text-ui font-medium text-slate-300" htmlFor="convert-name">
            Name <span className="font-normal text-slate-500">(optional)</span>
          </label>
          <input id="convert-name" className="input" value={name} maxLength={100} onChange={(e) => setName(e.target.value)}
            placeholder={source ? `${monitors.find((m) => m.id === source)?.name ?? 'Monitor'} routes` : 'Workspace default routes'} />
        </div>
        {source !== WORKSPACE ? (
          <label className="flex items-center gap-2 text-sm text-slate-300">
            <input type="checkbox" checked={assign} onChange={(e) => setAssign(e.target.checked)} />
            Switch this monitor to the new policy
          </label>
        ) : (
          <p className="text-xs text-slate-500">
            Make it the workspace default in Settings, Notifications, once it has the people you want paged.
          </p>
        )}
        {error && <Callout tone="danger">{error}</Callout>}
      </form>
    </Dialog>
  );
}
