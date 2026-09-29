import { ReactNode } from 'react';

interface PanelProps {
  title?: string;
  subtitle?: string;
  actions?: ReactNode;
  allowOverflow?: boolean;
  children: ReactNode;
}

export default function Panel({
  title,
  subtitle,
  actions,
  allowOverflow = false,
  children,
}: PanelProps) {
  return (
    <div className={`relative ${allowOverflow ? 'overflow-visible' : 'overflow-hidden'} rounded-xl border border-white/[0.06] bg-slate-900/50 p-5`}>
      {(title || actions) && (
        <div className="mb-4 flex items-center justify-between gap-3">
          <div>
            {title && <div className="text-sm font-medium text-white">{title}</div>}
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
      )}

      {/* Content */}
      <div className="mt-1">{children}</div>
    </div>
  );
}
