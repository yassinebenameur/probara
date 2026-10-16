'use client';

import { useRef, useState } from 'react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest, SIPMonitorConfig, DependencySuppression, NotificationMode, ChannelAssignment } from '@/lib/types';
import { buildSipConfig, SipFormFields } from '@/lib/sip-config';
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

  // RFC 3263 resolution ("Locate server via DNS") leaves port and transport
  // to SRV when they are unset (0 and '').
  const initialResolution: SipFormFields['resolution'] =
    (editSipConfig?.resolution ?? initialSipConfig?.resolution) === 'rfc3263' ? 'rfc3263' : 'a';
  const initialRfc3263 = initialResolution === 'rfc3263';

  // The API accepts transport in any case and port 0 (5060, or 5061 over
  // TLS); normalize both so the select matches and a save keeps the port the
  // worker actually dials. Under rfc3263 an empty transport is Auto and port
  // 0 stays empty: SRV decides both.
  const storedTransport = (editSipConfig?.transport || initialSipConfig?.transport || '').toLowerCase();
  const initialTransport = (storedTransport || (initialRfc3263 ? 'auto' : 'udp')) as SipFormFields['transport'];

  const [formData, setFormData] = useState({
    name: monitor?.name || initialData?.name || '',
    host: editSipConfig?.host || initialSipConfig?.host || '',
    port:
      editSipConfig?.port || initialSipConfig?.port || (initialRfc3263 ? 0 : initialTransport === 'tls' ? 5061 : 5060),
    transport: initialTransport,
    resolution: initialResolution,
    nameserver: editSipConfig?.nameserver || initialSipConfig?.nameserver || '',
    method: (editSipConfig?.method || initialSipConfig?.method || 'options') as 'options' | 'register',
    username: editSipConfig?.username || initialSipConfig?.username || '',
    password: editSipConfig?.password || initialSipConfig?.password || '',
    domain: editSipConfig?.domain || initialSipConfig?.domain || '',
    tls_skip_verify: editSipConfig?.tls_skip_verify ?? initialSipConfig?.tls_skip_verify ?? false,
    tls_server_name: editSipConfig?.tls_server_name || initialSipConfig?.tls_server_name || '',
    tls_min_days_valid:
      (editSipConfig?.tls_min_days_valid ?? initialSipConfig?.tls_min_days_valid)?.toString() ?? '',
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
  const rfc3263 = formData.resolution === 'rfc3263';
  const defaultPort = formData.transport === 'tls' ? 5061 : 5060;

  const setLocateViaDNS = (on: boolean) =>
    setFormData((prev) => {
      const prevDefault = prev.transport === 'tls' ? 5061 : 5060;
      if (on) {
        // A port pins A/AAAA resolution, so the conventional default the
        // form pre-filled is cleared and SRV picks the port.
        return { ...prev, resolution: 'rfc3263', port: prev.port === prevDefault ? 0 : prev.port };
      }
      const transport = prev.transport === 'auto' ? 'udp' : prev.transport;
      return {
        ...prev,
        resolution: 'a',
        transport,
        port: prev.port || (transport === 'tls' ? 5061 : 5060),
      };
    });

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (reportConstraintViolation(formRef.current)) return;
    setErrors({});

    const newErrors: Record<string, string> = {};
    if (!formData.name.trim()) newErrors.name = 'Name is required';
    if (!formData.host.trim()) newErrors.host = 'Host is required';
    if (rfc3263 ? formData.port < 0 || formData.port > 65535 : formData.port < 1 || formData.port > 65535) {
      newErrors.port = 'Port must be between 1 and 65535';
    }
    if (formData.timeout_seconds >= formData.interval_seconds) {
      newErrors.timeout_seconds = 'Timeout must be less than interval';
    }
    if (formData.transport === 'tls' && formData.tls_min_days_valid.trim()) {
      const days = Number(formData.tls_min_days_valid);
      if (!Number.isInteger(days) || days < 0) {
        newErrors.tls_min_days_valid = 'Must be a whole number of days, 0 or more';
      }
    }
    if (rfc3263 && /\s/.test(formData.nameserver.trim())) {
      newErrors.nameserver = 'Enter a host or host:port';
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
                placeholder={rfc3263 ? 'example.com' : 'sip.example.com'}
                className="input font-mono"
              />
              <FieldHint>
                {rfc3263 ? "SIP domain whose SRV records name the servers, or a hostname or IP" : 'Hostname or IP'}
              </FieldHint>
            </>
          )}
        </FormField>

        <ToggleRow
          title="Locate server via DNS (SRV)"
          description="RFC 3263: find the servers from the domain's SRV records and fail over between them"
          checked={rfc3263}
          onChange={setLocateViaDNS}
        />

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <FormField label="Port" required={!rfc3263} error={errors.port}>
            {(control) => (
              <>
                <input
                  {...control}
                  type="number"
                  value={rfc3263 && formData.port === 0 ? '' : formData.port}
                  onChange={(e) =>
                    setFormData({ ...formData, port: parseInt(e.target.value) || (rfc3263 ? 0 : defaultPort) })
                  }
                  placeholder={rfc3263 ? 'from SRV' : undefined}
                  min={1}
                  max={65535}
                  className="input tabular-nums"
                />
                <FieldHint>
                  {rfc3263
                    ? 'Leave empty to use SRV; a port skips SRV and dials the host directly'
                    : 'Default: 5060 (5061 for TLS)'}
                </FieldHint>
              </>
            )}
          </FormField>
          <FormField label="Transport">
            <select
              value={formData.transport}
              onChange={(e) => {
                const transport = e.target.value as SipFormFields['transport'];
                setFormData({
                  ...formData,
                  transport,
                  // Track the conventional default port unless the user set a
                  // custom one. Under SRV an empty port stays empty.
                  port: rfc3263
                    ? formData.port
                    : transport === 'tls' && formData.port === 5060
                    ? 5061
                    : transport !== 'tls' && formData.port === 5061
                    ? 5060
                    : formData.port,
                });
              }}
              className="input"
            >
              {rfc3263 && <option value="auto">Auto (from SRV)</option>}
              <option value="udp">UDP</option>
              <option value="tcp">TCP</option>
              <option value="tls">TLS</option>
            </select>
          </FormField>
        </div>

        {rfc3263 && (
          <FormField label="DNS server" error={errors.nameserver}>
            {(control) => (
              <>
                <input
                  {...control}
                  type="text"
                  value={formData.nameserver}
                  onChange={(e) => setFormData({ ...formData, nameserver: e.target.value })}
                  placeholder="10.0.0.2 or resolver.internal:5353"
                  className="input font-mono"
                />
                <FieldHint>Optional. Defaults to the worker&apos;s resolver.</FieldHint>
              </>
            )}
          </FormField>
        )}

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
            <FormField label="Certificate valid for at least" error={errors.tls_min_days_valid}>
              {(control) => (
                <>
                  <span className="flex items-center gap-2">
                    <input
                      {...control}
                      type="number"
                      value={formData.tls_min_days_valid}
                      onChange={(e) => setFormData({ ...formData, tls_min_days_valid: e.target.value })}
                      placeholder="14"
                      min={0}
                      className="input !w-28 tabular-nums"
                    />
                    <span className="text-xs text-slate-500">days</span>
                  </span>
                  {!errors.tls_min_days_valid && (
                    <FieldHint>Open a certificate-expiry alert when fewer days remain; the check itself still passes</FieldHint>
                  )}
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
                      SIP {formData.method.toUpperCase()} {formData.host.trim()}
                      {formData.port ? `:${formData.port}` : ''}
                    </PlanTarget>,
                    ...(rfc3263 && !formData.port ? ['located via SRV'] : []),
                    formData.transport === 'auto' ? 'transport from SRV' : `over ${formData.transport.toUpperCase()}`,
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
