'use client';

import { useMemo, useState } from 'react';
import { ChevronDown, ChevronRight, Search, Tag as TagIcon } from 'lucide-react';
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
    <div className="rounded-lg border border-white/[0.06] bg-slate-900/40">
      <div className="flex items-center gap-2 px-2.5 py-2">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          className="flex flex-shrink-0 items-center gap-1.5 text-[11px] font-medium text-slate-400 transition-colors hover:text-slate-200"
        >
          {open ? (
            <ChevronDown className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
          )}
          <TagIcon className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
          Tags
          <span className="text-slate-600">
            {selected.size > 0 ? `· ${selected.size} active` : `· ${tags.length}`}
          </span>
        </button>

        {/* Active tags stay visible with the panel closed; clicking one drops it. */}
        {selectedTags.length > 0 && (
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
            {selectedTags.slice(0, 6).map(({ tag }) => (
              <TagPill key={tag} tag={tag} size="xs" selected onClick={() => onToggle(tag)} />
            ))}
            {selectedTags.length > 6 && (
              <span className="text-[10px] text-slate-500">+{selectedTags.length - 6}</span>
            )}
          </div>
        )}

        {selected.size > 0 && (
          <button
            type="button"
            onClick={onClear}
            className="ml-auto flex-shrink-0 text-[10px] text-slate-400 transition-colors hover:text-white"
          >
            Clear tags
          </button>
        )}
      </div>

      {open && (
        <div className="border-t border-white/[0.06] p-2">
          <div className="mb-1.5 flex items-center gap-2 rounded-md border border-white/[0.06] bg-slate-950/40 px-2 py-1.5">
            <Search className="h-3.5 w-3.5 flex-shrink-0 text-slate-500" strokeWidth={1.75} aria-hidden="true" />
            <input
              type="search"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Find a tag…"
              aria-label="Find a tag"
              className="w-full bg-transparent text-xs text-slate-200 placeholder:text-slate-600 focus:outline-none"
            />
          </div>

          <div
            className="dashboard-scroll max-h-40 overflow-y-auto"
            role="listbox"
            aria-multiselectable="true"
            aria-label="Filter by tags"
          >
            {visible.length === 0 ? (
              <p className="px-2 py-4 text-center text-[11px] text-slate-500">No tags match.</p>
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
                    className={`flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors ${
                      isSelected ? 'bg-cyan-500/[0.08]' : 'hover:bg-white/[0.04]'
                    }`}
                  >
                    <span
                      aria-hidden="true"
                      className={`flex h-3.5 w-3.5 flex-shrink-0 items-center justify-center rounded border transition-colors ${
                        isSelected ? 'border-cyan-500 bg-cyan-500' : 'border-white/20 bg-slate-950/50'
                      }`}
                    >
                      {isSelected && (
                        <svg
                          className="h-2 w-2 text-white"
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
                    <span className="flex-shrink-0 text-[10px] tabular-nums text-slate-500">
                      {count}
                    </span>
                  </button>
                );
              })
            )}
            {hidden > 0 && (
              <p className="px-2 py-1.5 text-center text-[10px] text-slate-600">
                {hidden} more — search to narrow the list
              </p>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
