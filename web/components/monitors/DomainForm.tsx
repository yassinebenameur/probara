'use client';

import { useRef, useState } from 'react';
import { testMonitorConfig, type TestMonitorConfigResponse } from '@/lib/api';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest } from '@/lib/types';
import {
  DOMAIN_INTERVAL_PRESETS,
  DOMAIN_MIN_CONSECUTIVE_FAILURES,
  DOMAIN_MAX_MIN_DAYS_VALID,
  DOMAIN_MIN_INTERVAL_SECONDS,
  buildDomainConfig,
  buildDomainRequest,
  initialDomainFormState,
  validateDomainForm,
} from '@/lib/monitor-form/domain';
import { domainExpiryState, domainFacts } from '@/lib/domain-expiry';
import { UPTIME_TONE_TEXT } from '@/lib/status-tones';
import { errorMessage } from '@/lib/errors';
import Button from '@/components/ui/Button';
import FormField from '@/components/ui/FormField';
import { AlertingSection } from './AlertingSection';
import ScheduleSection from './ScheduleSection';
import DependenciesField from './DependenciesField';
import {
  FieldHint,
  FormActionBar,
  FormPanel,
  LabelsSection,
  NumberInput,
  PlanSummary,
  PlanTarget,
  downAfterLabel,
  everyLabel,
  revealFirstError,
  reportConstraintViolation,
  useClearErrorsOnEdit,
  useFollowEnabled,
} from './form-kit';

interface DomainFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

