'use client';

import { useSyncExternalStore } from 'react';

// Module-level store for hover-dim and search-highlight so pointer movement
// never touches the React state that owns the node/edge arrays. Nodes and
// edges subscribe individually; only the ones whose visual actually flips
// re-render, and a re-render is just a class swap on a mounted card.

export type NodeVisual = { dimmed: boolean; highlighted: boolean };

const DEFAULT_VISUAL: NodeVisual = { dimmed: false, highlighted: false };

let hood: Set<string> | null = null;
let matches: Set<string> | null = null;
const listeners = new Set<() => void>();
const visualCache = new Map<string, NodeVisual>();

function notify() {
  for (const l of listeners) l();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** The hovered node's 1-hop neighborhood (itself included), or null on leave. */
export function setHover(nextHood: Set<string> | null) {
  if (hood === nextHood) return;
  hood = nextHood;
  notify();
}

/** Ids matching the current search, or null when the query is empty. */
export function setMatches(next: Set<string> | null) {
  matches = next;
  notify();
}

function nodeVisual(id: string): NodeVisual {
  const dimmed = (hood ? !hood.has(id) : false) || (matches ? !matches.has(id) : false);
  const highlighted = matches ? matches.has(id) : false;
  const prev = visualCache.get(id);
  if (prev && prev.dimmed === dimmed && prev.highlighted === highlighted) return prev;
  const next = { dimmed, highlighted };
  visualCache.set(id, next);
  return next;
}

export function useNodeVisual(id: string): NodeVisual {
  return useSyncExternalStore(subscribe, () => nodeVisual(id), () => DEFAULT_VISUAL);
}

/** True when the edge should fade out under the current hover/search state. */
export function useEdgeVisual(source: string, target: string): boolean {
  return useSyncExternalStore(
    subscribe,
    () => {
      const inHood = hood ? hood.has(source) && hood.has(target) : true;
      const inMatch = matches ? matches.has(source) || matches.has(target) : true;
      return !inHood || !inMatch;
    },
    () => false
  );
}
