import { InputHTMLAttributes } from 'react';

interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  label?: string;
  error?: string;
}

export default function Input({ label, error, className = '', ...props }: InputProps) {
  return (
    <div>
      {label && (
        <label className="mb-1.5 block text-[13px] font-medium text-slate-300">
          {label}
        </label>
      )}
      <input
        className={`input ${
          error
            ? '!border-rose-500/60 focus:!border-rose-500 focus:!shadow-[0_0_0_3px_rgba(240,74,90,0.16)]'
            : ''
        } ${className}`}
        {...props}
      />
      {error && (
        <p className="mt-1 text-xs text-rose-300">{error}</p>
      )}
    </div>
  );
}
