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
        <label className="mb-1.5 block text-[13px] font-medium text-slate-300">
          {label}
        </label>
      )}
      <select
        className={`input ${error ? '!border-rose-500/60 focus:!border-rose-500' : ''} ${className}`}
        {...props}
      >
        {children}
      </select>
      {error && (
        <p className="mt-1 text-xs text-rose-300">{error}</p>
      )}
    </div>
  );
}
