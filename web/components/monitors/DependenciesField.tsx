'use client';

import { useEffect, useState } from 'react';
import CollapsibleSection from '@/components/ui/CollapsibleSection';
import { getAllMonitors } from '@/lib/monitor-list';
import type { Monitor } from '@/lib/types';
import { MonitorMultiSelect } from './MonitorMultiSelect';

interface DependenciesFieldProps {
  selectedIds: string[];
  onChange: (ids: string[]) => void;
  /** The monitor being edited, so it can't depend on itself. */
  excludeId?: string;
  error?: string;
}

/**
 * Upstream dependencies as a disclosure row of the Alerting panel: their only
 * effect is on alerts (root-cause annotation, optional suppression), and the
 * suppression control in AlertingSection appears once one is selected.
 * Every non-group type gets it; the API ignores dependencies on groups.
 */
export default function DependenciesField({ selectedIds, onChange, excludeId, error }: DependenciesFieldProps) {
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [open, setOpen] = useState(selectedIds.length > 0);

  useEffect(() => {
    let cancelled = false;
    // Every page: a workspace can hold more monitors than one page.
    getAllMonitors()
      .then((all) => {
        if (!cancelled) setMonitors(all);
      })
      .catch(() => {
        // The picker degrades to empty; not fatal for the form.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <CollapsibleSection
      variant="row"
      title="Dependencies"
      summary={error ? 'Fix errors' : selectedIds.length > 0 ? `${selectedIds.length} selected` : 'None'}
      tone={error ? 'danger' : 'default'}
      isOpen={open || Boolean(error)}
      onToggle={setOpen}
    >
      <div className="space-y-4">
        <p className="text-ui text-slate-400">
          Upstream monitors this one relies on, such as the database behind an API. While one of them is down,
          this monitor&apos;s alerts name it as the likely root cause.
        </p>
        <MonitorMultiSelect
          monitors={monitors}
          selectedIds={selectedIds}
          onChange={onChange}
          excludeIds={excludeId ? [excludeId] : []}
        />
        {error && (
          <p data-form-error className="text-xs text-rose-300">
            {error}
          </p>
        )}
      </div>
    </CollapsibleSection>
  );
}
