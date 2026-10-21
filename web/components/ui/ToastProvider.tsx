'use client';

import {
  createContext,
  KeyboardEvent as ReactKeyboardEvent,
  ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import Link from 'next/link';
import { CircleAlert, CircleCheck, Info, OctagonAlert, X } from 'lucide-react';
import { FOCUS_RING } from './focus';

export type ToastType = 'success' | 'error' | 'warning' | 'info';

export interface ToastAction {
  label: string;
  /** Runs, then the toast dismisses. */
  onClick?: () => void;
  /** Navigates (client-side), then the toast dismisses. */
  href?: string;
}

/** One show() call folded into a toast; a coalesced toast holds several. */
export interface ToastEntry {
  title?: string;
  message: string;
  type: ToastType;
  /** Caller data for `summarize` (an Alert, a monitor name…). */
  meta?: unknown;
}

export interface ToastOptions {
  /** Body text. `description` is accepted as an alias. */
  message?: string;
  description?: string;
  title?: string;
  type?: ToastType;
  /** ms; Infinity keeps it until dismissed. Defaults by type (see DURATION). */
  duration?: number;
  /**
   * Coalescing key: a show() with the group of a toast still on screen
   * updates that toast (count, text, restarted timer) instead of stacking a
   * new one. E.g. 'alert-created' turns 5 alerts in 2s into one toast.
   */
  group?: string;
  /** Text for a coalesced toast, from every entry so far (oldest first). */
  summarize?: (entries: ToastEntry[]) => { title?: string; message: string };
  meta?: unknown;
  action?: ToastAction;
  /**
   * Screen-reader urgency. Default: errors are assertive, everything else
   * polite. Ambient stream events should pass 'polite' even when red.
   */
  politeness?: 'polite' | 'assertive';
}

export interface ToastMessage {
  id: string;
  title?: string;
  message: string;
  type: ToastType;
  duration: number;
  group?: string;
  action?: ToastAction;
  politeness: 'polite' | 'assertive';
  /** Entries folded into this toast (1 unless coalesced). */
  count: number;
  entries: ToastEntry[];
  summarize?: ToastOptions['summarize'];
  createdAt: number;
  /** Bumped whenever the content changes: restarts the timer and progress bar. */
  version: number;
  leaving: boolean;
}

interface ToastContextType {
  toasts: ToastMessage[];
  /**
   * `showToast(message, type?, duration?)` or `showToast({ title, message,
   * type, group, action, … })`. Returns the toast id (the coalesced toast's
   * id when it merged into one on screen).
   */
  showToast: {
    (message: string, type?: ToastType, duration?: number): string;
    (options: ToastOptions): string;
  };
  /** Change a toast in place (a run-now "Running…" toast becoming its result). */
  updateToast: (id: string, patch: ToastOptions) => void;
  dismissToast: (id: string) => void;
  /** Alias of dismissToast, kept for existing callers. */
  removeToast: (id: string) => void;
  dismissAll: () => void;
  /**
   * @deprecated The alert stream builds its own toasts (lib/alert-toast.ts).
   * Kept so older callers still compile.
   */
  showAlertToast: (alertName: string, monitorName: string, eventType: 'created' | 'acknowledged' | 'resolved') => void;
}

const ToastContext = createContext<ToastContextType | undefined>(undefined);

/** At most this many toasts on screen; the rest wait behind a "+N more" row. */
export const MAX_VISIBLE = 3;

// Long enough to read (WCAG 2.2.1); errors stay longest. Hover, focus and a
// hidden tab pause every timer.
const DURATION: Record<ToastType, number> = {
  success: 4000,
  info: 5000,
  warning: 7000,
  error: 10000,
};

const EXIT_MS = 200;

let sequence = 0;
const nextId = () => `toast-${Date.now().toString(36)}-${(sequence++).toString(36)}`;

function normalize(arg: string | ToastOptions, type?: ToastType, duration?: number): ToastOptions {
  if (typeof arg === 'string') return { message: arg, type, duration };
  return arg;
}

function entryOf(options: ToastOptions): ToastEntry {
  return {
    title: options.title,
    message: options.message ?? options.description ?? '',
    type: options.type ?? 'info',
    meta: options.meta,
  };
}

export function ToastProvider({ children }: { children: ReactNode }) {
  // The list lives in a ref so showToast can decide synchronously whether it
  // merges into a toast on screen (and return that id); state mirrors it.
  const listRef = useRef<ToastMessage[]>([]);
  const [toasts, setToasts] = useState<ToastMessage[]>([]);
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const [hidden, setHidden] = useState(false);
  const [announcements, setAnnouncements] = useState({ polite: '', assertive: '' });
  const announceTimers = useRef<{ polite?: number; assertive?: number }>({});
  // Text waiting to be read, per region, keyed by toast id: a coalesced
  // toast replaces its own entry, other toasts in the same window queue up.
  const pendingAnnouncements = useRef({ polite: new Map<string, string>(), assertive: new Map<string, string>() });
  const exitTimers = useRef(new Map<string, number>());

  const commit = useCallback(() => setToasts([...listRef.current]), []);

  // Live regions stay mounted; their text changes. Debounced so a burst that
  // coalesces is read once, as its summary.
  const announce = useCallback((toast: ToastMessage) => {
    const region = toast.politeness;
    const pending = pendingAnnouncements.current[region];
    pending.set(toast.id, [toast.title, toast.message].filter(Boolean).join('. '));
    window.clearTimeout(announceTimers.current[region]);
    announceTimers.current[region] = window.setTimeout(() => {
      const text = Array.from(pending.values()).join('. ');
      pending.clear();
      setAnnouncements((prev) => ({
        ...prev,
        // A trailing no-break space makes a repeated message a change.
        [region]: prev[region] === text ? `${text}\u00a0` : text,
      }));
    }, 400);
  }, []);

  const removeNow = useCallback(
    (id: string) => {
      listRef.current = listRef.current.filter((t) => t.id !== id);
      exitTimers.current.delete(id);
      commit();
    },
    [commit]
  );

  const dismissToast = useCallback(
    (id: string) => {
      const toast = listRef.current.find((t) => t.id === id);
      if (!toast || toast.leaving) return;
      listRef.current = listRef.current.map((t) => (t.id === id ? { ...t, leaving: true } : t));
      commit();
      exitTimers.current.set(id, window.setTimeout(() => removeNow(id), EXIT_MS));
    },
    [commit, removeNow]
  );

  const dismissAll = useCallback(() => {
    listRef.current.forEach((t) => dismissToast(t.id));
  }, [dismissToast]);

  const showToast = useCallback(
    (arg: string | ToastOptions, type?: ToastType, duration?: number): string => {
      const options = normalize(arg, type, duration);
      const entry = entryOf(options);
      const toastType = options.type ?? 'info';

      if (options.group) {
        const existing = listRef.current.find((t) => t.group === options.group && !t.leaving);
        if (existing) {
          const entries = [...existing.entries, entry];
          const summarize = options.summarize ?? existing.summarize;
          const text = summarize ? summarize(entries) : { title: entry.title ?? existing.title, message: entry.message };
          const merged: ToastMessage = {
            ...existing,
            title: text.title,
            message: text.message,
            // A merged toast takes the most severe type it has seen.
            type: entries.some((e) => e.type === 'error') ? 'error' : toastType,
            action: options.action ?? existing.action,
            summarize,
            entries,
            count: entries.length,
            version: existing.version + 1,
            duration: options.duration ?? existing.duration,
          };
          // Move it to the newest slot so the update is where the eye is.
          listRef.current = [...listRef.current.filter((t) => t.id !== existing.id), merged];
          commit();
          announce(merged);
          return existing.id;
        }
      }

      const toast: ToastMessage = {
        id: nextId(),
        title: options.title,
        message: entry.message,
        type: toastType,
        duration: options.duration ?? DURATION[toastType],
        group: options.group,
        action: options.action,
        politeness: options.politeness ?? (toastType === 'error' ? 'assertive' : 'polite'),
        count: 1,
        entries: [entry],
        summarize: options.summarize,
        createdAt: Date.now(),
        version: 0,
        leaving: false,
      };
      listRef.current = [...listRef.current, toast];
      commit();
      announce(toast);
      return toast.id;
    },
    [announce, commit]
  ) as ToastContextType['showToast'];

  const updateToast = useCallback(
    (id: string, patch: ToastOptions) => {
      const current = listRef.current.find((t) => t.id === id);
      if (!current) return;
      const nextType = patch.type ?? current.type;
      const message = patch.message ?? patch.description ?? current.message;
      const updated: ToastMessage = {
        ...current,
        title: patch.title ?? current.title,
        message,
        type: nextType,
        duration: patch.duration ?? (patch.type ? DURATION[nextType] : current.duration),
        action: patch.action ?? current.action,
        politeness: patch.politeness ?? (patch.type ? (nextType === 'error' ? 'assertive' : 'polite') : current.politeness),
        version: current.version + 1,
        leaving: false,
      };
      listRef.current = listRef.current.map((t) => (t.id === id ? updated : t));
      const exit = exitTimers.current.get(id);
      if (exit) {
        window.clearTimeout(exit);
        exitTimers.current.delete(id);
      }
      commit();
      announce(updated);
    },
    [announce, commit]
  );

  const showAlertToast = useCallback(
    (_alertName: string, monitorName: string, eventType: 'created' | 'acknowledged' | 'resolved') => {
      if (eventType === 'acknowledged') return;
      showToast({
        title: eventType === 'created' ? 'Alert triggered' : 'Alert resolved',
        message: eventType === 'created' ? `${monitorName} is down` : monitorName,
        type: eventType === 'created' ? 'error' : 'success',
        group: `alert-${eventType}`,
        politeness: 'polite',
      });
    },
    [showToast]
  );

  useEffect(() => {
    const onVisibility = () => setHidden(document.visibilityState === 'hidden');
    onVisibility();
    document.addEventListener('visibilitychange', onVisibility);
    return () => document.removeEventListener('visibilitychange', onVisibility);
  }, []);

  useEffect(() => {
    const timers = announceTimers.current;
    const exits = exitTimers.current;
    return () => {
      window.clearTimeout(timers.polite);
      window.clearTimeout(timers.assertive);
      exits.forEach((t) => window.clearTimeout(t));
    };
  }, []);

  const paused = hovered || focused || hidden;
  const visible = toasts.slice(-MAX_VISIBLE);
  const waiting = toasts.length - visible.length;

  const onRegionKeyDown = (event: ReactKeyboardEvent<HTMLElement>) => {
    if (event.key !== 'Escape') return;
    const item = (event.target as HTMLElement).closest<HTMLElement>('[data-toast-id]');
    const id = item?.dataset.toastId ?? visible[visible.length - 1]?.id;
    if (!id) return;
    event.preventDefault();
    event.stopPropagation();
    dismissToast(id);
  };

  const value = useMemo(
    () => ({ toasts, showToast, updateToast, dismissToast, removeToast: dismissToast, dismissAll, showAlertToast }),
    [toasts, showToast, updateToast, dismissToast, dismissAll, showAlertToast]
  );

  return (
    <ToastContext.Provider value={value}>
      {children}
      <section
        aria-label="Notifications"
        // --toast-offset (app/globals.css) lifts the stack above a floating
        // action bar, such as the monitors list's bulk actions.
        className="pointer-events-none fixed bottom-[var(--toast-offset)] right-4 z-[60] flex w-[min(22rem,calc(100vw-2rem))] flex-col items-stretch gap-2 transition-[bottom] duration-slow ease-emphasized sm:right-6"
        onMouseEnter={() => setHovered(true)}
        onMouseLeave={() => setHovered(false)}
        onFocus={() => setFocused(true)}
        onBlur={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setFocused(false);
        }}
        onKeyDown={onRegionKeyDown}
      >
        {waiting > 0 && (
          <div className="pointer-events-auto flex animate-fade-in items-center justify-between gap-2 self-end rounded-full border border-edge-control bg-[rgb(var(--panel-rgb)/0.95)] py-1 pl-3 pr-1 text-xs text-slate-300 shadow-glass">
            <span className="tabular-nums">+{waiting} more</span>
            <button
              type="button"
              onClick={dismissAll}
              className={`rounded-full px-2 py-0.5 font-medium text-slate-400 transition-colors duration-fast hover:bg-well-hover hover:text-white ${FOCUS_RING}`}
            >
              Clear all
            </button>
          </div>
        )}
        {visible.map((toast) => (
          <ToastItem key={toast.id} toast={toast} paused={paused} onClose={() => dismissToast(toast.id)} />
        ))}
        {/* Persistent live regions: the visual toasts carry no live role. */}
        <div role="status" aria-live="polite" aria-atomic="true" className="sr-only">
          {announcements.polite}
        </div>
        <div role="alert" aria-live="assertive" aria-atomic="true" className="sr-only">
          {announcements.assertive}
        </div>
      </section>
    </ToastContext.Provider>
  );
}

