'use client';

import { useState, useEffect, useCallback, useRef } from 'react';

/**
 * Ids of rows on screen (inside their scroll card and the viewport), so the
 * per-row trace fetch covers only what can be seen: Needs attention draws up
 * to DASHBOARD_LIST_LIMIT rows but fetches results for the dozen visible.
 * Without IntersectionObserver every row counts as visible.
 */
export function useVisibleRows() {
  const [visible, setVisible] = useState<ReadonlySet<string>>(() => new Set());
  const supported = typeof window !== 'undefined' && 'IntersectionObserver' in window;
  const ids = useRef(new Map<Element, string>());
  const observer = useRef<IntersectionObserver | null>(null);

  const getObserver = useCallback(() => {
    if (!supported) return null;
    if (!observer.current) {
      observer.current = new IntersectionObserver(
        (entries) => {
          setVisible((prev) => {
            let next: Set<string> | null = null;
            for (const entry of entries) {
              const id = ids.current.get(entry.target);
              if (!id || entry.isIntersecting === prev.has(id)) continue;
              next ??= new Set(prev);
              if (entry.isIntersecting) next.add(id);
              else next.delete(id);
            }
            return next ?? prev;
          });
        },
        // Fetch a little ahead of the scroll.
        { rootMargin: '160px 0px' },
      );
    }
    return observer.current;
  }, [supported]);

  useEffect(() => () => observer.current?.disconnect(), []);

  const observe = useCallback(
    (el: Element | null, id: string) => {
      const io = getObserver();
      if (!el || !io) return undefined;
      ids.current.set(el, id);
      io.observe(el);
      return () => {
        io.unobserve(el);
        ids.current.delete(el);
        setVisible((prev) => {
          if (!prev.has(id)) return prev;
          const next = new Set(prev);
          next.delete(id);
          return next;
        });
      };
    },
    [getObserver],
  );

  return { visible, observe, supported };
}
