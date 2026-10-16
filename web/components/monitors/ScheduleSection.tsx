'use client';

import CollapsibleSection from '@/components/ui/CollapsibleSection';
import FormField from '@/components/ui/FormField';
import { handleRovingKeyDown, SELECTED_SEGMENT } from '@/components/ui/roving';
import { formatDuration } from '@/lib/format';
import type { Location } from '@/lib/types';
import { LocationsSection } from './LocationsSection';
import { FieldHint, FormPanel, NumberInput, ToggleRow } from './form-kit';

export const DEFAULT_INTERVAL_PRESETS = [30, 60, 300, 900] as const;

/**
 * Seconds-valued field with preset segments plus a free number input, so the
 * common cadences are one click and anything else is still typeable.
 */
export function IntervalField({
  label,
  value,
  onChange,
  presets = DEFAULT_INTERVAL_PRESETS,
  min,
  step,
  error,
  hint,
}: {
  label: string;
  value: number;
  onChange: (seconds: number) => void;
  presets?: readonly number[];
  min?: number;
  step?: number;
  error?: string;
  hint?: string;
}) {
  const ids = presets.map(String);
  const active = String(value);
  const anySelected = ids.includes(active);
  return (
    <FormField label={label} error={error}>
      {(control) => (
        <>
          <div className="flex flex-wrap items-center gap-2">
            <div
              role="radiogroup"
              aria-label={`${label} presets`}
              onKeyDown={(e) => handleRovingKeyDown(e, ids, active, (id) => onChange(Number(id)))}
              className="inline-flex items-center gap-0.5 rounded-lg bg-well p-0.5 shadow-[inset_0_0_0_1px_var(--glass-edge)]"
            >
              {presets.map((seconds, i) => {
                const selected = seconds === value;
                return (
                  <button
                    key={seconds}
                    type="button"
                    role="radio"
                    aria-checked={selected}
                    data-roving-id={String(seconds)}
                    tabIndex={selected || (!anySelected && i === 0) ? 0 : -1}
                    onClick={() => onChange(seconds)}
                    className={`inline-flex h-7 items-center rounded-md px-2.5 text-xs font-medium tabular-nums transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/50 ${
                      selected ? SELECTED_SEGMENT : 'text-slate-400 hover:text-slate-100'
                    }`}
                  >
                    {formatDuration(seconds)}
                  </button>
                );
              })}
            </div>
            <span className="flex items-center gap-2">
              <NumberInput {...control} value={value} onChange={onChange} min={min} step={step} className="input input-sm !w-24 tabular-nums" />
              <span className="text-xs text-slate-500">seconds</span>
            </span>
          </div>
          {hint && !error && <FieldHint>{hint}</FieldHint>}
        </>
      )}
    </FormField>
  );
}

interface ScheduleSectionProps {
  intervalSeconds: number;
  onIntervalChange: (seconds: number) => void;
  timeoutSeconds: number;
  onTimeoutChange: (seconds: number) => void;
  timeoutError?: string;
  intervalError?: string;
  /** Bounds a type puts on its schedule (a SIP test call: at least 60 s, at most 20 s). */
  minIntervalSeconds?: number;
  intervalPresets?: readonly number[];
  intervalHint?: string;
  maxTimeoutSeconds?: number;
  /** Omit for monitors that only run on the default fleet. */
  locations?: {
    selectedIds: string[];
    onChange: (ids: string[]) => void;
    quorum: number;
    onQuorumChange: (n: number) => void;
    onLocationsLoaded?: (locations: Location[]) => void;
  };
  enabled: boolean;
  onEnabledChange: (enabled: boolean) => void;
  /** What "enabled" does for this type, e.g. "Run SIP OPTIONS checks on schedule". */
  enabledDescription?: string;
}

/**
 * The Schedule panel every active-check form shares: cadence, timeout, where
 * the check runs, and whether it runs at all.
 */
export default function ScheduleSection({
  intervalSeconds,
  onIntervalChange,
  timeoutSeconds,
  onTimeoutChange,
  timeoutError,
  intervalError,
  minIntervalSeconds = 10,
  intervalPresets,
  intervalHint,
  maxTimeoutSeconds,
  locations,
  enabled,
  onEnabledChange,
  enabledDescription = 'Run checks on this schedule',
}: ScheduleSectionProps) {
  const overlaps = timeoutSeconds >= intervalSeconds;
  const selectedCount = locations?.selectedIds.length ?? 0;
  return (
    <FormPanel
      title="Schedule"
      rows={
        locations && (
          <CollapsibleSection
            variant="row"
            title="Locations"
            summary={selectedCount > 0 ? `${selectedCount} selected` : 'Default fleet'}
            defaultOpen={selectedCount > 0}
          >
            <LocationsSection
              selectedIds={locations.selectedIds}
              onChange={locations.onChange}
              quorum={locations.quorum}
              onQuorumChange={locations.onQuorumChange}
              onLocationsLoaded={locations.onLocationsLoaded}
            />
          </CollapsibleSection>
        )
      }
    >
      <div className="flex flex-wrap gap-x-10 gap-y-4">
        <IntervalField
          label="Check every"
          value={intervalSeconds}
          onChange={onIntervalChange}
          presets={intervalPresets}
          min={minIntervalSeconds}
          step={5}
          error={intervalError}
          hint={intervalHint}
        />
        <FormField label="Timeout" error={timeoutError}>
          {(control) => (
            <>
              <span className="flex items-center gap-2">
                <NumberInput
                  {...control}
                  value={timeoutSeconds}
                  onChange={onTimeoutChange}
                  min={1}
                  max={maxTimeoutSeconds}
                  className="input input-sm !w-24 tabular-nums"
                />
                <span className="text-xs text-slate-500">seconds</span>
              </span>
              {!timeoutError &&
                (overlaps ? (
                  <FieldHint tone="warning">At least the interval: checks would overlap</FieldHint>
                ) : (
                  <FieldHint>
                    {maxTimeoutSeconds ? `Less than the interval, at most ${maxTimeoutSeconds} seconds` : 'Less than the interval'}
                  </FieldHint>
                ))}
            </>
          )}
        </FormField>
      </div>
      <ToggleRow title="Monitor enabled" description={enabledDescription} checked={enabled} onChange={onEnabledChange} />
    </FormPanel>
  );
}
