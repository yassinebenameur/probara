'use client';

import { useCallback, useSyncExternalStore } from 'react';

/**
 * Desktop sidebar state: expanded (default) or collapsed to the icon rail.
 *
 * The source of truth is `html[data-sidebar="collapsed"]`. The boot script in
 * app/layout.tsx sets it from localStorage before first paint, and every
 * width that depends on it is CSS keyed on that attribute (--sidebar-width /
 * --sidebar-offset in app/globals.css), so the first paint is already right.
 * React reads the attribute through useSyncExternalStore with an "expanded"
 * server snapshot, which keeps hydration warning-free. Stored per viewer in
 * `probara-sidebar` ('collapsed' | 'expanded'); a convenience only, so every
 * storage access is wrapped and the attribute alone carries the session.
 */
export const SIDEBAR_STORAGE_KEY = 'probara-sidebar';

const listeners = new Set<() => void>();

function readAttribute(): boolean {
  return document.documentElement.dataset.sidebar === 'collapsed';
}

function applyAttribute(collapsed: boolean) {
  const root = document.documentElement;
  if (collapsed) root.dataset.sidebar = 'collapsed';
  else delete root.dataset.sidebar;
  listeners.forEach((listener) => listener());
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  // Another tab's choice lands here too.
  const onStorage = (event: StorageEvent) => {
    if (event.key !== null && event.key !== SIDEBAR_STORAGE_KEY) return;
    let stored: string | null = null;
    try {
      stored = localStorage.getItem(SIDEBAR_STORAGE_KEY);
    } catch {
      return;
    }
    if ((stored === 'collapsed') !== readAttribute()) applyAttribute(stored === 'collapsed');
  };
  window.addEventListener('storage', onStorage);
  return () => {
    listeners.delete(listener);
    window.removeEventListener('storage', onStorage);
  };
}

export function useSidebarCollapsed(): [boolean, (collapsed: boolean) => void] {
  const collapsed = useSyncExternalStore(subscribe, readAttribute, () => false);
  const setCollapsed = useCallback((next: boolean) => {
    try {
      localStorage.setItem(SIDEBAR_STORAGE_KEY, next ? 'collapsed' : 'expanded');
    } catch {
      /* private browsing: the choice lasts for this page only */
    }
    applyAttribute(next);
  }, []);
  return [collapsed, setCollapsed];
}
