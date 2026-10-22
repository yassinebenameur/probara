import { ReactNode } from 'react';
import { AlertTriangle, CircleCheck, Info, OctagonAlert } from 'lucide-react';

export type CalloutTone = 'warning' | 'danger' | 'info' | 'success';

// Tone lives in the icon, ring and a light tint laid over glass; the text stays
// neutral so it reads the same in both themes. No blur, so a callout is safe
// inside a panel (see the surface rules in app/globals.css).
const TONE: Record<CalloutTone, { scale: string; ring: string; icon: string; Icon: typeof Info }> = {
  warning: { scale: 'amber', ring: 'ring-amber-500/30', icon: 'text-amber-400', Icon: AlertTriangle },
  danger: { scale: 'rose', ring: 'ring-rose-500/30', icon: 'text-rose-400', Icon: OctagonAlert },
  info: { scale: 'sky', ring: 'ring-sky-500/30', icon: 'text-sky-400', Icon: Info },
  success: { scale: 'emerald', ring: 'ring-emerald-500/30', icon: 'text-emerald-400', Icon: CircleCheck },
};

interface CalloutProps {
  tone?: CalloutTone;
  title?: ReactNode;
  /** Replaces the tone's default icon. */
  icon?: ReactNode;
  /** Right-aligned control, e.g. a Dismiss or Fix button. */
  action?: ReactNode;
  /**
   * Announce it when it appears: `assertive` (role=alert) is the default for
   * danger — a failed save or load inserted after the fact — `off` for the
   * rest. Static notices present at page load are not read either way.
   */
  live?: 'off' | 'polite' | 'assertive';
  className?: string;
  children?: ReactNode;
}

/** Inline banner for warnings, errors and notices. */
export default function Callout({ tone = 'info', title, icon, action, live, className = '', children }: CalloutProps) {
  const t = TONE[tone];
  const politeness = live ?? (tone === 'danger' ? 'assertive' : 'off');
  const tint = `rgb(var(--tw-${t.scale}-500) / 0.09)`;
  return (
    <div
      role={politeness === 'assertive' ? 'alert' : politeness === 'polite' ? 'status' : undefined}
      className={`flex items-start gap-3 rounded-2xl px-4 py-3 text-sm ring-1 ring-inset ${t.ring} ${className}`.trim()}
      style={{ background: `linear-gradient(${tint}, ${tint}), var(--glass-fill)` }}
    >
      <span className={`mt-0.5 flex-shrink-0 [&_svg]:h-4 [&_svg]:w-4 ${t.icon}`} aria-hidden="true">
        {icon ?? <t.Icon strokeWidth={1.75} />}
      </span>
      <div className="min-w-0 flex-1">
        {title && <p className="font-medium text-white">{title}</p>}
        {children && <div className={`text-slate-300 ${title ? 'mt-0.5' : ''}`}>{children}</div>}
      </div>
      {action && <div className="flex-shrink-0 self-center">{action}</div>}
    </div>
  );
}
