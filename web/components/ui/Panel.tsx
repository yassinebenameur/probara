import { ReactNode } from 'react';

interface PanelProps {
  title: string;
  subtitle?: string;
  dotColor?: string;
  actions?: ReactNode;
  allowOverflow?: boolean;
  children: ReactNode;
}

export default function Panel({
  title,
  subtitle,
  dotColor = 'var(--success)',
  actions,
  allowOverflow = false,
  children,
}: PanelProps) {
  return (
    <div className={`relative ${allowOverflow ? 'overflow-visible' : 'overflow-hidden'} rounded-xl border border-white/[0.06] bg-slate-900/50 p-5`}>
      {/* Header */}
      <div className="mb-4 flex items-center justify-between gap-3">
        <div>
          <div className="flex items-center gap-2 text-sm font-medium text-white">
            <span
              className="h-2 w-2 rounded-full"
              style={{
                background: dotColor,
                boxShadow: `0 0 0 4px ${dotColor}26`,
              }}
            />
            {title}
          </div>
          {subtitle && (
            <div className="text-xs text-slate-500">{subtitle}</div>
          )}
        </div>
        {actions && (
          <div className="flex items-center gap-2 text-xs">
            {actions}
          </div>
        )}
      </div>

      {/* Content */}
      <div className="mt-1">{children}</div>
    </div>
  );
}
