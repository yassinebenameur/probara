import { ReactNode } from 'react';

interface EmptyStateProps {
  icon?: ReactNode;
  title: string;
  description?: string;
  action?: ReactNode;
}

export default function EmptyState({ icon, title, description, action }: EmptyStateProps) {
  return (
    <div className="glass flex flex-col items-center justify-center px-6 py-14 text-center">
      {icon && (
        <span
          className="mb-4 inline-flex h-11 w-11 items-center justify-center rounded-full bg-well-hover text-slate-400 [&_svg]:h-5 [&_svg]:w-5"
          aria-hidden="true"
        >
          {icon}
        </span>
      )}
      <h3 className="text-[15px] font-semibold text-white">{title}</h3>
      {description && (
        <p className="mt-1.5 max-w-sm text-sm text-slate-400">{description}</p>
      )}
      {action && <div className="mt-5">{action}</div>}
    </div>
  );
}
