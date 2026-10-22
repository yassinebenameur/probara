'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import Panel from '@/components/ui/Panel';
import TagFilter, { type TagOption } from '@/components/monitors/TagFilter';
import TagPill from '@/components/monitors/TagPill';
import { getDashboardSummary, updateTenantSettings } from '@/lib/api';

interface Props {
  initialTags: string[];
  onSaved?: (next: string[]) => void;
  /** Non-admins see the groups but cannot add or remove them. */
  readOnly?: boolean;
}

const byName = (a: string, b: string) => a.localeCompare(b);

// Each change saves immediately, so there is no Save button. The picker is
// the shared tag chip (TagFilter as a value: a searchable FilterPopover, with
// the chosen tags as removable pills beside it).
export default function DashboardGroupsSection({ initialTags, onSaved, readOnly = false }: Props) {
  const [selected, setSelected] = useState<string[]>(initialTags);
  const [availableTags, setAvailableTags] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  // Saves are optimistic and may overlap while the popup stays open; only the
  // newest request's answer (or failure) is applied.
  const saveSeq = useRef(0);

  useEffect(() => {
    setSelected(initialTags);
  }, [initialTags]);

  useEffect(() => {
    getDashboardSummary({ range: '24h' })
      .then((s) => setAvailableTags(s.available_tags || []))
      .catch(() => setAvailableTags([]));
  }, []);

  async function persist(next: string[]) {
    const seq = ++saveSeq.current;
    const previous = selected;
    setSelected(next);
    setError(null);
    try {
      const updated = await updateTenantSettings({ dashboard_group_tags: next });
      if (seq !== saveSeq.current) return;
      setSelected(updated.dashboard_group_tags);
      onSaved?.(updated.dashboard_group_tags);
    } catch (err) {
      if (seq !== saveSeq.current) return;
      setSelected(previous);
      setError(err instanceof Error ? err.message : 'Failed to save');
    }
  }

  // A configured group whose tag no monitor wears any more still lists, so it
  // can be removed.
  const options = useMemo<TagOption[]>(
    () => Array.from(new Set([...availableTags, ...selected])).sort(byName).map((tag) => ({ tag })),
    [availableTags, selected]
  );
  const selectedSet = useMemo(() => new Set(selected), [selected]);

  return (
    <Panel
      title="Dashboard groups"
      subtitle="Tags shown as service groups on the dashboard. Other tags stay available for filtering and search."
      allowOverflow
    >
      {readOnly ? (
        <div className="flex flex-wrap items-center gap-1.5">
          {selected.length === 0 ? (
            <p className="text-sm text-slate-500">No groups configured yet.</p>
          ) : (
            selected.map((tag) => <TagPill key={tag} tag={tag} size="xs" />)
          )}
        </div>
      ) : (
        <div className="space-y-2">
          <TagFilter
            tags={options}
            selected={selectedSet}
            onChange={(next) => persist(Array.from(next).sort(byName))}
            asValue
            accessibleLabel="Dashboard group tags"
          />
          {selected.length === 0 && <p className="text-sm text-slate-500">No groups configured yet.</p>}
        </div>
      )}

      {error && <p className="mt-2 text-xs text-rose-300">{error}</p>}
    </Panel>
  );
}
