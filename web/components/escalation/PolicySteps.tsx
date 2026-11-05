import { AlertTriangle } from 'lucide-react';
import { describeDelay, TARGET_KIND_LABELS, type EscalationStep, type EscalationTarget } from '@/lib/escalation';

/** One target as a short label, flagged when it cannot reach anybody now. */
export function TargetLabel({ target }: { target: EscalationTarget }) {
  const name = target.kind === 'all_admins' ? TARGET_KIND_LABELS.all_admins : target.label || TARGET_KIND_LABELS[target.kind];
  const unreachable = target.reachable === false;
  const why =
    target.kind === 'channel'
      ? 'This channel is disabled.'
      : 'Nobody here can be paged: no enabled member with a verified contact.';
  return (
    <span className={`inline-flex items-center gap-1 ${unreachable ? 'text-amber-300' : 'text-slate-300'}`} title={unreachable ? why : undefined}>
      {unreachable && <AlertTriangle className="h-3 w-3 shrink-0" strokeWidth={1.75} aria-hidden="true" />}
      <span className="text-slate-500">{target.kind === 'all_admins' ? '' : `${TARGET_KIND_LABELS[target.kind]}: `}</span>
      {name}
      {target.kind === 'channel' && target.notify_after_ack && <span className="text-slate-500">(after ack too)</span>}
      {unreachable && <span className="sr-only"> — {why}</span>}
    </span>
  );
}

/** A policy's steps as an ordered list, for tables and the monitor form. */
export function PolicySteps({ steps }: { steps: EscalationStep[] }) {
  return (
    <ol className="space-y-1 text-xs">
      {steps.map((step, i) => (
        <li key={i} className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
          <span className="w-24 shrink-0 tabular-nums text-slate-500">
            {i + 1}. {describeDelay(step.delay_seconds)}
          </span>
          <span className="flex min-w-0 flex-wrap gap-x-3 gap-y-0.5">
            {step.targets.map((t, j) => <TargetLabel key={j} target={t} />)}
          </span>
        </li>
      ))}
    </ol>
  );
}
