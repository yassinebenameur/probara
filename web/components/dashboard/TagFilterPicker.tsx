'use client';

import { useMemo } from 'react';
import FilterPopover, { type FilterOption } from '@/components/ui/FilterPopover';
import { Tag } from 'lucide-react';
import { getTagColor } from '@/components/monitors/TagPill';

/**
 * The dashboard's tag scope: the shared FilterPopover (as on /monitors), fed
 * the tags the dashboard reports. It has no per-tag counts, so rows carry
 * none; the page keeps its sorted array, so each change maps back onto
 * toggle/clear. Like /monitors (matchesAnyTag in lib/monitor-list.ts), the
 * dashboard API scopes to monitors carrying any selected tag, so a count and
 * the ?tag= list it links to agree.
 * Sized md to line up with the RangePicker and button beside it.
 */
export function TagFilterPicker({
  availableTags,
  selectedTags,
  onToggleTag,
  onClear,
}: {
  availableTags: string[];
  selectedTags: string[];
  onToggleTag: (tag: string) => void;
  onClear: () => void;
}) {
  const options = useMemo<FilterOption[]>(
    () => availableTags.map((tag) => ({ value: tag, label: tag, dotClassName: getTagColor(tag).dot })),
    [availableTags]
  );
  const selected = useMemo(() => new Set(selectedTags), [selectedTags]);

  if (availableTags.length === 0) return null;

  return (
    <FilterPopover
      label="Tags"
      noun="tag"
      icon={<Tag strokeWidth={1.75} />}
      options={options}
      selected={selected}
      size="md"
      popupLabel="Scope to monitors with any of these tags"
      hint="Monitors carrying any selected tag."
      onChange={(next) => {
        if (next.size === 0) {
          onClear();
          return;
        }
        for (const tag of selectedTags) if (!next.has(tag)) onToggleTag(tag);
        for (const tag of next) if (!selected.has(tag)) onToggleTag(tag);
      }}
    />
  );
}