export default function DomainForm({ monitor, initialData, onSubmit, onCancel, loading = false }: DomainFormProps) {
  const isEditMode = Boolean(monitor);
  const [formData, setFormData] = useState(() => initialDomainFormState(monitor, initialData));
  const [errors, setErrors] = useState<Record<string, string>>({});
  useClearErrorsOnEdit(formData, setErrors);
  useFollowEnabled(monitor?.enabled, setFormData);
  const formRef = useRef<HTMLFormElement>(null);

  const [preview, setPreview] = useState<TestMonitorConfigResponse | null>(null);
  const [previewError, setPreviewError] = useState('');
  const [previewSettings, setPreviewSettings] = useState('');
  const [testing, setTesting] = useState(false);
  const settingsKey = JSON.stringify([buildDomainConfig(formData), formData.timeout_seconds, formData.location_ids]);

  const testLookup = async () => {
    const fieldErrors = validateDomainForm(formData);
    if (fieldErrors.domain) {
      setErrors({ domain: fieldErrors.domain });
      return;
    }
    setTesting(true);
    setPreview(null);
    setPreviewError('');
    setPreviewSettings(settingsKey);
    try {
      setPreview(
        await testMonitorConfig({
          type: 'domain',
          config: buildDomainConfig(formData),
          timeout_seconds: formData.timeout_seconds,
          monitor_id: monitor?.id,
          location_id: formData.location_ids[0],
        })
      );
    } catch (error) {
      setPreviewError(errorMessage(error, 'Lookup failed'));
    } finally {
      setTesting(false);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (reportConstraintViolation(formRef.current)) return;
    const newErrors = validateDomainForm(formData);
    setErrors(newErrors);
    if (Object.keys(newErrors).length > 0) {
      revealFirstError(formRef.current);
      return;
    }
    await onSubmit(buildDomainRequest(formData));
  };

  const previewInfo = preview?.metrics_data?.domain;
  const previewState = previewInfo ? domainExpiryState(previewInfo, formData.min_days_valid) : null;

  return (
    <form ref={formRef} onSubmit={handleSubmit} noValidate data-form-stack className="space-y-5">
      <FormPanel title="Target">
        <FormField label="Monitor name" required error={errors.name}>
          <input
            className="input"
            value={formData.name}
            onChange={(e) => setFormData((prev) => ({ ...prev, name: e.target.value }))}
            placeholder="example.com registration"
          />
        </FormField>
        <FormField label="Domain" required error={errors.domain}>
          {(control) => (
            <>
              <input
                {...control}
                className="input font-mono"
                value={formData.domain}
                onChange={(e) => setFormData((prev) => ({ ...prev, domain: e.target.value }))}
                placeholder="example.com"
                autoComplete="off"
                spellCheck={false}
              />
              <FieldHint>
                A subdomain is watched through its registered domain (www.example.co.uk checks example.co.uk).
                Internationalized names are accepted.
              </FieldHint>
            </>
          )}
        </FormField>
      </FormPanel>

      <FormPanel title="Check">
        <FormField label="Warn before expiry" error={errors.min_days_valid}>
          {(control) => (
            <>
              <span className="flex items-center gap-2">
                <NumberInput
                  {...control}
                  value={formData.min_days_valid}
                  onChange={(n) => setFormData((prev) => ({ ...prev, min_days_valid: n }))}
                  min={0}
                  max={DOMAIN_MAX_MIN_DAYS_VALID}
                  className="input input-sm !w-24 tabular-nums"
                />
                <span className="text-xs text-slate-500">days</span>
              </span>
              <FieldHint>
                Opens a domain expiry alert when fewer days remain; the monitor stays up until the registration
                actually expires. 0 alerts only once it has expired.
              </FieldHint>
            </>
          )}
        </FormField>
        <div>
          <div className="glass-well flex flex-wrap items-center gap-3 px-4 py-3">
            <Button type="button" variant="ghost" loading={testing} disabled={loading} onClick={testLookup}>
              {testing ? 'Looking up…' : 'Test lookup'}
            </Button>
            <p className="text-xs text-slate-500">Ask the registry now, through a worker, without saving.</p>
          </div>
          <div aria-live="polite">
            {previewError && <p className="mt-3 text-sm text-rose-300">{previewError}</p>}
            {preview && (
              <div className="glass-well mt-3 space-y-2 p-4 text-sm">
                {previewSettings !== settingsKey && (
                  <p className="text-amber-300">Settings changed since this lookup. Run it again to check the current settings.</p>
                )}
                {previewState ? (
                  <div className="flex items-center justify-between gap-3">
                    <span className="text-slate-500">{previewInfo?.domain}</span>
                    <span className={`font-medium ${UPTIME_TONE_TEXT[previewState.tone]}`}>{previewState.headline}</span>
                  </div>
                ) : (
                  <p className="text-amber-300">{preview.error_message || 'The lookup returned nothing.'}</p>
                )}
                {previewState?.reason && (
                  <p className={previewState.tone === 'none' ? 'text-slate-300' : 'text-amber-300'}>{previewState.reason}</p>
                )}
                {previewInfo &&
                  domainFacts(previewInfo).map((fact) => (
                    <div key={fact.label} className="flex justify-between gap-3">
                      <span className="text-slate-500">{fact.label}</span>
                      <span className={`min-w-0 break-words text-right ${fact.warning ? 'text-amber-300' : 'text-slate-300'}`}>{fact.value}</span>
                    </div>
                  ))}
              </div>
            )}
          </div>
        </div>
      </FormPanel>

      <ScheduleSection
        intervalSeconds={formData.interval_seconds}
        onIntervalChange={(n) => setFormData((prev) => ({ ...prev, interval_seconds: n }))}
        intervalError={errors.interval_seconds}
        minIntervalSeconds={DOMAIN_MIN_INTERVAL_SECONDS}
        intervalPresets={DOMAIN_INTERVAL_PRESETS}
        intervalHint="At most once an hour, from one location: registries rate-limit lookups"
        timeoutSeconds={formData.timeout_seconds}
        onTimeoutChange={(n) => setFormData((prev) => ({ ...prev, timeout_seconds: n }))}
        timeoutError={errors.timeout_seconds}
        locations={{
          selectedIds: formData.location_ids,
          // One location at most: a registry answers the same from
          // everywhere, so picking another replaces the first.
          onChange: (ids) => setFormData((prev) => ({ ...prev, location_ids: ids.slice(-1) })),
          quorum: formData.location_quorum,
          onQuorumChange: (n) => setFormData((prev) => ({ ...prev, location_quorum: n })),
        }}
        enabled={formData.enabled}
        onEnabledChange={(v) => setFormData((prev) => ({ ...prev, enabled: v }))}
        enabledDescription="Look the registration up on this schedule"
      />

      <FormPanel
        title="Alerting"
        rows={
          <DependenciesField
            selectedIds={formData.depends_on_ids}
            onChange={(ids) => setFormData((prev) => ({ ...prev, depends_on_ids: ids }))}
            excludeId={monitor?.id}
            error={errors.depends_on_ids}
          />
        }
      >
        <AlertingSection
          isGroup={false}
          intervalSeconds={formData.interval_seconds}
          threshold={formData.consecutive_failures_threshold}
          minThreshold={DOMAIN_MIN_CONSECUTIVE_FAILURES}
          onThresholdChange={(n) => setFormData((prev) => ({ ...prev, consecutive_failures_threshold: n }))}
          recoveryThreshold={formData.recovery_threshold}
          onRecoveryThresholdChange={(n) => setFormData((prev) => ({ ...prev, recovery_threshold: n }))}
          mode={formData.notification_mode}
          onModeChange={(m) => setFormData((prev) => ({ ...prev, notification_mode: m }))}
          dependencySuppression={formData.dependency_suppression}
          hasDependencies={formData.depends_on_ids.length > 0}
          onDependencySuppressionChange={(d) => setFormData((prev) => ({ ...prev, dependency_suppression: d }))}
          customChannels={formData.notification_channels}
          onCustomChannelsChange={(next) => setFormData((prev) => ({ ...prev, notification_channels: next }))}
        />
      </FormPanel>

      <LabelsSection
        tags={formData.tags}
        onTagsChange={(v) => setFormData((prev) => ({ ...prev, tags: v }))}
        placeholder="domains, billing"
      />

      <FormActionBar
        summary={
          <PlanSummary
            errors={errors}
            parts={
              formData.domain.trim()
                ? [
                    everyLabel(formData.interval_seconds),
                    <PlanTarget key="t">{formData.domain.trim()}</PlanTarget>,
                    formData.min_days_valid === 0 ? 'alert once expired' : `alert ${formData.min_days_valid} days before expiry`,
                    downAfterLabel(formData.consecutive_failures_threshold),
                  ]
                : [everyLabel(formData.interval_seconds), downAfterLabel(formData.consecutive_failures_threshold)]
            }
          />
        }
        cancel={onCancel ? { label: 'Cancel', onClick: onCancel, disabled: loading } : undefined}
        submit={{
          label: loading ? 'Saving…' : isEditMode ? 'Save changes' : 'Create monitor',
          loading,
          disabled: loading,
          type: 'submit',
        }}
      />
    </form>
  );
}