const TYPE_CONFIG: Record<ToastType, { Icon: typeof Info; tint: string; bar: string }> = {
  error: { Icon: OctagonAlert, tint: 'bg-rose-500/[0.14] text-rose-400', bar: 'bg-rose-500' },
  success: { Icon: CircleCheck, tint: 'bg-emerald-500/[0.14] text-emerald-400', bar: 'bg-emerald-500' },
  warning: { Icon: CircleAlert, tint: 'bg-amber-500/[0.16] text-amber-400', bar: 'bg-amber-500' },
  info: { Icon: Info, tint: 'bg-sky-500/[0.14] text-sky-400', bar: 'bg-sky-500' },
};

function ToastItem({ toast, paused, onClose }: { toast: ToastMessage; paused: boolean; onClose: () => void }) {
  // Remaining time survives pauses; a content change (version) restarts it.
  const remaining = useRef(toast.duration);

  useEffect(() => {
    remaining.current = toast.duration;
  }, [toast.version, toast.duration]);

  useEffect(() => {
    if (paused || toast.leaving || !Number.isFinite(remaining.current)) return;
    const started = Date.now();
    const timer = window.setTimeout(onClose, remaining.current);
    return () => {
      window.clearTimeout(timer);
      remaining.current = Math.max(0, remaining.current - (Date.now() - started));
    };
    // onClose is recreated each render; the id behind it is stable.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [paused, toast.leaving, toast.version, toast.duration]);

  const config = TYPE_CONFIG[toast.type];
  const name = toast.title ?? toast.message;

  // Enter is an animation on the outer box, exit a transition on the inner
  // one: a filled animation would otherwise pin opacity and block the exit.
  return (
    <div data-toast-id={toast.id} className="pointer-events-auto animate-toast-in">
      <div
        className={`glass-overlay relative overflow-hidden !rounded-2xl transition-[transform,opacity] duration-base ease-exit ${
          toast.leaving ? 'translate-x-2 opacity-0' : ''
        }`}
      >
        <div className="flex items-start gap-3 p-3.5">
          <div className={`flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-full ${config.tint}`}>
            <config.Icon className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
          </div>

          <div className="min-w-0 flex-1 pt-1">
            {toast.title && (
              <p className="flex items-center gap-1.5 text-sm font-semibold text-white">
                <span key={toast.version} className="min-w-0 animate-value-in">
                  {toast.title}
                </span>
                {toast.count > 1 && !toast.summarize && (
                  <span className="rounded-full bg-well-hover px-1.5 text-2xs font-medium tabular-nums text-slate-400">
                    ×{toast.count}
                  </span>
                )}
              </p>
            )}
            <p className={`break-words text-sm ${toast.title ? 'text-slate-400' : 'text-white'}`}>{toast.message}</p>
            {toast.action && (
              <div className="mt-2">
                {toast.action.href ? (
                  <Link
                    href={toast.action.href}
                    onClick={() => {
                      toast.action?.onClick?.();
                      onClose();
                    }}
                    className={`inline-flex h-7 items-center rounded-lg border border-edge-control bg-control px-2.5 text-xs font-medium text-slate-200 transition-colors duration-fast hover:bg-control-hover hover:text-white active:bg-well-hover ${FOCUS_RING}`}
                  >
                    {toast.action.label}
                  </Link>
                ) : (
                  <button
                    type="button"
                    onClick={() => {
                      toast.action?.onClick?.();
                      onClose();
                    }}
                    className={`inline-flex h-7 items-center rounded-lg border border-edge-control bg-control px-2.5 text-xs font-medium text-slate-200 transition-colors duration-fast hover:bg-control-hover hover:text-white active:bg-well-hover ${FOCUS_RING}`}
                  >
                    {toast.action.label}
                  </button>
                )}
              </div>
            )}
          </div>

          <button
            type="button"
            onClick={onClose}
            className={`flex-shrink-0 rounded-lg p-1.5 text-slate-500 transition-colors duration-fast hover:bg-well-hover hover:text-slate-200 active:bg-control-hover ${FOCUS_RING}`}
            aria-label={`Dismiss: ${name}`}
            title="Dismiss (Esc)"
          >
            <X className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
          </button>
        </div>

        {Number.isFinite(toast.duration) && (
          <div className="absolute inset-x-0 bottom-0 h-0.5 bg-well-hover">
            <div
              key={toast.version}
              className={`h-full ${config.bar} animate-toast-progress opacity-60`}
              style={{ animationDuration: `${toast.duration}ms`, animationPlayState: paused ? 'paused' : 'running' }}
            />
          </div>
        )}
      </div>
    </div>
  );
}

export function useToast() {
  const context = useContext(ToastContext);
  if (context === undefined) {
    throw new Error('useToast must be used within a ToastProvider');
  }
  return context;
}
