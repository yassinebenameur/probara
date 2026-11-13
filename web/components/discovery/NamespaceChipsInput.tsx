'use client';

import { KeyboardEvent, ReactNode, useEffect, useRef } from 'react';
import { Lock, X } from 'lucide-react';
import type { FieldControlProps } from '@/components/ui/FormField';
import { FOCUS_RING } from '@/components/ui/focus';
import { MAX_NAMESPACES, parseNamespaces, removeButtonIndex, validNamespace } from '@/lib/discovery';
import { TONE_TEXT } from '@/lib/status-tones';

interface NamespaceChipsInputProps {
  /** FormField's control props (id, aria-invalid, aria-describedby). */
  field: FieldControlProps;
  /** Committed namespaces, in order. */
  value: string[];
  onChange: (namespaces: string[]) => void;
  /** Text still being typed; the parent counts it toward the limit and refuses an invalid one on submit. */
  draft: string;
  onDraftChange: (draft: string) => void;
  /** A chip that cannot be removed (the collector's home namespace), with why. */
  locked?: { namespace: string; reason: string };
  /** Extra text after a chip's name, such as its state. */
  chipNote?: (namespace: string) => ReactNode;
  autoFocus?: boolean;
}

/**
 * Namespace chips plus a free-text draft: Enter, comma or space commits the
 * draft, Backspace on an empty draft removes the last removable chip. Every
 * entry is kept, even past the limit: the field error says how many to remove
 * and the form blocks, rather than silently dropping the overflow.
 */
export default function NamespaceChipsInput({ field, value, onChange, draft, onDraftChange, locked, chipNote, autoFocus }: NamespaceChipsInputProps) {
  // Removing a chip unmounts its focused × button: focus the chip that took
  // its place, else the one before, else the namespace input (as TagFilter).
  // The index is among the × buttons, which a locked chip does not have.
  const chipsRef = useRef<HTMLDivElement>(null);
  const draftRef = useRef<HTMLInputElement>(null);
  const refocusChip = useRef<number | null>(null);
  useEffect(() => {
    const index = refocusChip.current;
    if (index === null) return;
    refocusChip.current = null;
    const chips = chipsRef.current?.querySelectorAll<HTMLElement>('[data-namespace-remove]');
    (chips?.[Math.min(index, chips.length - 1)] ?? draftRef.current)?.focus();
  }, [value]);

  const removable = (ns: string) => ns !== locked?.namespace;
  const commitDraft = () => {
    const added = parseNamespaces(draft);
    if (added.length) onChange([...new Set([...value, ...added])]);
    onDraftChange('');
  };
  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' || e.key === ',' || e.key === ' ') { e.preventDefault(); commitDraft(); }
    if (e.key === 'Backspace' && !draft) {
      const last = value[value.length - 1];
      if (last !== undefined && removable(last)) onChange(value.slice(0, -1));
    }
  };

  return (
    <div ref={chipsRef} className="input flex min-h-10 flex-wrap items-center gap-1.5 !py-1.5 focus-within:!border-[var(--focus)] focus-within:bg-control-hover focus-within:shadow-[0_0_0_1px_var(--focus)]">
      {value.map((ns, index) => {
        // An invalid namespace, or one past the limit, takes Pill's danger tint.
        const ok = validNamespace(ns) && index < MAX_NAMESPACES;
        const note = chipNote?.(ns);
        return (
          <span key={ns} className={`inline-flex h-6 items-center gap-1 rounded-full pl-2.5 font-mono text-xs ${removable(ns) ? 'pr-0.5' : 'pr-2.5'} ${ok ? 'bg-well-hover text-slate-200' : `bg-rose-500/[0.13] ${TONE_TEXT.danger}`}`}>
            {!removable(ns) && <Lock className="h-3 w-3 text-slate-400" strokeWidth={2} aria-hidden="true" />}
            {ns}
            {!removable(ns) && locked && <span className="sr-only">({locked.reason})</span>}
            {note && <span className="font-sans text-2xs text-slate-400">{note}</span>}
            {removable(ns) && (
              <button type="button" data-namespace-remove="" aria-label={`Remove ${ns}`} className={`inline-flex h-5 w-5 items-center justify-center rounded-full text-slate-400 transition-colors hover:bg-control-hover hover:text-white ${FOCUS_RING}`} onClick={() => { refocusChip.current = removeButtonIndex(value, index, removable); onChange(value.filter((n) => n !== ns)); }}>
                <X className="h-3 w-3" strokeWidth={2} aria-hidden="true" />
              </button>
            )}
          </span>
        );
      })}
      <input
        {...field}
        ref={draftRef}
        autoFocus={autoFocus}
        className="min-w-[8rem] flex-1 border-0 bg-transparent p-0 font-mono text-sm text-white shadow-none outline-none ring-0 placeholder:font-sans placeholder:text-slate-500 focus:outline-none focus:ring-0 focus-visible:outline-none"
        value={draft}
        onChange={(e) => onDraftChange(e.target.value)}
        onKeyDown={onKey}
        onBlur={commitDraft}
        placeholder={value.length ? 'Add another…' : 'Type a namespace and press Enter'}
      />
    </div>
  );
}
