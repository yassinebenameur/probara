import { ReactNode } from 'react';

interface EmptyStateProps {
  icon?: ReactNode;
  title: string;
  description?: string;
  action?: ReactNode;
}

export default function EmptyState({ icon, title, description, action }: EmptyStateProps) {
  return (
    <div className="flex flex-col items-center justify-center rounded-xl border border-white/[0.06] bg-slate-900/40 px-6 py-12 text-center">
      {icon && (
        <span
          className="mb-4 inline-flex h-9 w-9 items-center justify-center text-slate-500"
          aria-hidden="true"
        >
          {icon}
        </span>
      )}
      <h3 className="text-base font-medium text-slate-200">{title}</h3>
      {description && (
        <p className="mt-1 max-w-sm text-sm text-slate-500">{description}</p>
      )}
      {action && <div className="mt-5">{action}</div>}
    </div>
  );
}
