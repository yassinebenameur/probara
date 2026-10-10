import { ReactNode } from 'react';

interface PanelProps {
  title?: ReactNode;
  subtitle?: string;
  actions?: ReactNode;
  allowOverflow?: boolean;
  className?: string;
  children: ReactNode;
}

export default function Panel({
  title,
  subtitle,
  actions,
  allowOverflow = false,
  className = '',
  children,
}: PanelProps) {
  return (
    <div className={`glass relative ${allowOverflow ? 'overflow-visible' : 'overflow-hidden'} p-5 ${className}`.trim()}>
      {(title || actions) && (
        <div className="mb-4 flex items-center justify-between gap-3">
          <div>
            {title && <h2 className="text-title font-semibold text-white">{title}</h2>}
            {subtitle && (
              <p className="mt-0.5 text-xs text-slate-500">{subtitle}</p>
            )}
          </div>
          {actions && (
            <div className="flex items-center gap-2 text-xs">
              {actions}
            </div>
          )}
        </div>
      )}

      <div>{children}</div>
    </div>
  );
}
