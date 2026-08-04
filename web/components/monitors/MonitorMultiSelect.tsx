'use client';

import { useCallback, useMemo, useState } from 'react';
import { ChevronDown, ChevronRight, Layers, Search } from 'lucide-react';
import { Monitor } from '@/lib/types';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import SelectCheckbox from '@/components/ui/SelectCheckbox';
import TagPill from '@/components/monitors/TagPill';
import TagFilter from '@/components/monitors/TagFilter';

interface MonitorMultiSelectProps {
  monitors: Monitor[];
  selectedIds: string[];
  onChange: (ids: string[]) => void;
  excludeIds?: string[];
  emptyMessage?: string;
  // groupId -> direct member monitor ids. When provided, the list renders by
  // group instead of flat, and selecting a group selects the group monitor
  // itself — consumers whose backend expands a group to its members (e.g.
  // maintenance windows) get member coverage without listing every member.
  groupMembers?: Record<string, string[]>;
  // Tailwind max-height for the scroll box; raise it where the list is the
  // main content of the surface rather than one field among many.
  maxHeightClass?: string;
}

// Searchable checkbox-list monitor picker, extracted from the GroupForm
// members pattern. Used for dependency selection (and reusable elsewhere).
export function MonitorMultiSelect({
  monitors,
  selectedIds,
  onChange,
  excludeIds = [],
  emptyMessage = 'No monitors available',
  groupMembers,
  maxHeightClass = 'max-h-56',
}: MonitorMultiSelectProps) {
  const [search, setSearch] = useState('');
  const [selectedTags, setSelectedTags] = useState<Set<string>>(new Set());
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  const selectable = useMemo(
    () => monitors.filter((m) => !excludeIds.includes(m.id)),
    [monitors, excludeIds]
  );

  // Tag options carry how many selectable monitors wear them, so the filter can
  // lead with the tags that actually cover something.
  const allTags = useMemo(() => {
    const counts = new Map<string, number>();
    for (const m of selectable) {
      for (const tag of m.tags ?? []) counts.set(tag, (counts.get(tag) ?? 0) + 1);
    }
    return Array.from(counts, ([tag, count]) => ({ tag, count })).sort((a, b) =>
      a.tag.localeCompare(b.tag)
    );
  }, [selectable]);

  const query = search.trim().toLowerCase();
  // Tag chips are an OR filter, matching the monitors list page; it stacks with
  // the text query so "prod" + "api" narrows to production APIs.
  const matches = useCallback(
    (m: Monitor) => {
      if (selectedTags.size > 0 && !(m.tags ?? []).some((tag) => selectedTags.has(tag))) {
        return false;
      }
      if (!query) return true;
      return (
        m.name.toLowerCase().includes(query) ||
        m.type.toLowerCase().includes(query) ||
        (m.tags ?? []).some((tag) => tag.toLowerCase().includes(query))
      );
    },
    [query, selectedTags]
  );

  const filtering = query !== '' || selectedTags.size > 0;

  const toggleTag = (tag: string) => {
    setSelectedTags((prev) => {
      const next = new Set(prev);
      if (next.has(tag)) next.delete(tag);
      else next.add(tag);
      return next;
    });
  };

  const filtered = useMemo(() => selectable.filter(matches), [selectable, matches]);

  const grouped = groupMembers !== undefined;

  // Group view: a group monitor is a top-level row carrying its members; a
  // monitor that belongs to a group is only shown nested under it.
  const tree = useMemo(() => {
    if (!grouped) return null;

    const byId = new Map(selectable.map((m) => [m.id, m]));
    const memberships = new Map<string, string[]>();
    for (const [groupId, memberIds] of Object.entries(groupMembers!)) {
      // A group the caller excluded renders nowhere, so its members must stay
      // in the flat list rather than hide under an invisible parent.
      if (!byId.has(groupId)) continue;
      for (const memberId of memberIds) {
        memberships.set(memberId, [...(memberships.get(memberId) ?? []), groupId]);
      }
    }

    const groups = selectable
      .filter((m) => m.type === 'group' && !memberships.has(m.id))
      .map((group) => ({
        group,
        // Drop member ids the caller excluded or that we never loaded.
        members: (groupMembers![group.id] ?? [])
          .map((id) => byId.get(id))
          .filter((m): m is Monitor => m !== undefined),
      }));

    const loose = selectable.filter((m) => m.type !== 'group' && !memberships.has(m.id));

    return { groups, loose, memberships };
  }, [grouped, groupMembers, selectable]);

  // Search keeps a group when the group itself matches (all members stay
  // visible) or when only some members match (just those).
  const visibleGroups = useMemo(() => {
    if (!tree) return [];
    return tree.groups.flatMap(({ group, members }) => {
      if (matches(group)) return [{ group, members, total: members.length }];
      const hits = members.filter(matches);
      // `total` stays the real membership so a search can't make a group look
      // smaller than what selecting it would actually cover.
      return hits.length > 0 ? [{ group, members: hits, total: members.length }] : [];
    });
  }, [tree, matches]);

  const visibleLoose = useMemo(() => (tree ? tree.loose.filter(matches) : []), [tree, matches]);

  const selected = useMemo(() => new Set(selectedIds), [selectedIds]);

  // Monitors muted/covered by the selection: the ids themselves plus every
  // member of a selected group.
  const coveredCount = useMemo(() => {
    if (!grouped) return selectedIds.length;
    const covered = new Set(selectedIds);
    for (const id of selectedIds) {
      for (const memberId of groupMembers![id] ?? []) covered.add(memberId);
    }
    return covered.size;
  }, [grouped, groupMembers, selectedIds]);

  // Members of a selected group are already covered, so keep them out of the
  // payload — that also makes a group's checkbox unambiguous.
  const prune = (ids: string[]) => {
    const next = new Set(ids);
    for (const id of ids) {
      if (next.has(id)) {
        for (const memberId of groupMembers?.[id] ?? []) next.delete(memberId);
      }
    }
    return Array.from(next);
  };

  const toggle = (id: string) => {
    if (selected.has(id)) {
      onChange(selectedIds.filter((x) => x !== id));
    } else {
      onChange(prune([...selectedIds, id]));
    }
  };

  const selectAllFiltered = () => {
    if (!grouped) {
      onChange(prune([...selectedIds, ...filtered.map((m) => m.id)]));
      return;
    }
    // A group only gets selected wholesale when the group itself passes the
    // filter; when a filter merely matched some members, take those members so
    // "select all" can't quietly pull in their untagged siblings.
    const ids = visibleGroups.flatMap(({ group, members }) =>
      matches(group) ? [group.id] : members.map((m) => m.id)
    );
    onChange(prune([...selectedIds, ...ids, ...visibleLoose.map((m) => m.id)]));
  };

  const toggleExpanded = (groupId: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(groupId)) next.delete(groupId);
      else next.add(groupId);
      return next;
    });
  };

  const rowClass = (isSelected: boolean) =>
    `flex items-center gap-3 rounded-lg px-3 py-2 transition-colors ${
      isSelected ? 'border border-cyan-500/30 bg-cyan-500/10' : 'border border-transparent hover:bg-white/[0.03]'
    }`;

  const monitorRow = (mon: Monitor, opts?: { covered?: boolean; coveredBy?: string }) => {
    const covered = opts?.covered ?? false;
    const isSelected = selected.has(mon.id);
    return (
      <div key={mon.id} className={rowClass(isSelected)}>
        <SelectCheckbox
          checked={isSelected || covered}
          disabled={covered}
          onChange={() => toggle(mon.id)}
          label={`Select ${mon.name}`}
        />
        <button
          type="button"
          disabled={covered}
          onClick={() => toggle(mon.id)}
          className={`flex min-w-0 flex-1 items-center gap-3 text-left ${covered ? 'cursor-default' : 'cursor-pointer'}`}
        >
          <div className="min-w-0 flex-1">
            <p className={`truncate text-sm font-medium ${covered ? 'text-slate-400' : 'text-white'}`}>
              {mon.name}
            </p>
            <div className="flex min-w-0 items-center gap-1.5">
              <p className="flex-shrink-0 text-xs text-slate-500">
                {covered ? `Covered by ${opts?.coveredBy}` : mon.type.toUpperCase()}
              </p>
              {(mon.tags ?? []).slice(0, 2).map((tag) => (
                <TagPill key={tag} tag={tag} size="xs" />
              ))}
              {(mon.tags?.length ?? 0) > 2 && (
                <span className="text-[9px] text-slate-500">+{mon.tags!.length - 2}</span>
              )}
            </div>
          </div>
          <Pill tone={mon.enabled ? 'success' : 'neutral'} size="xs" dot>
            {mon.enabled ? 'Active' : 'Paused'}
          </Pill>
        </button>
      </div>
    );
  };

  const nothingVisible = grouped
    ? visibleGroups.length === 0 && visibleLoose.length === 0
    : filtered.length === 0;

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" />
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={grouped ? 'Search monitors and groups...' : 'Search monitors...'}
            // `.input` sets padding via shorthand and wins the cascade, so the
            // icon clearance has to be important or the placeholder sits under it.
            className="input !pl-9"
            aria-label="Search monitors"
          />
        </div>
      </div>

      {allTags.length > 0 && (
        <TagFilter
          tags={allTags}
          selected={selectedTags}
          onToggle={toggleTag}
          onClear={() => setSelectedTags(new Set())}
        />
      )}

      <div className="flex items-center justify-between">
        <span className="text-xs text-slate-400">
          {grouped ? (
            <>
              {selectedIds.length} selected
              {coveredCount !== selectedIds.length && (
                <span className="text-slate-500"> · covers {coveredCount} monitors</span>
              )}
            </>
          ) : (
            <>
              {selectedIds.length} of {selectable.length} selected
            </>
          )}
        </span>
        <div className="flex gap-1.5">
          <Button variant="ghost" size="xs" type="button" onClick={selectAllFiltered}>
            {/* Say which "all" is meant — with a filter on, it's only the matches. */}
            {filtering ? 'Select matching' : 'Select all'}
          </Button>
          <Button variant="ghost" size="xs" type="button" onClick={() => onChange([])}>
            Clear
          </Button>
        </div>
      </div>

      <div
        className={`${maxHeightClass} overflow-y-auto rounded-lg border border-white/[0.06] bg-slate-900/40 p-2`}
      >
        {nothingVisible ? (
          <p className="py-6 text-center text-sm text-slate-500">
            {selectable.length === 0 ? emptyMessage : 'No monitors match your filters'}
          </p>
        ) : !grouped ? (
          <div className="space-y-1">{filtered.map((mon) => monitorRow(mon))}</div>
        ) : (
          <div className="space-y-3">
            {visibleGroups.length > 0 && (
              <div className="space-y-1">
                <p className="px-1 text-[10px] font-semibold uppercase tracking-wider text-slate-500">
                  Groups
                </p>
                {visibleGroups.map(({ group, members, total }) => {
                  const isSelected = selected.has(group.id);
                  const selectedMembers = members.filter((m) => selected.has(m.id)).length;
                  const isOpen = expanded.has(group.id) || (filtering && members.length > 0);
                  return (
                    <div key={group.id}>
                      <div className={rowClass(isSelected)}>
                        <SelectCheckbox
                          checked={isSelected}
                          indeterminate={!isSelected && selectedMembers > 0}
                          onChange={() => toggle(group.id)}
                          label={`Select group ${group.name}`}
                        />
                        <button
                          type="button"
                          onClick={() => toggle(group.id)}
                          className="flex min-w-0 flex-1 cursor-pointer items-center gap-2 text-left"
                        >
                          <Layers className="h-3.5 w-3.5 flex-shrink-0 text-indigo-400" />
                          <div className="min-w-0 flex-1">
                            <p className="truncate text-sm font-medium text-white">{group.name}</p>
                            <p className="text-xs text-slate-500">
                              {isSelected ? 'Group — covers ' : 'Group — '}
                              {total} {total === 1 ? 'member' : 'members'}
                              {members.length !== total && ` · ${members.length} match`}
                            </p>
                          </div>
                        </button>
                        {members.length > 0 && (
                          <button
                            type="button"
                            onClick={() => toggleExpanded(group.id)}
                            aria-expanded={isOpen}
                            aria-label={`${isOpen ? 'Hide' : 'Show'} members of ${group.name}`}
                            className="flex-shrink-0 rounded p-1 text-slate-500 transition-colors hover:bg-white/[0.06] hover:text-slate-300"
                          >
                            {isOpen ? (
                              <ChevronDown className="h-3.5 w-3.5" />
                            ) : (
                              <ChevronRight className="h-3.5 w-3.5" />
                            )}
                          </button>
                        )}
                      </div>
                      {isOpen && members.length > 0 && (
                        <div className="mt-1 space-y-1 border-l border-white/[0.08] pl-3 ml-4">
                          {members.map((member) =>
                            monitorRow(member, { covered: isSelected, coveredBy: group.name })
                          )}
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            )}

            {visibleLoose.length > 0 && (
              <div className="space-y-1">
                {visibleGroups.length > 0 && (
                  <p className="px-1 text-[10px] font-semibold uppercase tracking-wider text-slate-500">
                    Ungrouped
                  </p>
                )}
                {visibleLoose.map((mon) => monitorRow(mon))}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
