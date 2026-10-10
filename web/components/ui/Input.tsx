import { InputHTMLAttributes, useId } from 'react';

interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  label?: string;
  error?: string;
}

export default function Input({ label, error, className = '', id, ...props }: InputProps) {
  const autoId = useId();
  const inputId = id ?? autoId;
  const errorId = `${inputId}-error`;
  return (
    <div>
      {label && (
        <label htmlFor={inputId} className="mb-1.5 block text-ui font-medium text-slate-300">
          {label}
        </label>
      )}
      <input
        id={inputId}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? errorId : undefined}
        className={`input ${
          error
            ? '!border-rose-500/60 focus:!border-rose-500 focus:!shadow-[0_0_0_3px_rgba(240,74,90,0.16)]'
            : ''
        } ${className}`}
        {...props}
      />
      {error && (
        <p id={errorId} className="mt-1 text-xs text-rose-300">{error}</p>
      )}
    </div>
  );
}
