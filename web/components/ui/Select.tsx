import { SelectHTMLAttributes, ReactNode } from 'react';

interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {
  label?: string;
  error?: string;
  children: ReactNode;
}

export default function Select({
  label,
  error,
  className = '',
  children,
  ...props
}: SelectProps) {
  return (
    <div>
      {label && (
        <label className="block text-xs font-medium text-slate-400 mb-1.5">
          {label}
        </label>
      )}
      <select
        className={`input ${error ? 'border-rose-500/60 focus:border-rose-500/60' : ''} ${className}`}
        {...props}
      >
        {children}
      </select>
      {error && (
        <p className="mt-1 text-xs text-rose-400">{error}</p>
      )}
    </div>
  );
}
