'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import Link from 'next/link';
import { Search, X } from 'lucide-react';
import { FLAT_DOC_NAVIGATION } from '@/lib/docs/navigation';

export function DocsSearch() {
  const [query, setQuery] = useState('');
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const normalized = query.trim().toLowerCase();

  const results = useMemo(() => {
    if (!normalized) return [];
    return FLAT_DOC_NAVIGATION.filter((item) =>
      `${item.title} ${item.description} ${item.group}`.toLowerCase().includes(normalized),
    ).slice(0, 7);
  }, [normalized]);

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
                href={`/docs/${result.slug}/`}
                key={result.slug}
                onClick={() => {
                  setOpen(false);
                  setQuery('');
                }}
              >
                <strong>{result.title}</strong>
                <span>{result.group}</span>
              </Link>
            ))
          ) : (
            <p>No matching guide. Try a service or feature name.</p>
          )}
        </div>
      )}
    </div>
  );
}
