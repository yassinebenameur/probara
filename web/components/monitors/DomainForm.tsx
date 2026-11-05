'use client';

import { useRef, useState } from 'react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest } from '@/lib/types';
import {
  DOMAIN_INTERVAL_PRESETS,
  DOMAIN_MIN_INTERVAL_SECONDS,
  buildDomainRequest,
  initialDomainFormState,
  validateDomainForm,
} from '@/lib/monitor-form/domain';
import FormField from '@/components/ui/FormField';
import CollapsibleSection from '@/components/ui/CollapsibleSection';
import { AlertingSection } from './AlertingSection';
import ScheduleSection from './ScheduleSection';
import DependenciesField from './DependenciesField';
import { FieldHint, FormPanel, LabelsSection, NumberInput, PlanSummary, PlanTarget, ToggleRow, downAfterLabel, everyLabel, revealFirstError, reportConstraintViolation, useClearErrorsOnEdit, useFollowEnabled, FormActionBar } from './form-kit';

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

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (reportConstraintViolation(formRef.current)) return;
    setErrors({});
    const newErrors = validateDomainForm(formData);
    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      revealFirstError(formRef.current);
      return;
    }
    await onSubmit(buildDomainRequest(formData));
  };

  const domain = formData.domain.trim();

  return (
    <form ref={formRef} onSubmit={handleSubmit} noValidate data-form-stack className="space-y-5">
      <FormPanel title="Target">
        <FormField label="Monitor name" required error={errors.name}>
          <input
            className="input"
            value={formData.name}
            onChange={(e) => setFormData({ ...formData, name: e.target.value })}
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
                onChange={(e) => setFormData({ ...formData, domain: e.target.value })}
                placeholder="example.com"
                autoCapitalize="off"
                spellCheck={false}
              />
              <FieldHint>The name you registered, not a subdomain: example.com or example.co.uk.</FieldHint>
            </>
          )}
        </FormField>
      </FormPanel>

      <FormPanel
        title="Check"
        rows={
          <CollapsibleSection
            variant="row"
            title="Lookup"
            summary={formData.rdap_server_override.trim() ? 'Custom RDAP server' : formData.whois_fallback ? 'RDAP, then WHOIS' : 'RDAP only'}
            defaultOpen={Boolean(formData.rdap_server_override.trim()) || !formData.whois_fallback}
          >
            <div className="space-y-4">
              <FormField label="RDAP server" error={errors.rdap_server_override}>
                {(control) => (
                  <>
                    <input
                      {...control}
                      className="input font-mono"
                      type="url"
                      value={formData.rdap_server_override}
                      onChange={(e) => setFormData({ ...formData, rdap_server_override: e.target.value })}
                      placeholder="https://rdap.example-registry.net/"
                    />
                    <FieldHint>Optional. By default the IANA bootstrap registry names the server for the domain&apos;s TLD.</FieldHint>
                  </>
                )}
              </FormField>
              <ToggleRow
                title="Fall back to WHOIS"
                description="Ask WHOIS (TCP port 43) when RDAP cannot answer. Workers need outbound access to port 43."
                checked={formData.whois_fallback}
                onChange={(v) => setFormData({ ...formData, whois_fallback: v })}
              />
            </div>
          </CollapsibleSection>
        }
      >
        <ToggleRow
          title="Alert before the registration expires"
          description="Opens a domain expiry alert while the check stays up. The check itself fails only once the domain has expired or is not registered."
          checked={formData.alert_on_expiry}
          onChange={(v) => setFormData({ ...formData, alert_on_expiry: v })}
        />
        {formData.alert_on_expiry && (
          <FormField label="Warn when fewer days remain" error={errors.min_days_valid}>
            {(control) => (
              <span className="flex items-center gap-2">
                <NumberInput
                  {...control}
                  value={formData.min_days_valid}
                  onChange={(n) => setFormData((prev) => ({ ...prev, min_days_valid: n }))}
                  min={0}
                  max={3650}
                  className="input input-sm !w-24 tabular-nums"
                />
                <span className="text-xs text-slate-500">days</span>
              </span>
            )}
          </FormField>
        )}
      </FormPanel>

      <ScheduleSection
        intervalSeconds={formData.interval_seconds}
        onIntervalChange={(n) => setFormData((prev) => ({ ...prev, interval_seconds: n }))}
        timeoutSeconds={formData.timeout_seconds}
        onTimeoutChange={(n) => setFormData((prev) => ({ ...prev, timeout_seconds: n }))}
        timeoutError={errors.timeout_seconds}
        intervalError={errors.interval_seconds}
        minIntervalSeconds={DOMAIN_MIN_INTERVAL_SECONDS}
        intervalPresets={DOMAIN_INTERVAL_PRESETS}
        intervalHint="At least an hour: registries rate-limit lookups"
        locations={{
          selectedIds: formData.location_ids,
          onChange: (ids) => setFormData((prev) => ({ ...prev, location_ids: ids })),
          quorum: formData.location_quorum,
          onQuorumChange: (n) => setFormData((prev) => ({ ...prev, location_quorum: n })),
        }}
        enabled={formData.enabled}
        onEnabledChange={(v) => setFormData((prev) => ({ ...prev, enabled: v }))}
        enabledDescription="Look the domain up on this schedule"
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
          onThresholdChange={(n) => setFormData((prev) => ({ ...prev, consecutive_failures_threshold: n }))}
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
        onTagsChange={(v) => setFormData({ ...formData, tags: v })}
        placeholder="domains, production"
      />

      <FormActionBar
        summary={
          <PlanSummary
            errors={errors}
            parts={
              domain
                ? [
                    everyLabel(formData.interval_seconds),
                    <PlanTarget key="t">{domain}</PlanTarget>,
                    formData.alert_on_expiry ? `warn under ${formData.min_days_valid} days` : 'fail once expired',
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
