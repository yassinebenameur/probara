'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import Link from 'next/link';
import { CornerDownRight, Search, X } from 'lucide-react';
import type { DocSearchEntry } from '@/lib/docs/search';

export function DocsSearch({ entries }: { entries: DocSearchEntry[] }) {
  const [query, setQuery] = useState('');
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const normalized = query.trim().toLowerCase();

  const results = useMemo(() => {
    if (!normalized) return [];
    const tokens = normalized.split(/\s+/);
    return entries
      .filter((entry) => tokens.every((token) => entry.haystack.includes(token)))
      .map((entry) => {
        const title = (entry.section ?? entry.page).toLowerCase();
        const score =
          (title.startsWith(normalized) ? 0 : title.includes(normalized) ? 1 : 2) +
          (entry.section ? 0.5 : 0);
        return { entry, score };
      })
      .sort((a, b) => a.score - b.score)
      .slice(0, 8)
      .map((ranked) => ranked.entry);
  }, [entries, normalized]);

  useEffect(() => {
    const onPointerDown = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    window.addEventListener('mousedown', onPointerDown);
    return () => window.removeEventListener('mousedown', onPointerDown);
  }, []);

  return (
    <div className="docs-search" ref={containerRef}>
      <Search size={14} aria-hidden="true" />
      <input
        type="search"
        value={query}
        onChange={(event) => {
          setQuery(event.target.value);
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        placeholder="Search documentation"
        aria-label="Search documentation"
      />
      {query && (
        <button
          type="button"
          onClick={() => {
            setQuery('');
            setOpen(false);
          }}
          aria-label="Clear search"
        >
          <X size={13} />
        </button>
      )}
      {open && normalized && (
        <div className="docs-search__results">
          {results.length > 0 ? (
            results.map((result) => (
              <Link
                href={result.href}
                key={result.href}
                onClick={() => {
                  setOpen(false);
                  setQuery('');
                }}
              >
                {result.section ? (
                  <>
                    <strong>
                      <CornerDownRight size={11} aria-hidden="true" />
                      {result.section}
                    </strong>
                    <span>{result.page}</span>
                  </>
                ) : (
                  <>
                    <strong>{result.page}</strong>
                    <span>{result.group}</span>
                  </>
                )}
              </Link>
            ))
          ) : (
            <p>No matches. Try a feature, variable, or service name.</p>
          )}
        </div>
      )}
    </div>
  );
}
