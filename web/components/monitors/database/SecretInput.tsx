'use client';

import { useState } from 'react';
import { Eye, EyeOff, KeyRound, X } from 'lucide-react';
import { FOCUS_RING } from '@/components/ui/focus';

// Write-only secret input: the API never returns stored secrets (only "***"),
// so in edit mode an untouched blank field means "keep the current value".
// onClear (when provided) lets the user drop the stored secret entirely.
export default function SecretInput({
  value,
  onChange,
  hasStored,
  onClear,
  placeholder,
  storedPlaceholder,
  mono = false,
  error,
  ariaLabel,
}: {
  value: string;
  onChange: (value: string) => void;
  hasStored: boolean;
  onClear?: () => void;
  placeholder: string;
  storedPlaceholder: string;
  mono?: boolean;
  error?: string;
  /** The field's name; FormField cannot link its label through this wrapper. */
  ariaLabel?: string;
}) {
  const [reveal, setReveal] = useState(false);
  return (
    <div>
      <div className="relative">
        <input
          type={reveal ? 'text' : 'password'}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={hasStored ? storedPlaceholder : placeholder}
          aria-label={ariaLabel}
          autoComplete="new-password"
          className={`input ${hasStored && !value ? '!pr-28' : '!pr-10'} ${mono ? 'font-mono' : ''}`}
        />
        <div className="absolute right-2 top-1/2 flex -translate-y-1/2 items-center gap-1">
          {hasStored && !value && (
            <span className={`flex items-center gap-1 rounded-full bg-well-hover py-0.5 pl-2 text-2xs font-medium text-slate-300 ${onClear ? 'pr-1' : 'pr-2'}`} title="A value is stored. Leave blank to keep it.">
              <KeyRound className="h-3 w-3" strokeWidth={1.75} />
              set
              {onClear && (
                <button
                  type="button"
                  onClick={onClear}
                  className={`rounded-full p-0.5 text-slate-400 transition-colors hover:bg-control-hover hover:text-white ${FOCUS_RING}`}
                  aria-label="Clear stored value"
                  title="Clear the stored value"
                >
                  <X className="h-3 w-3" strokeWidth={1.75} />
                </button>
              )}
            </span>
          )}
          <button
            type="button"
            onClick={() => setReveal(!reveal)}
            className={`rounded-lg p-1 text-slate-400 transition-colors hover:text-white ${FOCUS_RING}`}
            aria-label={reveal ? 'Hide value' : 'Show value'}
            aria-pressed={reveal}
          >
            {reveal ? <EyeOff className="h-4 w-4" strokeWidth={1.75} /> : <Eye className="h-4 w-4" strokeWidth={1.75} />}
          </button>
        </div>
      </div>
      {error && <p className="mt-1 text-xs text-rose-300">{error}</p>}
    </div>
  );
}
