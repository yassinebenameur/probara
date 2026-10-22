// The sidebar toggle shortcut, kept free of React and the live DOM so its
// rules are unit-tested (sidebar-shortcut.test.ts); Sidebar.tsx wires it up.
//
// Ctrl+B (⌘B on macOS) toggles the rail, the editor convention for a side
// bar. A modifier chord rather than a bare character key, so it cannot fire
// from speech input or a stray keypress (WCAG 2.1.4). Firefox opens its
// bookmarks sidebar on the same chord, so the caller preventDefaults any
// match, including a held key's auto-repeat ('repeat'), which toggles nothing.
//
// The chord is left to the browser (or the field: ⌘B is bold in rich text)
// when it is meant for something else: typing in a field, or an open popup —
// a modal, or a menu whose fixed position and focus would be orphaned when
// the sidebar resizes or the rail's workspace menu unmounts.

import { isApplePlatform, shortcutAria, shortcutLabel } from './shortcuts';

/** Focus targets that keep the chord for themselves. */
export const SHORTCUT_IGNORED_TARGETS =
  'input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="menu"], [role="listbox"], [role="dialog"], [role="combobox"]';

/** Open popups that suspend the shortcut wherever focus is. */
export const SHORTCUT_BLOCKING_POPUPS = '[aria-modal="true"], [role="menu"]';

export type SidebarShortcutMatch = 'toggle' | 'repeat' | null;

/** The KeyboardEvent fields the match reads. */
export interface ShortcutKeyEvent {
  key: string;
  code: string;
  metaKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
  repeat: boolean;
  defaultPrevented: boolean;
  isComposing: boolean;
  target: unknown;
}

// Platform detection and the label formats live in lib/shortcuts.ts, shared
// with every other modifier chord; re-exported so callers import one module.
export { isApplePlatform };

/** The key cap shown in the toggle's tooltip. */
export function sidebarShortcutLabel(apple: boolean) {
  return shortcutLabel('B', apple);
}

/** The same chord in aria-keyshortcuts syntax. */
export function sidebarShortcutAria(apple: boolean) {
  return shortcutAria('B', apple);
}

function keepsChord(target: unknown) {
  if (!target || typeof target !== 'object') return false;
  const el = target as { isContentEditable?: boolean; closest?: (selector: string) => unknown };
  return !!el.isContentEditable || (typeof el.closest === 'function' && !!el.closest(SHORTCUT_IGNORED_TARGETS));
}

/**
 * Whether a keydown is the sidebar chord: 'toggle', 'repeat' (a held key's
 * auto-repeat — swallow it, toggle nothing) or null (not ours). `popupOpen`
 * is only consulted once the chord itself matches.
 */
export function matchSidebarShortcut(
  event: ShortcutKeyEvent,
  apple: boolean,
  popupOpen: () => boolean
): SidebarShortcutMatch {
  // event.key is the layout's letter; a non-Latin layout (Cyrillic, Greek)
  // reports its own letter there, so fall back to the physical B key.
  const key = event.key.toLowerCase();
  const isB = key === 'b' || (!/^[a-z]$/.test(key) && event.code === 'KeyB');
  const chord = apple ? event.metaKey && !event.ctrlKey : event.ctrlKey && !event.metaKey;
  // Alt excludes AltGr too (reported as Ctrl+Alt on Windows).
  if (!isB || !chord || event.altKey || event.shiftKey || event.defaultPrevented || event.isComposing) {
    return null;
  }
  if (keepsChord(event.target) || popupOpen()) return null;
  return event.repeat ? 'repeat' : 'toggle';
}
