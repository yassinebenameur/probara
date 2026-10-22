'use client';

import { useRef, useState } from 'react';
import {
  Monitor,
  CreateMonitorRequest,
  UpdateMonitorRequest,
} from '@/lib/types';
import { buildTcpRequest, initialTcpFormState } from '@/lib/monitor-form/network';
import FormField from '@/components/ui/FormField';
import { AlertingSection } from './AlertingSection';
import ScheduleSection from './ScheduleSection';
import DependenciesField from './DependenciesField';
import { FieldHint, FormPanel, LabelsSection, PlanSummary, PlanTarget, ToggleRow, downAfterLabel, everyLabel, revealFirstError, reportConstraintViolation, useClearErrorsOnEdit, useFollowEnabled, FormActionBar } from './form-kit';

interface TcpFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

export default function TcpForm({
  monitor,
  initialData,
  onSubmit,
  onCancel,
  loading = false,
}: TcpFormProps) {
  const isEditMode = Boolean(monitor);

  const [formData, setFormData] = useState(() => initialTcpFormState(monitor, initialData));

  const [errors, setErrors] = useState<Record<string, string>>({});
  useClearErrorsOnEdit(formData, setErrors);
  useFollowEnabled(monitor?.enabled, setFormData);
  const formRef = useRef<HTMLFormElement>(null);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (reportConstraintViolation(formRef.current)) return;
    setErrors({});

    const newErrors: Record<string, string> = {};
    if (!formData.name.trim()) newErrors.name = 'Name is required';
    if (!formData.host.trim()) newErrors.host = 'Host is required';
    if (formData.port < 1 || formData.port > 65535) newErrors.port = 'Port must be between 1 and 65535';
    if (formData.timeout_seconds >= formData.interval_seconds) {
      newErrors.timeout_seconds = 'Timeout must be less than interval';
    }

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      revealFirstError(formRef.current);
      return;
    }

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = buildTcpRequest(formData);

    await onSubmit(requestData);
  };

  return (
    <form ref={formRef} onSubmit={handleSubmit} noValidate data-form-stack className="space-y-5">
      <FormPanel title="Target">
        <FormField label="Monitor name" required error={errors.name}>
          <input
            type="text"
            value={formData.name}
            onChange={(e) => setFormData({ ...formData, name: e.target.value })}
            placeholder="Postgres port reachability"
            className="input"
          />
        </FormField>

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div className="sm:col-span-2">
            <FormField label="Host" required error={errors.host}>
              {(control) => (
                <>
                  <input
                    {...control}
                    type="text"
                    value={formData.host}
                    onChange={(e) => setFormData({ ...formData, host: e.target.value })}
                    placeholder="db.internal.example.com"
                    className="input font-mono"
                  />
                  <FieldHint>Hostname or IP address to connect to</FieldHint>
                </>
              )}
            </FormField>
          </div>

          <FormField label="Port" required error={errors.port}>
            {(control) => (
              <>
                <input
                  {...control}
                  type="number"
                  value={formData.port || ''}
                  onChange={(e) => setFormData({ ...formData, port: parseInt(e.target.value, 10) || 0 })}
                  min={1}
                  max={65535}
                  placeholder="5432"
                  className="input tabular-nums"
                />
                <FieldHint>TCP port, e.g. 5432</FieldHint>
              </>
            )}
          </FormField>
        </div>

        <ToggleRow
          title="TLS handshake"
          description="Also complete a TLS handshake after connecting"
          checked={formData.use_tls}
          onChange={(v) => setFormData({ ...formData, use_tls: v })}
        />

        {formData.use_tls && (
          <ToggleRow
            title="Skip certificate verification"
            description="Accept self-signed or mismatched certificates"
            checked={formData.tls_skip_verify}
            onChange={(v) => setFormData({ ...formData, tls_skip_verify: v })}
            tone="warning"
          />
        )}
      </FormPanel>

      <ScheduleSection
        intervalSeconds={formData.interval_seconds}
        onIntervalChange={(n) => setFormData((prev) => ({ ...prev, interval_seconds: n }))}
        timeoutSeconds={formData.timeout_seconds}
        onTimeoutChange={(n) => setFormData((prev) => ({ ...prev, timeout_seconds: n }))}
        timeoutError={errors.timeout_seconds}
        locations={{
          selectedIds: formData.location_ids,
          onChange: (ids) => setFormData((prev) => ({ ...prev, location_ids: ids })),
          quorum: formData.location_quorum,
          onQuorumChange: (n) => setFormData((prev) => ({ ...prev, location_quorum: n })),
        }}
        enabled={formData.enabled}
        onEnabledChange={(v) => setFormData((prev) => ({ ...prev, enabled: v }))}
        enabledDescription="Run TCP connect checks on this schedule"
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
        placeholder="production, network, critical"
      />

      <FormActionBar
        summary={
          <PlanSummary
            errors={errors}
            parts={
              formData.host.trim() && formData.port
                ? [
                    everyLabel(formData.interval_seconds),
                    <PlanTarget key="t">
                      TCP {formData.host.trim()}:{formData.port}
                    </PlanTarget>,
                    formData.use_tls && 'with TLS',
                    downAfterLabel(formData.consecutive_failures_threshold),
                  ]
                : []
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
