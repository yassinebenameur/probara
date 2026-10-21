'use client';

import { useEffect, useState } from 'react';
import { Monitor, Moon, Sun } from 'lucide-react';
import { FOCUS_RING, FOCUS_RING_INSET } from '@/components/ui/focus';
import { useTooltip } from '@/components/ui/Tooltip';

const STORAGE_KEY = 'probara-theme';

type ThemeMode = 'light' | 'dark' | 'system';

const NEXT: Record<ThemeMode, ThemeMode> = { light: 'dark', dark: 'system', system: 'light' };
const LABEL: Record<ThemeMode, string> = { light: 'Light', dark: 'Dark', system: 'System' };
const ICON = { light: Sun, dark: Moon, system: Monitor } as const;

function readMode(): ThemeMode {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    return stored === 'light' || stored === 'dark' ? stored : 'system';
  } catch {
    return 'system';
  }
}

function systemTheme(): 'light' | 'dark' {
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

/**
 * Theme switch cycling light → dark → system. With no stored choice the app
 * follows the OS: the boot script in app/layout.tsx resolves the theme before
 * first paint and keeps following prefers-color-scheme. This control stores
 * an explicit choice in `probara-theme` (or clears it for "system") and sets
 * `data-theme` on <html>, which is all the theme tokens key on.
 */
export default function ThemeToggle({
  className = '',
  tooltip = false,
}: {
  className?: string;
  /**
   * The collapsed sidebar rail: label in a portaled Tooltip instead of the
   * native title, and the inset focus ring every 40×40 rail control uses.
   */
  tooltip?: boolean;
}) {
  const [mode, setMode] = useState<ThemeMode>('system');

  useEffect(() => {
    setMode(readMode());
    const onStorage = (event: StorageEvent) => {
      if (event.key === null || event.key === STORAGE_KEY) setMode(readMode());
    };
    window.addEventListener('storage', onStorage);
    return () => window.removeEventListener('storage', onStorage);
  }, []);

  const cycle = () => {
    const next = NEXT[mode];
    try {
      if (next === 'system') localStorage.removeItem(STORAGE_KEY);
      else localStorage.setItem(STORAGE_KEY, next);
    } catch {
      /* private browsing: the choice lasts for this page only */
    }
    document.documentElement.dataset.theme = next === 'system' ? systemTheme() : next;
    setMode(next);
  };

  const Icon = ICON[mode];
  const label = `Theme: ${LABEL[mode]}. Switch to ${LABEL[NEXT[mode]].toLowerCase()}`;
  const tip = useTooltip<HTMLButtonElement>({ label, disabled: !tooltip });
  return (
    <>
      <button
        ref={tip.ref}
        {...tip.triggerProps}
        type="button"
        onClick={cycle}
        title={tooltip ? undefined : label}
        aria-label={label}
        className={`flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg text-slate-400 transition-colors hover:bg-well-hover hover:text-slate-100 active:bg-well ${tooltip ? FOCUS_RING_INSET : FOCUS_RING} ${className}`}
      >
        <Icon className="h-4 w-4" strokeWidth={1.75} aria-hidden="true" />
      </button>
      {tip.tooltip}
    </>
  );
}
