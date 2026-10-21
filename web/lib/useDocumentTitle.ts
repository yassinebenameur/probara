'use client';

import { useEffect } from 'react';

/** The suffix every route title carries (matches the root metadata template). */
export const TITLE_SUFFIX = ' · Probara';

/** "Orders API" → "Orders API · Probara"; empty → "Probara". */
export function documentTitle(title?: string | null): string {
  const trimmed = title?.trim();
  return trimmed ? `${trimmed}${TITLE_SUFFIX}` : 'Probara';
}

/**
 * Per-entity titles for client detail pages, which cannot export metadata
 * ("Orders API · Probara"). Next's route announcer reads document.title, so
 * a distinct title is also what makes client navigation audible. Pass
 * undefined while the entity loads to leave the route's title in place;
 * the previous title comes back on unmount.
 */
export function useDocumentTitle(title?: string | null) {
  useEffect(() => {
    if (!title) return;
    const previous = document.title;
    document.title = documentTitle(title);
    return () => {
      document.title = previous;
    };
  }, [title]);
}

export default useDocumentTitle;
