'use client';

import { useRef, useState } from 'react';
import {
  Monitor,
  CreateMonitorRequest,
  UpdateMonitorRequest,
  GRPCMonitorConfig,
  DependencySuppression,
  NotificationMode,
  ChannelAssignment,
} from '@/lib/types';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import { AlertingSection } from './AlertingSection';
import ScheduleSection from './ScheduleSection';
import DependenciesField from './DependenciesField';
import { FieldHint, FormPanel, LabelsSection, PlanSummary, PlanTarget, ToggleRow, downAfterLabel, everyLabel, revealFirstError, reportConstraintViolation, useClearErrorsOnEdit, useFollowEnabled } from './form-kit';

interface GrpcFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

export default function GrpcForm({
  monitor,
  initialData,
  onSubmit,
  onCancel,
  loading = false,
}: GrpcFormProps) {
  const isEditMode = Boolean(monitor);
  const initialGrpcConfig =
    !isEditMode && initialData?.type === 'grpc' ? (initialData.config as GRPCMonitorConfig) : undefined;

  const [formData, setFormData] = useState({
    name: monitor?.name || initialData?.name || '',
    host: monitor && monitor.type === 'grpc'
      ? (monitor.config as GRPCMonitorConfig)?.host || ''
      : initialGrpcConfig?.host || '',
    port: monitor && monitor.type === 'grpc'
      ? (monitor.config as GRPCMonitorConfig)?.port || 443
      : initialGrpcConfig?.port || 443,
    service: monitor && monitor.type === 'grpc'
      ? (monitor.config as GRPCMonitorConfig)?.service || ''
      : initialGrpcConfig?.service || '',
    use_tls: monitor && monitor.type === 'grpc'
      ? (monitor.config as GRPCMonitorConfig)?.use_tls ?? true
      : initialGrpcConfig?.use_tls ?? true,
    interval_seconds: monitor?.interval_seconds || initialData?.interval_seconds || 60,
    timeout_seconds: monitor?.timeout_seconds || initialData?.timeout_seconds || 10,
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
    consecutive_failures_threshold: monitor?.consecutive_failures_threshold ?? 2,
    notification_mode: (monitor?.notification_mode ?? 'default') as NotificationMode,
    dependency_suppression: (monitor?.dependency_suppression ?? 'inherit') as DependencySuppression,
    notification_channels: monitor?.notification_channels ?? [] as ChannelAssignment[],
    location_ids: monitor?.location_ids ?? initialData?.location_ids ?? ([] as string[]),
    location_quorum: monitor?.location_quorum ?? initialData?.location_quorum ?? 1,
    depends_on_ids: monitor?.depends_on_ids ?? initialData?.depends_on_ids ?? ([] as string[]),
  });

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

    const config: GRPCMonitorConfig = {
      host: formData.host.trim(),
      port: formData.port,
      use_tls: formData.use_tls,
    };

    if (formData.service.trim()) {
      config.service = formData.service.trim();
    }

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = {
      name: formData.name.trim(),
      type: 'grpc',
      config,
      interval_seconds: formData.interval_seconds,
      timeout_seconds: formData.timeout_seconds,
      enabled: formData.enabled,
    };

    requestData.consecutive_failures_threshold = formData.consecutive_failures_threshold;
    requestData.notification_mode = formData.notification_mode;
    requestData.dependency_suppression = formData.dependency_suppression;
    requestData.notification_channels = formData.notification_mode === 'custom' ? formData.notification_channels : [];
    if (formData.tags.trim()) {
      requestData.tags = formData.tags.split(',').map(t => t.trim()).filter(t => t);
    }
    requestData.depends_on_ids = formData.depends_on_ids;
    requestData.location_ids = formData.location_ids;
    requestData.location_quorum =
      formData.location_ids.length >= 2
        ? Math.min(formData.location_quorum, formData.location_ids.length)
        : 1;

    await onSubmit(requestData);
  };

  return (
    <form ref={formRef} onSubmit={handleSubmit} noValidate className="space-y-5">
      <FormSection title="Target">
        <FormField label="Monitor name" required error={errors.name}>
          <input
            type="text"
            value={formData.name}
            onChange={(e) => setFormData({ ...formData, name: e.target.value })}
            placeholder="My gRPC service"
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
                    placeholder="grpc.example.com"
                    className="input font-mono"
                  />
                  <FieldHint>gRPC server hostname or IP</FieldHint>
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
                  value={formData.port}
                  onChange={(e) => setFormData({ ...formData, port: parseInt(e.target.value, 10) || (formData.use_tls ? 443 : 80) })}
                  min={1}
                  max={65535}
                  className="input tabular-nums"
                />
                <FieldHint>Default: {formData.use_tls ? 443 : 80}</FieldHint>
              </>
            )}
          </FormField>
        </div>

        <ToggleRow
          title="TLS"
          description="Use TLS for the gRPC connection"
          checked={formData.use_tls}
          onChange={(nextUseTLS) => {
            let nextPort = formData.port;
            if (formData.port === 443 && !nextUseTLS) nextPort = 80;
            if (formData.port === 80 && nextUseTLS) nextPort = 443;
            setFormData({ ...formData, use_tls: nextUseTLS, port: nextPort });
          }}
        />

        <FormField label="Service">
          {(control) => (
            <>
              <input
                {...control}
                type="text"
                value={formData.service}
                onChange={(e) => setFormData({ ...formData, service: e.target.value })}
                placeholder="my.package.Service"
                className="input font-mono"
              />
              <FieldHint>Optional. Passed to grpc.health.v1.Health/Check</FieldHint>
            </>
          )}
        </FormField>
      </FormSection>

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
        enabledDescription="Run gRPC health checks on this schedule"
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
          onThresholdChange={(n) => setFormData({ ...formData, consecutive_failures_threshold: n })}
          mode={formData.notification_mode}
          onModeChange={(m) => setFormData({ ...formData, notification_mode: m })}
          dependencySuppression={formData.dependency_suppression}
          hasDependencies={formData.depends_on_ids.length > 0}
          onDependencySuppressionChange={(d) => setFormData({ ...formData, dependency_suppression: d })}
          customChannels={formData.notification_channels}
          onCustomChannelsChange={(next) => setFormData({ ...formData, notification_channels: next })}
        />
      </FormPanel>

      <LabelsSection
        tags={formData.tags}
        onTagsChange={(v) => setFormData({ ...formData, tags: v })}
        placeholder="production, grpc, critical"
      />

      <FormActions
        middle={
          <PlanSummary
            errors={errors}
            parts={
              formData.host.trim()
                ? [
                    everyLabel(formData.interval_seconds),
                    <PlanTarget key="t">
                      gRPC {formData.host.trim()}:{formData.port}
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
