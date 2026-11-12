'use client';

import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import FilterChip from '@/components/ui/FilterChip';
import Input from '@/components/ui/Input';
import { RETENTION_PRESETS, type RetentionSettingsState } from '@/components/settings/useRetentionSettings';

interface RetentionPanelProps {
  retention: RetentionSettingsState;
  /** Only workspace admins can change retention. */
  isTenantAdmin: boolean;
}

/** Settings → General: how long mesh history and raw host-metric samples are kept. */
export function RetentionPanel({ retention, isTenantAdmin }: RetentionPanelProps) {
  const {
    retentionLoading,
    retentionError,
    savingRetention,
    retentionInput,
    setRetentionInput,
    customRetention,
    setCustomRetention,
    loadRetentionSettings,
    handleSaveRetention,
    retentionDirty,
    customRetentionInvalid,
  } = retention;

  return (
    <Panel
      title="Data retention"
      subtitle="How long mesh history and raw host-metric samples are kept."
    >
      {retentionLoading ? (
        <p className="text-sm text-slate-500">Loading retention settings…</p>
      ) : retentionError ? (
        <Callout
          tone="danger"
          action={<Button variant="ghost" size="xs" onClick={loadRetentionSettings}>Retry</Button>}
        >
          {retentionError}
        </Callout>
      ) : (
        <div className="space-y-5">
          <div>
            <div className="flex flex-wrap gap-1.5" role="group" aria-label="Retention">
              {RETENTION_PRESETS.map((preset) => (
                <FilterChip
                  key={preset.label}
                  selected={!customRetention && retentionInput === preset.value}
                  disabled={!isTenantAdmin}
                  onClick={() => {
                    setCustomRetention(false);
                    setRetentionInput(preset.value);
                  }}
                >
                  {preset.label}
                </FilterChip>
              ))}
              <FilterChip
                selected={customRetention}
                disabled={!isTenantAdmin}
                onClick={() => setCustomRetention(true)}
              >
                Custom…
              </FilterChip>
            </div>
            {customRetention && (
              <div className="mt-3 sm:w-60">
                <Input
                  label="Retention days"
                  type="number"
                  min={30}
                  max={3650}
                  value={retentionInput}
                  disabled={!isTenantAdmin}
                  onChange={(event) => setRetentionInput(event.target.value)}
                  placeholder="30–3650"
                  error={customRetentionInvalid && retentionDirty ? 'Enter 30–3650 days' : undefined}
                />
              </div>
            )}
            <p className="mt-3 max-w-prose text-xs text-slate-500">
              Raw check results are always kept 30 days; uptime and latency history comes from hourly
              rollups kept 400 days.
            </p>
          </div>
          {isTenantAdmin && (
            <div className="flex items-center justify-end gap-3">
              {retentionDirty && <span className="text-xs text-slate-400">Unsaved changes</span>}
              <Button
                variant={retentionDirty ? 'accent' : 'ghost'}
                size="sm"
                onClick={handleSaveRetention}
                disabled={savingRetention || !retentionDirty || customRetentionInvalid}
                loading={savingRetention}
              >
                {savingRetention ? 'Saving…' : 'Save retention'}
              </Button>
            </div>
          )}
        </div>
      )}
    </Panel>
  );
}
