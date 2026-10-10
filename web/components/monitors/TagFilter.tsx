'use client';

import { useMemo, useState } from 'react';
import { ChevronDown, ChevronRight, Search, Tag as TagIcon } from 'lucide-react';
import Button from '@/components/ui/Button';
import TagPill from './TagPill';

export interface TagOption {
  tag: string;
  count: number;
}

interface TagFilterProps {
  tags: TagOption[];
  selected: Set<string>;
  onToggle: (tag: string) => void;
  onClear: () => void;
  // Tags a workspace accumulates can run into the hundreds; the list keeps its
  // own search so the panel never grows past a few rows of scroll.
  visibleLimit?: number;
}

// Collapsed chip row + expandable searchable tag list. Inline rather than a
// floating popover so it can't be clipped by a modal's scroll container.
export default function TagFilter({
  tags,
  selected,
  onToggle,
  onClear,
  visibleLimit = 80,
}: TagFilterProps) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');

  // Most-used tags first — that ordering is what makes a long list usable
  // before anyone types anything.
  const ordered = useMemo(
    () => [...tags].sort((a, b) => b.count - a.count || a.tag.localeCompare(b.tag)),
    [tags]
  );

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return ordered;
    return ordered.filter((t) => t.tag.toLowerCase().includes(q));
  }, [ordered, search]);

  const visible = filtered.slice(0, visibleLimit);
  const hidden = filtered.length - visible.length;
  const selectedTags = ordered.filter((t) => selected.has(t.tag));

  return (
    <div className="glass-well">
      <div className="flex items-center gap-2 px-3 py-2">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          className="flex flex-shrink-0 items-center gap-1.5 rounded text-xs font-medium text-slate-400 transition-colors hover:text-white focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/50"
        >
          {open ? (
            <ChevronDown className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
          )}
          <TagIcon className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
          Tags
          <span className="ml-1 font-normal tabular-nums text-slate-500">
            {selected.size > 0 ? `${selected.size} active` : tags.length}
          </span>
        </button>

        {/* Active tags stay visible with the panel closed; clicking one drops it. */}
        {selectedTags.length > 0 && (
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
            {selectedTags.slice(0, 6).map(({ tag }) => (
              <TagPill key={tag} tag={tag} size="xs" selected onClick={() => onToggle(tag)} />
            ))}
            {selectedTags.length > 6 && (
              <span className="text-2xs text-slate-500">+{selectedTags.length - 6}</span>
            )}
          </div>
        )}

        {selected.size > 0 && (
          <Button type="button" variant="subtle" size="xs" onClick={onClear} className="ml-auto flex-shrink-0">
            Clear tags
          </Button>
        )}
      </div>

      {open && (
        <div className="px-2 pb-2">
          <div className="relative mb-1.5">
            <Search
              className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500"
              strokeWidth={1.75}
              aria-hidden="true"
            />
            <input
              type="search"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Find a tag…"
              aria-label="Find a tag"
              className="input input-sm !pl-8"
            />
          </div>

          <div
            className="dashboard-scroll max-h-40 overflow-y-auto"
            role="listbox"
            aria-multiselectable="true"
            aria-label="Filter by tags"
          >
            {visible.length === 0 ? (
              <p className="px-2 py-4 text-center text-xs text-slate-500">No tags match.</p>
            ) : (
              visible.map(({ tag, count }) => {
                const isSelected = selected.has(tag);
                return (
                  <button
                    key={tag}
                    type="button"
                    role="option"
                    aria-selected={isSelected}
                    onClick={() => onToggle(tag)}
                    className={`flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/50 ${
                      isSelected ? 'bg-well-hover' : 'hover:bg-well-hover'
                    }`}
                  >
                    <span
                      aria-hidden="true"
                      className={`flex h-3.5 w-3.5 flex-shrink-0 items-center justify-center rounded-[5px] border transition-colors ${
                        isSelected ? 'border-cyan-500 bg-cyan-500' : 'border-edge-control bg-control'
                      }`}
                    >
                      {isSelected && (
                        <svg
                          className="h-2 w-2 text-onaccent"
                          fill="none"
                          viewBox="0 0 24 24"
                          stroke="currentColor"
                          strokeWidth={3}
                        >
                          <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                        </svg>
                      )}
                    </span>
                    <span className="min-w-0 flex-1">
                      <TagPill tag={tag} size="xs" />
                    </span>
                    <span className="flex-shrink-0 text-2xs tabular-nums text-slate-500">
                      {count}
                    </span>
                  </button>
                );
              })
            )}
            {hidden > 0 && (
              <p className="px-2 py-1.5 text-center text-2xs text-slate-500">
                {hidden} more; search to narrow the list
              </p>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
