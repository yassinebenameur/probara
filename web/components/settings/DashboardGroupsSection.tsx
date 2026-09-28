'use client';

import { useEffect, useState } from 'react';
import { Plus, X } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import { getDashboardSummary, updateTenantSettings } from '@/lib/api';

interface Props {
  initialTags: string[];
  onSaved?: (next: string[]) => void;
}

export default function DashboardGroupsSection({ initialTags, onSaved }: Props) {
  const [selected, setSelected] = useState<string[]>(initialTags);
  const [availableTags, setAvailableTags] = useState<string[]>([]);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setSelected(initialTags);
  }, [initialTags]);

  useEffect(() => {
    getDashboardSummary({ range: '24h' })
      .then((s) => setAvailableTags(s.available_tags || []))
      .catch(() => setAvailableTags([]));
  }, []);

  async function persist(next: string[]) {
    setSaving(true);
    setError(null);
    try {
      const updated = await updateTenantSettings({ dashboard_group_tags: next });
      setSelected(updated.dashboard_group_tags);
      onSaved?.(updated.dashboard_group_tags);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save');
    } finally {
      setSaving(false);
    }
  }

  const addable = availableTags.filter((t) => !selected.includes(t)).sort((a, b) => a.localeCompare(b));

  return (
    <Panel
      title="Dashboard groups"
      subtitle="Tags shown as service groups on the dashboard. Other tags stay available for filtering and search."
      allowOverflow
    >
      <div className="flex flex-wrap items-center gap-2">
        {selected.length === 0 && (
          <p className="text-sm text-slate-500">No groups configured yet.</p>
        )}
        {selected.map((tag) => (
          <span
            key={tag}
            className="inline-flex h-7 items-center gap-1 rounded-full bg-well-hover pl-3 pr-1 text-xs font-medium text-slate-200"
          >
            {tag}
            <button
              type="button"
              onClick={() => persist(selected.filter((t) => t !== tag))}
              aria-label={`Remove ${tag}`}
              disabled={saving}
              className="inline-flex h-5 w-5 items-center justify-center rounded-full text-slate-400 transition-colors hover:bg-control-hover hover:text-white disabled:opacity-40"
            >
              <X className="h-3 w-3" strokeWidth={2} />
            </button>
          </span>
        ))}

        <div className="relative">
          <Button
            type="button"
            variant="ghost"
            size="xs"
            icon={<Plus strokeWidth={1.75} />}
            onClick={() => setPickerOpen((v) => !v)}
            disabled={saving || addable.length === 0}
          >
            Add tag
          </Button>
          {pickerOpen && (
            <div className="glass-overlay absolute left-0 z-50 mt-2 w-72 !rounded-xl p-2">
              {addable.length === 0 ? (
                <p className="px-1 py-0.5 text-xs text-slate-500">No more tags to add.</p>
              ) : (
                <div className="flex max-h-56 flex-wrap gap-1.5 overflow-y-auto">
                  {addable.map((tag) => (
                    <button
                      type="button"
                      key={tag}
                      onClick={() => {
                        setPickerOpen(false);
                        persist([...selected, tag].sort((a, b) => a.localeCompare(b)));
                      }}
                      className="rounded-full bg-well-hover px-2.5 py-1 text-xs text-slate-300 transition-colors hover:bg-control-hover hover:text-white"
                    >
                      {tag}
                    </button>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>
      </div>

      {error && <p className="mt-2 text-xs text-rose-300">{error}</p>}
    </Panel>
  );
}
