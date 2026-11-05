'use client';

import { useEffect, useMemo, useState } from 'react';
import { Plus, Trash2, X } from 'lucide-react';
import {
  createEscalationPolicy, getAlertChannels, getOnCallPeople, getOnCallSchedules, updateEscalationPolicy,
} from '@/lib/api';
import {
  MAX_REPEAT_COUNT, MAX_STEPS, MAX_TARGETS_PER_STEP, TARGET_KIND_LABELS, makeTarget, policyInputError, targetRef,
  toPolicyInput, type EscalationPolicy, type EscalationPolicyInput, type EscalationStep, type EscalationTarget,
  type EscalationTargetKind,
} from '@/lib/escalation';
import type { OnCallPerson, OnCallSchedule } from '@/lib/oncall';
import type { AlertChannel } from '@/lib/types';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Dialog from '@/components/ui/Dialog';
import IconButton from '@/components/ui/IconButton';

interface Props {
  policy?: EscalationPolicy; // present = edit mode
  onDone: (p: EscalationPolicy) => void;
  onCancel: () => void;
}

const KINDS: EscalationTargetKind[] = ['user', 'schedule', 'all_admins', 'channel'];

const REPEAT_AFTER_OPTIONS = [5, 10, 15, 30, 60, 120, 240].map((m) => ({ value: m * 60, label: `${m < 60 ? `${m} min` : `${m / 60} h`}` }));

function repeatAfterOptions(current: number) {
  if (REPEAT_AFTER_OPTIONS.some((o) => o.value === current)) return REPEAT_AFTER_OPTIONS;
  return [...REPEAT_AFTER_OPTIONS, { value: current, label: `${Math.round(current / 60)} min` }].sort((a, b) => a.value - b.value);
}

const label = 'mb-1.5 block text-ui font-medium text-slate-300';
const labelHint = 'font-normal text-slate-500';

function blankStep(previousDelay: number): EscalationStep {
  return { delay_seconds: previousDelay + 600, targets: [makeTarget('user', '')] };
}

