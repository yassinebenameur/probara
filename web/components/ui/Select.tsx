import { SelectHTMLAttributes, ReactNode, useId } from 'react';

interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {
  label?: string;
  error?: string;
  children: ReactNode;
}

export default function Select({
  label,
  error,
  className = '',
  id,
  children,
  ...props
}: SelectProps) {
  const autoId = useId();
  const selectId = id ?? autoId;
  const errorId = `${selectId}-error`;
  return (
    <div>
      {label && (
        <label htmlFor={selectId} className="mb-1.5 block text-ui font-medium text-slate-300">
          {label}
        </label>
      )}
      <select
        id={selectId}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? errorId : undefined}
        className={`input ${error ? '!border-rose-500/60 focus:!border-rose-500' : ''} ${className}`}
        {...props}
      >
        {children}
      </select>
      {error && (
        <p id={errorId} className="mt-1 text-xs text-rose-300">{error}</p>
      )}
    </div>
  );
}
