import { InputHTMLAttributes } from 'react';

interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  label?: string;
  error?: string;
}

export default function Input({ label, error, className = '', ...props }: InputProps) {
  return (
    <div>
      {label && (
        <label className="mb-1.5 block text-xs font-medium text-slate-400">
          {label}
        </label>
      )}
      <input
        className={`input ${
          error
            ? 'border-rose-500/50 bg-rose-500/5 focus:border-rose-500/70 focus:ring-1 focus:ring-rose-500/20'
            : ''
        } ${className}`}
        {...props}
      />
      {error && (
        <p className="mt-1 text-xs text-rose-400">{error}</p>
      )}
    </div>
  );
}
