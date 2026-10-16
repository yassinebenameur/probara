import { monitorTypeDisplay } from '@/lib/monitor-type-labels';

/**
 * Fixed-width monitor type tag (`HTTP`, `PING`, `PG`): mono, ≤5 chars, one
 * muted colour, so a column of them lines up and the target after it starts
 * at the same x. The full name is the tooltip and, as sr-only text, the
 * accessible name (aria-label on a plain span is ignored). Status colours stay
 * reserved for state. Pass the monitor's `config` where it is at hand so a
 * variant reads as itself: a SIP test call is `CALL`, not `SIP`.
 */
export default function TypeBadge({ type, config, className = '' }: { type: string; config?: unknown; className?: string }) {
  const { label, badge } = monitorTypeDisplay({ type, config });
  return (
    <span
      title={label}
      className={`inline-flex h-[18px] w-11 flex-shrink-0 items-center justify-center rounded border border-edge-control font-mono text-2xs font-medium tracking-wide text-slate-400 ${className}`.trim()}
    >
      <span aria-hidden="true">{badge}</span>
      <span className="sr-only">{label}</span>
    </span>
  );
}
