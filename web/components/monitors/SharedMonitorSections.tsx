'use client';

import { useEffect, useState } from 'react';
import type { Location, Monitor } from '@/lib/types';
import { getMonitors } from '@/lib/api';
import type { FormErrors } from '@/lib/monitor-form/common';
import type { SharedMonitorFields } from '@/lib/monitor-form/shared';
import FormSection from '@/components/ui/FormSection';
import { AlertingSection } from './AlertingSection';
import { LocationsSection } from './LocationsSection';
import { FormInput, FormToggle } from './MonitorFormControls';
import { MonitorMultiSelect } from './MonitorMultiSelect';

interface SharedMonitorSectionsProps {
  value: SharedMonitorFields;
  onChange: (patch: Partial<SharedMonitorFields>) => void;
  errors: FormErrors;
  /** Excluded from the dependency picker (a monitor cannot depend on itself). */
  monitorId?: string;
  onLocationsLoaded: (locations: Location[]) => void;
}

/** Schedule, locations, alerting, dependencies and meta: the sections every inline monitor type shares. */
export default function SharedMonitorSections({ value, onChange, errors, monitorId, onLocationsLoaded }: SharedMonitorSectionsProps) {
  const [availableMonitors, setAvailableMonitors] = useState<Monitor[]>([]);

  useEffect(() => {
    let cancelled = false;
    getMonitors({ page_size: 100 })
      .then((res) => {
        if (!cancelled) setAvailableMonitors(res.items || []);
      })
      .catch(() => {
        // Dependency picker degrades to empty; not fatal for the form.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <>
      <FormSection title="Schedule">
        <div className="grid grid-cols-2 gap-4">
          <FormInput
            label="Interval (seconds)"
            type="number"
            value={value.interval_seconds}
            onChange={(v) => onChange({ interval_seconds: parseInt(v) || 60 })}
            min={10}
            step={5}
            hint="How often to check"
          />
          <FormInput
            label="Timeout (seconds)"
            type="number"
            value={value.timeout_seconds}
            onChange={(v) => onChange({ timeout_seconds: parseInt(v) || 30 })}
            min={1}
            error={errors.timeout_seconds}
            hint="Must be less than interval"
          />
        </div>
      </FormSection>

      <FormSection
        title="Locations"
        summary={value.location_ids.length > 0 ? `${value.location_ids.length} selected` : 'Default fleet'}
        collapsible
        defaultOpen={value.location_ids.length > 0}
      >
        <LocationsSection
          selectedIds={value.location_ids}
          onChange={(location_ids) => onChange({ location_ids })}
          quorum={value.location_quorum}
          onQuorumChange={(location_quorum) => onChange({ location_quorum })}
          onLocationsLoaded={onLocationsLoaded}
        />
      </FormSection>

      <FormSection title="Alerting">
        <AlertingSection
          isGroup={false}
          intervalSeconds={value.interval_seconds}
          threshold={value.consecutive_failures_threshold}
          onThresholdChange={(consecutive_failures_threshold) => onChange({ consecutive_failures_threshold })}
          mode={value.notification_mode}
          onModeChange={(notification_mode) => onChange({ notification_mode })}
          dependencySuppression={value.dependency_suppression}
          onDependencySuppressionChange={(dependency_suppression) => onChange({ dependency_suppression })}
          customChannels={value.notification_channels}
          onCustomChannelsChange={(notification_channels) => onChange({ notification_channels })}
        />
      </FormSection>

      <FormSection
        title="Dependencies"
        summary={value.depends_on_ids.length > 0 ? `${value.depends_on_ids.length} selected` : 'None'}
        collapsible
        defaultOpen={value.depends_on_ids.length > 0}
        infoTip={{
          title: 'Monitor dependencies',
          entries: [
            {
              label: 'What it does',
              value:
                'When an upstream dependency is down, alerts for this monitor are annotated with the likely root cause.',
            },
          ],
        }}
      >
        <p className="text-xs text-slate-400">
          Upstream monitors this one depends on (e.g. the database behind this API).
        </p>
        <MonitorMultiSelect
          monitors={availableMonitors}
          selectedIds={value.depends_on_ids}
          onChange={(depends_on_ids) => onChange({ depends_on_ids })}
          excludeIds={monitorId ? [monitorId] : []}
        />
        {errors.depends_on_ids && <p className="text-xs text-rose-400">{errors.depends_on_ids}</p>}
      </FormSection>

      <FormSection title="Meta">
        <FormInput
          label="Tags"
          value={value.tags}
          onChange={(tags) => onChange({ tags })}
          placeholder="production, api, critical"
          hint="Comma-separated tags for filtering"
        />
        <FormToggle
          label="Monitor enabled"
          description="Run checks on the configured schedule"
          checked={value.enabled}
          onChange={(enabled) => onChange({ enabled })}
        />
      </FormSection>
    </>
  );
}
