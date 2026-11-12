'use client';

import { useCallback, useEffect, useRef, useState } from 'react';

/** How long a "Copied" confirmation stays up. */
export const COPIED_RESET_MS = 2000;

/**
 * Writes text to the clipboard. Falls back to a hidden textarea where the
 * async clipboard API is unavailable (non-secure origins). False when both fail.
 */
export async function writeClipboard(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    /* falls back below */
  }
  try {
    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.setAttribute('readonly', '');
    textarea.style.position = 'fixed';
    textarea.style.opacity = '0';
    document.body.appendChild(textarea);
    textarea.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(textarea);
    return ok;
  } catch {
    return false;
  }
}

type ClipboardState = { key: string; status: 'copied' | 'failed' } | null;

/**
 * Copy-to-clipboard with a confirmation that resets itself. One hook can serve
 * several buttons: pass each a `key` and compare it with `copiedKey`.
 * "Copied" is only reported once the write has succeeded.
 */
export function useClipboard(resetMs: number = COPIED_RESET_MS) {
  const [state, setState] = useState<ClipboardState>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
    },
    []
  );

  const copy = useCallback(
    async (text: string, key: string = 'default'): Promise<boolean> => {
      if (!text) return false;
      const ok = await writeClipboard(text);
      setState({ key, status: ok ? 'copied' : 'failed' });
      if (timer.current) clearTimeout(timer.current);
      timer.current = setTimeout(() => setState(null), resetMs);
      return ok;
    },
    [resetMs]
  );

  return {
    copy,
    /** Any button's copy just succeeded. */
    copied: state?.status === 'copied',
    /** The last copy failed (clipboard unavailable or denied). */
    failed: state?.status === 'failed',
    /** The `key` whose copy just succeeded, or null. */
    copiedKey: state?.status === 'copied' ? state.key : null,
  };
}
