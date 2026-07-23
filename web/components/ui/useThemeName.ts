'use client';

import { useEffect, useState } from 'react';

/**
 * Reactive name of the active theme. Tracks the `data-theme` attribute that
 * the boot script (app/layout.tsx) and ThemeToggle maintain on <html>, so
 * canvas-style components (charts, graph views) can re-render on switch.
 */
export function useThemeName(): 'dark' | 'light' {
  const [theme, setTheme] = useState<'dark' | 'light'>('light');

  useEffect(() => {
    const read = () =>
      setTheme(document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light');
    read();
    const observer = new MutationObserver(read);
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-theme'],
    });
    return () => observer.disconnect();
  }, []);

  return theme;
}
