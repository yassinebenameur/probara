'use client';

import { useRef, useState } from 'react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest, SIPMonitorConfig, DependencySuppression, NotificationMode, ChannelAssignment } from '@/lib/types';
import { buildSipConfig } from '@/lib/sip-config';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import { AlertingSection } from './AlertingSection';
import ScheduleSection from './ScheduleSection';
import DependenciesField from './DependenciesField';
import { FieldHint, FormPanel, LabelsSection, PlanSummary, PlanTarget, ToggleRow, downAfterLabel, everyLabel, revealFirstError, reportConstraintViolation, useClearErrorsOnEdit, useFollowEnabled } from './form-kit';

interface SipFormProps {
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

export default function SipForm({
  monitor,
  initialData,
  onSubmit,
  onCancel,
  loading = false,
}: SipFormProps) {
  const isEditMode = Boolean(monitor);
  const initialSipConfig =
    !isEditMode && initialData?.type === 'sip' ? (initialData.config as SIPMonitorConfig) : undefined;

  const editSipConfig =
    monitor && monitor.type === 'sip' ? (monitor.config as SIPMonitorConfig) : undefined;

  // The API accepts transport in any case and port 0 (5060, or 5061 over
  // TLS); normalize both so the select matches and a save keeps the port the
  // worker actually dials.
  const initialTransport = (
    (editSipConfig?.transport || initialSipConfig?.transport || 'udp').toLowerCase()
  ) as 'udp' | 'tcp' | 'tls';

  const [formData, setFormData] = useState({
    name: monitor?.name || initialData?.name || '',
    host: editSipConfig?.host || initialSipConfig?.host || '',
    port: editSipConfig?.port || initialSipConfig?.port || (initialTransport === 'tls' ? 5061 : 5060),
    transport: initialTransport,
    method: (editSipConfig?.method || initialSipConfig?.method || 'options') as 'options' | 'register',
    username: editSipConfig?.username || initialSipConfig?.username || '',
    password: editSipConfig?.password || initialSipConfig?.password || '',
    domain: editSipConfig?.domain || initialSipConfig?.domain || '',
    tls_skip_verify: editSipConfig?.tls_skip_verify ?? initialSipConfig?.tls_skip_verify ?? false,
    tls_server_name: editSipConfig?.tls_server_name || initialSipConfig?.tls_server_name || '',
    expected_status: editSipConfig?.expected_status?.toString()
      || initialSipConfig?.expected_status?.toString() || '',
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
    if ((formData.username.trim() === '') !== (formData.password === '')) {
      newErrors.username = 'Username and password must be provided together';
    }

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      revealFirstError(formRef.current);
      return;
    }

    // Built on top of the loaded config so keys this form does not render
    // survive the save (an update replaces the whole stored config).
    const config = buildSipConfig(editSipConfig ?? initialSipConfig, formData);

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = {
      name: formData.name.trim(),
      type: 'sip',
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
            placeholder="My SIP server"
            className="input"
          />
        </FormField>

        <FormField label="Host" required error={errors.host}>
          {(control) => (
            <>
              <input
                {...control}
                type="text"
                value={formData.host}
                onChange={(e) => setFormData({ ...formData, host: e.target.value })}
                placeholder="sip.example.com"
                className="input font-mono"
              />
              <FieldHint>Hostname or IP</FieldHint>
            </>
          )}
        </FormField>

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <FormField label="Port" required error={errors.port}>
            {(control) => (
              <>
                <input
                  {...control}
                  type="number"
                  value={formData.port}
                  onChange={(e) => setFormData({ ...formData, port: parseInt(e.target.value) || 5060 })}
                  min={1}
                  max={65535}
                  className="input tabular-nums"
                />
                <FieldHint>Default: 5060 (5061 for TLS)</FieldHint>
              </>
            )}
          </FormField>
          <FormField label="Transport">
            <select
              value={formData.transport}
              onChange={(e) => {
                const transport = e.target.value as 'udp' | 'tcp' | 'tls';
                setFormData({
                  ...formData,
                  transport,
                  // Track the conventional default port unless the user set a custom one.
                  port: transport === 'tls' && formData.port === 5060
                    ? 5061
                    : transport !== 'tls' && formData.port === 5061
                    ? 5060
                    : formData.port,
                });
              }}
              className="input"
            >
              <option value="udp">UDP</option>
              <option value="tcp">TCP</option>
              <option value="tls">TLS</option>
            </select>
          </FormField>
        </div>

        {formData.transport === 'tls' && (
          <>
            <ToggleRow
              title="Skip certificate verification"
              description="For lab servers with self-signed certificates"
              checked={formData.tls_skip_verify}
              onChange={(v) => setFormData({ ...formData, tls_skip_verify: v })}
              tone="warning"
            />
            <FormField label="TLS server name (SNI)">
              {(control) => (
                <>
                  <input
                    {...control}
                    type="text"
                    value={formData.tls_server_name}
                    onChange={(e) => setFormData({ ...formData, tls_server_name: e.target.value })}
                    placeholder="sip.example.com"
                    className="input font-mono"
                  />
                  <FieldHint>Overrides the name used for SNI and certificate verification. Defaults to the host.</FieldHint>
                </>
              )}
            </FormField>
          </>
        )}
      </FormSection>

      <FormSection title="Check">
        <FormField label="Method">
          {(control) => (
            <>
              <select
                {...control}
                value={formData.method}
                onChange={(e) => setFormData({ ...formData, method: e.target.value as 'options' | 'register' })}
                className="input"
              >
                <option value="options">OPTIONS: server availability ping</option>
                <option value="register">REGISTER: registrar and authentication probe</option>
              </select>
              <FieldHint>
                {formData.method === 'register'
                  ? 'Sends a query-style REGISTER (no Contact) that exercises the registrar and its digest authentication without creating or removing any bindings.'
                  : 'Sends a SIP OPTIONS request to test server availability without initiating a call. Answers digest challenges when credentials are configured.'}
              </FieldHint>
            </>
          )}
        </FormField>

        {formData.method === 'register' && (
          <FormField label="SIP domain">
            {(control) => (
              <>
                <input
                  {...control}
                  type="text"
                  value={formData.domain}
                  onChange={(e) => setFormData({ ...formData, domain: e.target.value })}
                  placeholder="example.com"
                  className="input font-mono"
                />
                <FieldHint>Domain of the address-of-record. Defaults to the host.</FieldHint>
              </>
            )}
          </FormField>
        )}

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <FormField label="Username" error={errors.username}>
            {(control) => (
              <>
                <input
                  {...control}
                  type="text"
                  value={formData.username}
                  onChange={(e) => setFormData({ ...formData, username: e.target.value })}
                  placeholder="agent42"
                  autoComplete="off"
                  className="input"
                />
                <FieldHint>Optional digest auth user</FieldHint>
              </>
            )}
          </FormField>
          <FormField label="Password">
            {(control) => (
              <>
                <input
                  {...control}
                  type="password"
                  value={formData.password}
                  onChange={(e) => setFormData({ ...formData, password: e.target.value })}
                  autoComplete="new-password"
                  className="input"
                />
                <FieldHint>{isEditMode ? 'Leave *** to keep the stored password' : 'Stored encrypted'}</FieldHint>
              </>
            )}
          </FormField>
        </div>

        <FormField label="Expected status code">
          {(control) => (
            <>
              <input
                {...control}
                type="number"
                value={formData.expected_status}
                onChange={(e) => setFormData({ ...formData, expected_status: e.target.value })}
                placeholder="200"
                min={100}
                max={699}
                className="input !w-32 tabular-nums"
              />
              <FieldHint>Default: 200 OK</FieldHint>
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
        enabledDescription={`Run SIP ${formData.method === 'register' ? 'REGISTER' : 'OPTIONS'} checks on this schedule`}
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
        placeholder="production, voip, critical"
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
                      SIP {formData.method.toUpperCase()} {formData.host.trim()}:{formData.port}
                    </PlanTarget>,
                    `over ${formData.transport.toUpperCase()}`,
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
