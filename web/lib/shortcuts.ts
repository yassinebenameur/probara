// Keyboard-shortcut labels and guards shared by pages that bind a modifier
// chord (Cmd/Ctrl+S to save, Cmd/Ctrl+Enter to post). Handlers accept both
// metaKey and ctrlKey; only the label shown follows the platform.

/** The navigator fields platform detection reads (injectable for tests). */
export interface PlatformSource {
  userAgentData?: { platform?: string };
  platform?: string;
  userAgent?: string;
}

/**
 * macOS / iOS, where the chord modifier is ⌘ rather than Ctrl. The one
 * platform check for every chord label (sidebar, save, post).
 */
export function isApplePlatform(
  nav: PlatformSource | null = typeof navigator === 'undefined' ? null : navigator
): boolean {
  if (!nav) return false;
  const platform = nav.userAgentData?.platform || nav.platform || nav.userAgent || '';
  return /mac|iphone|ipad|ipod/i.test(platform);
}

/**
 * A chord's key cap for a tooltip or hint: `⌘S` / `Ctrl+S`, `⌘↵` / `Ctrl+↵`.
 * `apple` is read once per component (`useState(isApplePlatform)`): the shell
 * renders client-only, so there is no server markup to mismatch.
 */
export function shortcutLabel(key: string, apple: boolean): string {
  return apple ? `⌘${key}` : `Ctrl+${key}`;
}

/** The same chord in aria-keyshortcuts syntax: `Meta+S` / `Control+S`. */
export function shortcutAria(key: string, apple: boolean): string {
  return apple ? `Meta+${key}` : `Control+${key}`;
}

/**
 * True while a modal (Dialog, ConfirmDialog — anything with aria-modal) is
 * open: page-level chords stand down so they never act behind it.
 */
export function modalOpen(doc: Pick<Document, 'querySelector'> | undefined =
  typeof document === 'undefined' ? undefined : document): boolean {
  return !!doc?.querySelector('[aria-modal="true"]');
}
