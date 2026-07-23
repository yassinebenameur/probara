'use client';

import { useEffect, useState } from 'react';
import { Moon, Sun } from 'lucide-react';

const STORAGE_KEY = 'probara-theme';

/**
 * Dark/light switch. Light is the default; the boot script in app/layout.tsx
 * applies the stored choice before first paint, and this control just
 * flips `data-theme` on <html> and persists the preference.
 */
export default function ThemeToggle({ className = '' }: { className?: string }) {
  const [theme, setTheme] = useState<'dark' | 'light'>('light');

  useEffect(() => {
    setTheme(document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light');
  }, []);

  const toggle = () => {
    const next = theme === 'dark' ? 'light' : 'dark';
    document.documentElement.dataset.theme = next;
    try {
      localStorage.setItem(STORAGE_KEY, next);
    } catch {
      /* private browsing */
    }
    setTheme(next);
  };

  const Icon = theme === 'dark' ? Sun : Moon;
  return (
    <button
      type="button"
      onClick={toggle}
      title={theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
      aria-label={theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
      className={`flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-md border border-white/[0.08] text-slate-400 transition-colors hover:border-white/[0.14] hover:text-slate-200 ${className}`}
    >
      <Icon className="h-4 w-4" strokeWidth={1.75} />
    </button>
  );
}
