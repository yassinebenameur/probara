'use client';

import { useEffect, useMemo, useRef } from 'react';
import { Tag as TagIcon, X } from 'lucide-react';
import FilterPopover, { type FilterOption } from '@/components/ui/FilterPopover';
import { FOCUS_RING } from '@/components/ui/focus';
import TagPill, { getTagColor } from './TagPill';

export interface TagOption {
  tag: string;
  /** Monitors wearing the tag; omit when the caller has no counts. */
  count?: number;
}

interface TagFilterProps {
  tags: TagOption[];
  selected: ReadonlySet<string>;
  onChange: (next: Set<string>) => void;
  /**
   * Picking tags as a value (an SLA's scope) rather than filtering a list:
   * names the popup "Choose tags", and the chip counts while the chosen tags
   * read as pills beside it, since a form field should show its whole value.
   */
  asValue?: boolean;
  /**
   * The chip's accessible name when "Tags" alone is ambiguous: the SLA form
   * has a second Tags chip (MonitorMultiSelect's filter) in the same dialog.
   */
  accessibleLabel?: string;
  className?: string;
}

/**
 * Tag chip for the monitor pickers inside dialogs (MonitorMultiSelect, the
 * SLA form) and the Settings dashboard groups: FilterPopover over the tags with their monitor counts, most used
 * first, so a long tag list is searchable and never pushes the dialog's
 * content down. The popup is portaled above the dialog and out of its focus
 * trap (see FilterPopover).
 */
export default function TagFilter({
  tags,
  selected,
  onChange,
  asValue = false,
  accessibleLabel,
  className,
}: TagFilterProps) {
  // Most-used tags first: that ordering is what makes a long list usable
  // before anyone types anything.
  const options = useMemo<FilterOption[]>(
    () =>
      [...tags]
        .sort((a, b) => (b.count ?? 0) - (a.count ?? 0) || a.tag.localeCompare(b.tag))
        .map(({ tag, count }) => ({ value: tag, label: tag, count, dotClassName: getTagColor(tag).dot })),
    [tags]
  );

  // A removed pill unmounts with focus on it: hand focus to the pill that
  // took its place, else the one before, else the chip.
  const wrapRef = useRef<HTMLDivElement>(null);
  const refocusPill = useRef<number | null>(null);
  useEffect(() => {
    const index = refocusPill.current;
    if (index === null) return;
    refocusPill.current = null;
    const pills = wrapRef.current?.querySelectorAll<HTMLElement>('[data-tag-remove]');
    const target =
      pills?.[Math.min(index, pills.length - 1)] ??
      wrapRef.current?.querySelector<HTMLElement>('[data-filter-popover-trigger]');
    target?.focus();
  }, [selected]);

  const popover = (
    <FilterPopover
      label="Tags"
      noun="tag"
      icon={<TagIcon strokeWidth={1.75} />}
      options={options}
      selected={selected}
      onChange={onChange}
      popupLabel={asValue ? 'Choose tags' : undefined}
      clearLabel={asValue ? `Clear ${accessibleLabel ?? 'tags'}` : undefined}
      accessibleLabel={accessibleLabel}
      summarizeAsCount={asValue}
      className={className}
    />
  );
  if (!asValue) return popover;

  // The chip counts; the value itself reads beside it, most used first, and
  // each pill removes its tag in one click.
  const chosen = [
    ...options.filter((o) => selected.has(o.value)).map((o) => o.value),
    ...Array.from(selected).filter((tag) => !options.some((o) => o.value === tag)),
  ];
  return (
    <div ref={wrapRef} className="flex flex-wrap items-center gap-1.5">
      {popover}
      {chosen.map((tag, index) => (
        <button
          key={tag}
          type="button"
          data-tag-remove=""
          aria-label={`Remove ${tag}`}
          title={`Remove ${tag}`}
          onClick={() => {
            refocusPill.current = index;
            const next = new Set(selected);
            next.delete(tag);
            onChange(next);
          }}
          className={`group inline-flex items-center gap-0.5 rounded-full pr-1 transition active:scale-[.98] ${FOCUS_RING}`}
        >
          <TagPill tag={tag} size="xs" />
          <X
            className="h-3 w-3 shrink-0 text-slate-500 transition-colors duration-fast group-hover:text-white"
            strokeWidth={2}
            aria-hidden="true"
          />
        </button>
      ))}
    </div>
  );
}
