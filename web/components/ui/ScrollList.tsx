'use client';

import { CSSProperties, ReactNode, useCallback, useEffect, useRef, useState } from 'react';
import { FOCUS_RING_INSET } from './focus';

interface ScrollListProps {
  children: ReactNode;
  /** Height cap (any CSS length); the list scrolls inside it. Or size it with className (h-*, max-h-*, flex-1). */
  maxHeight?: string;
  /** Names the scroll region for assistive tech when it overflows. */
  label?: string;
  className?: string;
  /** Fade length at a scrollable edge (px). */
  fade?: number;
}

type Edges = { overflow: boolean; atTop: boolean; atBottom: boolean };

/**
 * A vertical scroll container for lists inside cards ("What changed", long
 * pickers). It fades an edge only where more content hides behind it, and
 * shows no fade at all when everything fits — a fade over a short list reads
 * as a rendering bug. Overflow is re-measured with a ResizeObserver on the
 * container and its content, so rows arriving from a refresh update it.
 * When it overflows it becomes a focusable, named region so keyboard users
 * can scroll it.
 */
export default function ScrollList({ children, maxHeight, label, className = '', fade = 24 }: ScrollListProps) {
  const ref = useRef<HTMLDivElement>(null);
  const [edges, setEdges] = useState<Edges>({ overflow: false, atTop: true, atBottom: true });

  const measure = useCallback(() => {
    const el = ref.current;
    if (!el) return;
    const overflow = el.scrollHeight - el.clientHeight > 1;
    const next: Edges = {
      overflow,
      atTop: el.scrollTop <= 1,
      atBottom: el.scrollTop + el.clientHeight >= el.scrollHeight - 1,
    };
    setEdges((prev) =>
      prev.overflow === next.overflow && prev.atTop === next.atTop && prev.atBottom === next.atBottom ? prev : next
    );
  }, []);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    measure();
    if (typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    Array.from(el.children).forEach((child) => observer.observe(child));
    // Rows added or removed change the content height without resizing the
    // container's existing children; watch the child list too.
    const mutations = new MutationObserver(() => {
      Array.from(el.children).forEach((child) => observer.observe(child));
      measure();
    });
    mutations.observe(el, { childList: true });
    return () => {
      observer.disconnect();
      mutations.disconnect();
    };
  }, [measure]);

  let mask: string | undefined;
  if (edges.overflow && !(edges.atTop && edges.atBottom)) {
    const top = edges.atTop ? 'black 0' : `transparent 0, black ${fade}px`;
    const bottom = edges.atBottom ? 'black 100%' : `black calc(100% - ${fade}px), transparent 100%`;
    mask = `linear-gradient(to bottom, ${top}, ${bottom})`;
  }

  const style: CSSProperties = {
    maxHeight,
    maskImage: mask,
    WebkitMaskImage: mask,
  };

  return (
    <div
      ref={ref}
      onScroll={measure}
      style={style}
      className={`overflow-y-auto overscroll-contain ${edges.overflow ? FOCUS_RING_INSET : ''} ${className}`.trim()}
      tabIndex={edges.overflow ? 0 : undefined}
      role={edges.overflow && label ? 'region' : undefined}
      aria-label={edges.overflow ? label : undefined}
    >
      {children}
    </div>
  );
}