export function PolicyFormModal({ policy, onDone, onCancel }: Props) {
  const [name, setName] = useState(policy?.name ?? '');
  const [description, setDescription] = useState(policy?.description ?? '');
  const [repeatCount, setRepeatCount] = useState(policy?.repeat_count ?? 0);
  const [repeatAfter, setRepeatAfter] = useState(policy?.repeat_after_seconds ?? 1800);
  const [steps, setSteps] = useState<EscalationStep[]>(() =>
    policy ? policy.steps.map((s) => ({ ...s, targets: [...s.targets] })) : [{ delay_seconds: 0, targets: [makeTarget('user', '')] }]
  );
  const [people, setPeople] = useState<OnCallPerson[]>([]);
  const [schedules, setSchedules] = useState<OnCallSchedule[]>([]);
  const [channels, setChannels] = useState<AlertChannel[]>([]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    Promise.all([getOnCallPeople(), getOnCallSchedules(), getAlertChannels({ page_size: 100 })])
      .then(([p, s, c]) => {
        setPeople(p);
        setSchedules(s);
        setChannels(c.items ?? []);
      })
      .catch((err: any) => setError(err.message || 'Failed to load people, schedules and channels'));
  }, []);

  // Choices per kind, keeping a stored target that is no longer listed.
  const options = useMemo(() => {
    const byKind: Record<EscalationTargetKind, { id: string; label: string }[]> = {
      user: people.map((p) => ({ id: p.id, label: `${p.username}${p.verified_contacts === 0 ? ' (no verified contact)' : ''}` })),
      schedule: schedules.map((s) => ({ id: s.id, label: s.name })),
      channel: channels.map((c) => ({ id: c.id, label: `${c.name}${c.is_active === false ? ' (disabled)' : ''}` })),
      all_admins: [],
    };
    for (const step of policy?.steps ?? []) {
      for (const t of step.targets) {
        const ref = targetRef(t);
        if (ref && !byKind[t.kind].some((o) => o.id === ref)) byKind[t.kind].push({ id: ref, label: t.label || ref });
      }
    }
    return byKind;
  }, [people, schedules, channels, policy]);

  const input: EscalationPolicyInput = {
    name, description, repeat_count: repeatCount, repeat_after_seconds: repeatAfter, steps,
  };
  const validation = policyInputError(input);

  const updateStep = (i: number, patch: Partial<EscalationStep>) =>
    setSteps((prev) => prev.map((s, j) => (j === i ? { ...s, ...patch } : s)));
  const updateTarget = (i: number, k: number, next: EscalationTarget) =>
    updateStep(i, { targets: steps[i].targets.map((t, j) => (j === k ? next : t)) });

  const save = async () => {
    if (validation) {
      setError(validation);
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const body = toPolicyInput(input);
      onDone(policy ? await updateEscalationPolicy(policy.id, body) : await createEscalationPolicy(body));
    } catch (err: any) {
      setError(err.message || 'Save failed');
      setSaving(false);
    }
  };

  return (
    <Dialog
      onClose={onCancel}
      busy={saving}
      size="lg"
      title={policy ? 'Edit escalation policy' : 'New escalation policy'}
      description="Each step pages its targets once the alert has paged for its delay. Acknowledging or snoozing the alert stops the walk."
      bodyClassName="dashboard-scroll space-y-4"
      footer={
        <>
          <Button variant="ghost" size="sm" onClick={onCancel} disabled={saving}>Cancel</Button>
          <Button variant="accent" size="sm" onClick={save} loading={saving}>
            {policy ? 'Save changes' : 'Create policy'}
          </Button>
        </>
      }
    >
      <form noValidate className="space-y-4" onSubmit={(e) => { e.preventDefault(); save(); }}>
        <div>
          <label className={label} htmlFor="policy-name">Name</label>
          <input id="policy-name" className="input" value={name} maxLength={100} autoFocus
            placeholder="Platform primary" onChange={(e) => setName(e.target.value)} />
        </div>
        <div>
          <label className={label} htmlFor="policy-description">Description <span className={labelHint}>(optional)</span></label>
          <textarea id="policy-description" rows={2} maxLength={500} className="input resize-none" value={description}
            onChange={(e) => setDescription(e.target.value)} />
        </div>

        {steps.map((step, i) => (
          <div key={i} role="group" aria-label={`Step ${i + 1}`} className="glass-well space-y-3 p-4">
            <div className="flex flex-wrap items-end justify-between gap-3">
              <div className="flex items-end gap-3">
                <h3 className="pb-2 text-sm font-medium text-white">Step {i + 1}</h3>
                <div>
                  <label className={label} htmlFor={`policy-delay-${i}`}>After <span className={labelHint}>(minutes)</span></label>
                  <input id={`policy-delay-${i}`} type="number" min={0} max={10080} className="input input-sm !w-28"
                    value={Math.round(step.delay_seconds / 60)}
                    onChange={(e) => updateStep(i, { delay_seconds: Math.max(0, Math.round(Number(e.target.value) || 0)) * 60 })} />
                </div>
              </div>
              {steps.length > 1 && (
                <IconButton icon={<Trash2 strokeWidth={1.75} />} label={`Remove step ${i + 1}`} danger
                  onClick={() => setSteps((prev) => prev.filter((_, j) => j !== i))} />
              )}
            </div>
            <ul className="space-y-2">
              {step.targets.map((t, k) => (
                <li key={k} className="flex flex-wrap items-center gap-2">
                  <select aria-label={`Step ${i + 1} target ${k + 1} kind`} className="input input-sm !w-48"
                    value={t.kind} onChange={(e) => updateTarget(i, k, makeTarget(e.target.value as EscalationTargetKind, ''))}>
                    {KINDS.map((kind) => <option key={kind} value={kind}>{TARGET_KIND_LABELS[kind]}</option>)}
                  </select>
                  {t.kind !== 'all_admins' && (
                    <select aria-label={`Step ${i + 1} target ${k + 1}`} className="input input-sm min-w-0 flex-1"
                      value={targetRef(t)} onChange={(e) => updateTarget(i, k, { ...makeTarget(t.kind, e.target.value), notify_after_ack: t.notify_after_ack })}>
                      <option value="">Choose…</option>
                      {options[t.kind].map((o) => <option key={o.id} value={o.id}>{o.label}</option>)}
                    </select>
                  )}
                  {t.kind === 'channel' && (
                    <label className="flex items-center gap-1.5 text-xs text-slate-400">
                      <input type="checkbox" checked={!!t.notify_after_ack}
                        onChange={(e) => updateTarget(i, k, { ...t, notify_after_ack: e.target.checked })} />
                      Keep after ack
                    </label>
                  )}
                  {step.targets.length > 1 && (
                    <IconButton icon={<X strokeWidth={1.75} />} label="Remove target" danger
                      onClick={() => updateStep(i, { targets: step.targets.filter((_, j) => j !== k) })} />
                  )}
                </li>
              ))}
            </ul>
            <Button size="xs" icon={<Plus strokeWidth={1.75} />} disabled={step.targets.length >= MAX_TARGETS_PER_STEP}
              onClick={() => updateStep(i, { targets: [...step.targets, makeTarget('user', '')] })}>
              Add target
            </Button>
          </div>
        ))}

        <Button size="xs" icon={<Plus strokeWidth={1.75} />} disabled={steps.length >= MAX_STEPS}
          onClick={() => setSteps((prev) => [...prev, blankStep(prev[prev.length - 1]?.delay_seconds ?? 0)])}>
          Add step
        </Button>

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label className={label} htmlFor="policy-repeat">Repeat <span className={labelHint}>(people only)</span></label>
            <select id="policy-repeat" className="input" value={repeatCount} onChange={(e) => setRepeatCount(Number(e.target.value))}>
              {Array.from({ length: MAX_REPEAT_COUNT + 1 }, (_, n) => (
                <option key={n} value={n}>{n === 0 ? 'Never' : n === 1 ? 'Once more' : `${n} more times`}</option>
              ))}
            </select>
          </div>
          <div>
            <label className={label} htmlFor="policy-repeat-after">Pause before a repeat</label>
            <select id="policy-repeat-after" className="input" value={repeatAfter} disabled={repeatCount === 0}
              onChange={(e) => setRepeatAfter(Number(e.target.value))}>
              {repeatAfterOptions(repeatAfter).map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
            </select>
          </div>
        </div>
        <p className="text-xs text-slate-500">
          People are paged at their verified contacts: email, and SMS through the workspace&apos;s SMS channel for people.
          Channel targets follow the workspace reminders instead of repeating.
        </p>

        {error && <Callout tone="danger">{error}</Callout>}
      </form>
    </Dialog>
  );
}
