'use client';

import { useState } from 'react';
import {
  Monitor,
  CreateMonitorRequest,
  UpdateMonitorRequest,
  MonitorType,
  NotificationMode,
  ChannelAssignment,
  RedisMonitorConfig,
  PostgresMonitorConfig,
  MongoDBMonitorConfig,
  MASKED_SECRET,
} from '@/lib/types';
import { ChevronDown, ChevronRight, Eye, EyeOff, KeyRound, ShieldCheck } from 'lucide-react';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import { AlertingSection } from './AlertingSection';

export type DatabaseMonitorType = 'redis' | 'postgres' | 'mongodb';

type DatabaseConfig = RedisMonitorConfig & PostgresMonitorConfig & MongoDBMonitorConfig;

type ConnectionMode = 'fields' | 'connection_string';

const DB_TYPE_META: Record<
  DatabaseMonitorType,
  {
    label: string;
    defaultPort: number;
    csPlaceholder: string;
    csHint: string;
    supportsTLSToggle: boolean;
  }
> = {
  redis: {
    label: 'Redis',
    defaultPort: 6379,
    csPlaceholder: 'redis://user:password@redis.internal:6379/0',
    csHint: 'redis:// or rediss:// (TLS) URI. May include username, password, and DB index.',
    supportsTLSToggle: true,
  },
  postgres: {
    label: 'PostgreSQL',
    defaultPort: 5432,
    csPlaceholder: 'postgres://user:password@db.internal:5432/mydb?sslmode=require',
    csHint: 'postgres:// URI or key=value DSN. TLS is controlled by sslmode in the string.',
    supportsTLSToggle: false,
  },
  mongodb: {
    label: 'MongoDB',
    defaultPort: 27017,
    csPlaceholder: 'mongodb://user:password@mongo.internal:27017/?authSource=admin',
    csHint: 'mongodb:// or mongodb+srv:// URI. May include credentials and options like tls=true.',
    supportsTLSToggle: true,
  },
};

// Write-only secret input: the API never returns stored secrets (only "***"),
// so in edit mode an untouched blank field means "keep the current value".
function SecretInput({
  value,
  onChange,
  hasStored,
  placeholder,
  storedPlaceholder,
  mono = false,
  error,
}: {
  value: string;
  onChange: (value: string) => void;
  hasStored: boolean;
  placeholder: string;
  storedPlaceholder: string;
  mono?: boolean;
  error?: string;
}) {
  const [reveal, setReveal] = useState(false);
  return (
    <div>
      <div className="relative">
        <input
          type={reveal ? 'text' : 'password'}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={hasStored ? storedPlaceholder : placeholder}
          autoComplete="new-password"
          className={`input pr-16 ${mono ? 'font-mono text-xs' : ''}`}
        />
        <div className="absolute right-2 top-1/2 flex -translate-y-1/2 items-center gap-1.5">
          {hasStored && !value && (
            <span className="flex items-center gap-1 rounded-md bg-cyan-500/10 px-1.5 py-0.5 text-[10px] font-medium text-cyan-400" title="A value is stored. Leave blank to keep it.">
              <KeyRound className="h-3 w-3" strokeWidth={1.75} />
              set
            </span>
          )}
          <button
            type="button"
            onClick={() => setReveal(!reveal)}
            className="rounded p-1 text-slate-500 transition-colors hover:text-slate-300"
            aria-label={reveal ? 'Hide value' : 'Show value'}
            tabIndex={-1}
          >
            {reveal ? <EyeOff className="h-3.5 w-3.5" strokeWidth={1.75} /> : <Eye className="h-3.5 w-3.5" strokeWidth={1.75} />}
          </button>
        </div>
      </div>
      {error && <p className="mt-1 text-xs text-rose-400">{error}</p>}
    </div>
  );
}

function MiniToggle({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label: string }) {
  return (
    <button
      type="button"
      onClick={() => onChange(!checked)}
      className={`relative h-5 w-9 rounded-full transition-colors ${checked ? 'bg-cyan-500' : 'bg-slate-700'}`}
      aria-pressed={checked}
      aria-label={label}
    >
      <span className={`absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white transition-transform ${checked ? 'translate-x-4' : ''}`} />
    </button>
  );
}

function ToggleRow({
  title,
  description,
  checked,
  onChange,
}: {
  title: string;
  description: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3">
      <div>
        <p className="text-sm font-medium text-white">{title}</p>
        <p className="text-xs text-slate-500">{description}</p>
      </div>
      <MiniToggle checked={checked} onChange={onChange} label={title} />
    </div>
  );
}

interface DatabaseFormProps {
  type: DatabaseMonitorType;
  monitor?: Monitor;
  initialData?: CreateMonitorRequest;
  onSubmit: (data: CreateMonitorRequest | UpdateMonitorRequest) => Promise<void>;
  onCancel?: () => void;
  loading?: boolean;
}

export default function DatabaseForm({
  type,
  monitor,
  initialData,
  onSubmit,
  onCancel,
  loading = false,
}: DatabaseFormProps) {
  const meta = DB_TYPE_META[type];
  const isEditMode = Boolean(monitor);
  const existingConfig =
    monitor && monitor.type === type
      ? ((monitor.config || {}) as DatabaseConfig)
      : !isEditMode && initialData?.type === type
        ? ((initialData.config || {}) as DatabaseConfig)
        : ({} as DatabaseConfig);

  // The API masks stored secrets as "***" — that tells us one exists without
  // revealing it. Cloned monitors carry the mask too, but a clone can't reuse
  // another monitor's secret, so treat it as unset there.
  const hasStoredPassword = isEditMode && existingConfig.password === MASKED_SECRET;
  const hasStoredConnString = isEditMode && existingConfig.connection_string === MASKED_SECRET;
  const hasStoredClientKey = isEditMode && existingConfig.tls_client_key_pem === MASKED_SECRET;

  const [formData, setFormData] = useState({
    name: monitor?.name || initialData?.name || '',
    connection_mode: (existingConfig.connection_string ? 'connection_string' : 'fields') as ConnectionMode,
    connection_string: '',
    host: existingConfig.host || '',
    port: existingConfig.port || meta.defaultPort,
    username: existingConfig.username || '',
    password: '',
    database: existingConfig.database || '',
    ssl_mode: existingConfig.ssl_mode || ('' as '' | 'disable' | 'require' | 'verify-full'),
    auth_source: existingConfig.auth_source || '',
    db_index: existingConfig.db ?? 0,
    tls_enabled: existingConfig.tls_enabled ?? false,
    tls_skip_verify: existingConfig.tls_skip_verify ?? false,
    tls_ca_pem: existingConfig.tls_ca_pem || '',
    tls_client_cert_pem: existingConfig.tls_client_cert_pem || '',
    tls_client_key_pem: '',
    query: existingConfig.query || '',
    max_latency_ms: existingConfig.max_latency_ms ? String(existingConfig.max_latency_ms) : '',
    interval_seconds: monitor?.interval_seconds || initialData?.interval_seconds || 60,
    timeout_seconds: monitor?.timeout_seconds || initialData?.timeout_seconds || 10,
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
    consecutive_failures_threshold: monitor?.consecutive_failures_threshold ?? 2,
    notification_mode: (monitor?.notification_mode ?? 'default') as NotificationMode,
    notification_channels: monitor?.notification_channels ?? ([] as ChannelAssignment[]),
  });

  const [errors, setErrors] = useState<Record<string, string>>({});
  const [certsOpen, setCertsOpen] = useState(
    Boolean(formData.tls_ca_pem || formData.tls_client_cert_pem || hasStoredClientKey)
  );

  const usingConnString = formData.connection_mode === 'connection_string';
  // PEM material only makes sense when the connection can use TLS.
  const tlsMaterialRelevant = usingConnString
    ? true
    : type === 'postgres'
      ? formData.ssl_mode !== 'disable'
      : formData.tls_enabled;
  const hasTLSMaterial = Boolean(
    formData.tls_ca_pem.trim() || formData.tls_client_cert_pem.trim() || formData.tls_client_key_pem.trim() || hasStoredClientKey
  );

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setErrors({});

    const newErrors: Record<string, string> = {};
    if (!formData.name.trim()) newErrors.name = 'Name is required';
    if (usingConnString) {
      if (!formData.connection_string.trim() && !hasStoredConnString) {
        newErrors.connection_string = 'Connection string is required';
      }
    } else {
      if (!formData.host.trim()) newErrors.host = 'Host is required';
      if (formData.port < 1 || formData.port > 65535) newErrors.port = 'Port must be between 1 and 65535';
      if (type === 'postgres' && !formData.username.trim()) newErrors.username = 'Username is required';
      if (type === 'mongodb' && formData.username.trim() && !formData.password && !hasStoredPassword) {
        newErrors.password = 'Password is required when a username is set';
      }
    }
    const certProvided = formData.tls_client_cert_pem.trim() !== '';
    const keyProvided = formData.tls_client_key_pem.trim() !== '' || hasStoredClientKey;
    if (tlsMaterialRelevant && certProvided && !keyProvided) {
      newErrors.tls_client_key_pem = 'Client key is required alongside a client certificate';
    }
    const maxLatency = formData.max_latency_ms.trim() ? parseInt(formData.max_latency_ms, 10) : undefined;
    if (formData.max_latency_ms.trim() && (!Number.isFinite(maxLatency) || (maxLatency as number) <= 0)) {
      newErrors.max_latency_ms = 'Must be a positive number of milliseconds';
    }
    if (formData.timeout_seconds >= formData.interval_seconds) {
      newErrors.timeout_seconds = 'Timeout must be less than interval';
    }

    if (Object.keys(newErrors).length > 0) {
      setErrors(newErrors);
      return;
    }

    const config: DatabaseConfig = {};

    if (usingConnString) {
      // Blank + previously stored = keep the stored secret (write-only).
      config.connection_string = formData.connection_string.trim() || MASKED_SECRET;
      if (meta.supportsTLSToggle && formData.tls_skip_verify) config.tls_skip_verify = true;
    } else {
      config.host = formData.host.trim();
      config.port = formData.port;
      if (formData.username.trim()) config.username = formData.username.trim();
      if (formData.password) {
        config.password = formData.password;
      } else if (hasStoredPassword) {
        config.password = MASKED_SECRET;
      }
      if (type === 'redis' && formData.db_index > 0) config.db = formData.db_index;
      if (type === 'postgres') {
        if (formData.database.trim()) config.database = formData.database.trim();
        if (formData.ssl_mode) config.ssl_mode = formData.ssl_mode;
      }
      if (type === 'mongodb' && formData.auth_source.trim()) config.auth_source = formData.auth_source.trim();
      if (meta.supportsTLSToggle && formData.tls_enabled) {
        config.tls_enabled = true;
        if (formData.tls_skip_verify) config.tls_skip_verify = true;
      }
    }

    if (tlsMaterialRelevant) {
      if (formData.tls_ca_pem.trim()) config.tls_ca_pem = formData.tls_ca_pem.trim();
      if (certProvided) {
        config.tls_client_cert_pem = formData.tls_client_cert_pem.trim();
        // Blank + previously stored = keep the stored key (write-only).
        config.tls_client_key_pem = formData.tls_client_key_pem.trim() || MASKED_SECRET;
      }
    }

    if (type === 'postgres' && formData.query.trim()) config.query = formData.query.trim();
    if (maxLatency) config.max_latency_ms = maxLatency;

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = {
      name: formData.name.trim(),
      type: type as MonitorType,
      config,
      interval_seconds: formData.interval_seconds,
      timeout_seconds: formData.timeout_seconds,
      enabled: formData.enabled,
    };

    requestData.consecutive_failures_threshold = formData.consecutive_failures_threshold;
    requestData.notification_mode = formData.notification_mode;
    requestData.notification_channels = formData.notification_mode === 'custom' ? formData.notification_channels : [];
    if (formData.tags.trim()) {
      requestData.tags = formData.tags.split(',').map((t) => t.trim()).filter((t) => t);
    }

    await onSubmit(requestData);
  };

  const summary = (() => {
    const target = usingConnString
      ? formData.connection_string.trim() || hasStoredConnString
        ? `${meta.label} via connection string`
        : null
      : formData.host.trim()
        ? `${meta.label} ${formData.host.trim()}:${formData.port}${formData.tls_enabled ? ' (TLS)' : ''}`
        : null;
    if (!target) return undefined;
    const check = type === 'postgres' && formData.query.trim() ? 'run query' : 'connect + ping';
    return `Every ${formData.interval_seconds}s · ${check} ${target} · down after ${formData.consecutive_failures_threshold} failed check${formData.consecutive_failures_threshold === 1 ? '' : 's'}`;
  })();

  return (
    <form onSubmit={handleSubmit} className="space-y-5">
      <FormSection title="Connection">
        <FormField label="Monitor name" required error={errors.name}>
          <input
            type="text"
            value={formData.name}
            onChange={(e) => setFormData({ ...formData, name: e.target.value })}
            placeholder={`${meta.label} production`}
            className="input"
          />
        </FormField>

        <div>
          <div className="mb-2 flex rounded-lg border border-white/[0.08] bg-slate-900/60 p-0.5" role="radiogroup" aria-label="Connection mode">
            {(
              [
                { mode: 'fields' as ConnectionMode, label: 'Form fields' },
                { mode: 'connection_string' as ConnectionMode, label: 'Connection string' },
              ]
            ).map(({ mode, label }) => (
              <button
                key={mode}
                type="button"
                role="radio"
                aria-checked={formData.connection_mode === mode}
                onClick={() => setFormData({ ...formData, connection_mode: mode })}
                className={`flex-1 rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                  formData.connection_mode === mode
                    ? 'bg-cyan-500/15 text-cyan-300'
                    : 'text-slate-400 hover:text-slate-200'
                }`}
              >
                {label}
              </button>
            ))}
          </div>
          <p className="text-xs text-slate-500">
            {usingConnString
              ? 'Paste a full connection URI. It is stored encrypted and never shown again.'
              : 'Enter connection details field by field. The password is stored encrypted and never shown again.'}
          </p>
        </div>

        {usingConnString ? (
          <>
            <FormField label="Connection string" required={!hasStoredConnString} error={errors.connection_string} description={meta.csHint}>
              <SecretInput
                value={formData.connection_string}
                onChange={(v) => setFormData({ ...formData, connection_string: v })}
                hasStored={hasStoredConnString}
                placeholder={meta.csPlaceholder}
                storedPlaceholder="Unchanged — paste a new connection string to replace"
                mono
              />
            </FormField>
            {meta.supportsTLSToggle && (
              <ToggleRow
                title="Skip TLS certificate verification"
                description="Accept self-signed or mismatched certificates when the URI enables TLS"
                checked={formData.tls_skip_verify}
                onChange={(v) => setFormData({ ...formData, tls_skip_verify: v })}
              />
            )}
          </>
        ) : (
          <>
            <div className="grid grid-cols-[1fr_120px] gap-4">
              <FormField label="Host" required error={errors.host} description={`${meta.label} hostname or IP`}>
                <input
                  type="text"
                  value={formData.host}
                  onChange={(e) => setFormData({ ...formData, host: e.target.value })}
                  placeholder={`${type === 'postgres' ? 'db' : type === 'redis' ? 'redis' : 'mongo'}.internal`}
                  className="input"
                />
              </FormField>
              <FormField label="Port" error={errors.port} description={`Default: ${meta.defaultPort}`}>
                <input
                  type="number"
                  value={formData.port}
                  onChange={(e) => setFormData({ ...formData, port: parseInt(e.target.value, 10) || meta.defaultPort })}
                  min={1}
                  max={65535}
                  className="input"
                />
              </FormField>
            </div>

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <FormField
                label={type === 'postgres' ? 'Username' : 'Username (optional)'}
                required={type === 'postgres'}
                error={errors.username}
                description={type === 'redis' ? 'ACL user — leave blank for the default user' : undefined}
              >
                <input
                  type="text"
                  value={formData.username}
                  onChange={(e) => setFormData({ ...formData, username: e.target.value })}
                  placeholder={type === 'postgres' ? 'monitoring' : ''}
                  autoComplete="off"
                  className="input"
                />
              </FormField>
              <FormField
                label="Password"
                error={errors.password}
                description={hasStoredPassword ? 'Leave blank to keep the current password' : 'Stored encrypted, never displayed'}
              >
                <SecretInput
                  value={formData.password}
                  onChange={(v) => setFormData({ ...formData, password: v })}
                  hasStored={hasStoredPassword}
                  placeholder="••••••••"
                  storedPlaceholder="Unchanged"
                />
              </FormField>
            </div>

            {type === 'redis' && (
              <FormField label="Database index" description="0–15, default 0">
                <input
                  type="number"
                  value={formData.db_index}
                  onChange={(e) => setFormData({ ...formData, db_index: parseInt(e.target.value, 10) || 0 })}
                  min={0}
                  max={15}
                  className="input"
                />
              </FormField>
            )}

            {type === 'postgres' && (
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <FormField label="Database" description="Default: postgres">
                  <input
                    type="text"
                    value={formData.database}
                    onChange={(e) => setFormData({ ...formData, database: e.target.value })}
                    placeholder="postgres"
                    className="input"
                  />
                </FormField>
                <FormField label="TLS (sslmode)" description="How to negotiate TLS">
                  <select
                    value={formData.ssl_mode}
                    onChange={(e) => setFormData({ ...formData, ssl_mode: e.target.value as typeof formData.ssl_mode })}
                    className="input"
                  >
                    <option value="">Prefer (TLS if available)</option>
                    <option value="disable">Disable</option>
                    <option value="require">Require</option>
                    <option value="verify-full">Require + verify certificate</option>
                  </select>
                </FormField>
              </div>
            )}

            {type === 'mongodb' && (
              <FormField label="Auth source" description="Database to authenticate against. Default: admin">
                <input
                  type="text"
                  value={formData.auth_source}
                  onChange={(e) => setFormData({ ...formData, auth_source: e.target.value })}
                  placeholder="admin"
                  className="input"
                />
              </FormField>
            )}

            {meta.supportsTLSToggle && (
              <div className="space-y-2">
                <ToggleRow
                  title="Use TLS"
                  description={`Connect to ${meta.label} over an encrypted connection`}
                  checked={formData.tls_enabled}
                  onChange={(v) => setFormData({ ...formData, tls_enabled: v, tls_skip_verify: v ? formData.tls_skip_verify : false })}
                />
                {formData.tls_enabled && (
                  <ToggleRow
                    title="Skip certificate verification"
                    description="Accept self-signed or mismatched certificates"
                    checked={formData.tls_skip_verify}
                    onChange={(v) => setFormData({ ...formData, tls_skip_verify: v })}
                  />
                )}
              </div>
            )}
          </>
        )}

        {tlsMaterialRelevant && (
          <div className="rounded-lg border border-white/[0.06] bg-slate-900/40">
            <button
              type="button"
              onClick={() => setCertsOpen(!certsOpen)}
              className="flex w-full items-center justify-between px-4 py-3 text-left"
              aria-expanded={certsOpen}
            >
              <span className="flex items-center gap-2 text-sm font-medium text-white">
                <ShieldCheck className="h-4 w-4 text-slate-400" strokeWidth={1.75} />
                Custom certificates
                <span className="text-xs font-normal text-slate-500">private CA / mutual TLS</span>
                {hasTLSMaterial && (
                  <span className="rounded-md bg-cyan-500/10 px-1.5 py-0.5 text-[10px] font-medium text-cyan-400">configured</span>
                )}
              </span>
              {certsOpen ? (
                <ChevronDown className="h-4 w-4 text-slate-500" strokeWidth={1.75} />
              ) : (
                <ChevronRight className="h-4 w-4 text-slate-500" strokeWidth={1.75} />
              )}
            </button>
            {certsOpen && (
              <div className="space-y-4 border-t border-white/[0.06] px-4 py-4">
                <FormField
                  label="CA certificate (PEM, optional)"
                  description="Verify a server certificate signed by a private or internal CA"
                >
                  <textarea
                    rows={4}
                    value={formData.tls_ca_pem}
                    onChange={(e) => setFormData({ ...formData, tls_ca_pem: e.target.value })}
                    placeholder={'-----BEGIN CERTIFICATE-----\n…\n-----END CERTIFICATE-----'}
                    spellCheck={false}
                    className="input min-h-[88px] font-mono text-xs"
                  />
                </FormField>
                <FormField
                  label="Client certificate (PEM, optional)"
                  description="Only for mutual TLS / X.509 authentication"
                >
                  <textarea
                    rows={4}
                    value={formData.tls_client_cert_pem}
                    onChange={(e) => setFormData({ ...formData, tls_client_cert_pem: e.target.value })}
                    placeholder={'-----BEGIN CERTIFICATE-----\n…\n-----END CERTIFICATE-----'}
                    spellCheck={false}
                    className="input min-h-[88px] font-mono text-xs"
                  />
                </FormField>
                {(formData.tls_client_cert_pem.trim() !== '' || formData.tls_client_key_pem !== '' || hasStoredClientKey) && (
                  <FormField
                    label="Client key (PEM)"
                    error={errors.tls_client_key_pem}
                    description={
                      hasStoredClientKey
                        ? 'Stored encrypted — leave blank to keep the current key'
                        : 'Stored encrypted, never displayed again'
                    }
                  >
                    <textarea
                      rows={4}
                      value={formData.tls_client_key_pem}
                      onChange={(e) => setFormData({ ...formData, tls_client_key_pem: e.target.value })}
                      placeholder={hasStoredClientKey ? 'Unchanged — paste a new key to replace' : '-----BEGIN PRIVATE KEY-----\n…\n-----END PRIVATE KEY-----'}
                      spellCheck={false}
                      className="input min-h-[88px] font-mono text-xs"
                    />
                  </FormField>
                )}
              </div>
            )}
          </div>
        )}
      </FormSection>

      <FormSection title="Checks">
        {type === 'postgres' && (
          <FormField
            label="Assertion query (optional)"
            error={errors.query}
            description="Runs after connecting; the check fails if it errors or returns 0 rows. Example: SELECT 1"
          >
            <input
              type="text"
              value={formData.query}
              onChange={(e) => setFormData({ ...formData, query: e.target.value })}
              placeholder="SELECT 1"
              className="input font-mono text-xs"
            />
          </FormField>
        )}
        <FormField
          label="Max latency (ms, optional)"
          error={errors.max_latency_ms}
          description="Fail the check if the round trip takes longer than this"
        >
          <input
            type="number"
            value={formData.max_latency_ms}
            onChange={(e) => setFormData({ ...formData, max_latency_ms: e.target.value })}
            min={1}
            placeholder="e.g. 250"
            className="input"
          />
        </FormField>
      </FormSection>

      <FormSection title="Schedule">
        <div className="grid grid-cols-2 gap-4">
          <FormField label="Interval (seconds)" description="How often to check">
            <input
              type="number"
              value={formData.interval_seconds}
              onChange={(e) => setFormData({ ...formData, interval_seconds: parseInt(e.target.value, 10) || 60 })}
              min={10}
              step={5}
              className="input"
            />
          </FormField>
          <FormField label="Timeout (seconds)" error={errors.timeout_seconds} description="Must be less than interval">
            <input
              type="number"
              value={formData.timeout_seconds}
              onChange={(e) => setFormData({ ...formData, timeout_seconds: parseInt(e.target.value, 10) || 10 })}
              min={1}
              className="input"
            />
          </FormField>
        </div>
      </FormSection>

      <FormSection title="Alerting">
        <AlertingSection
          isGroup={false}
          intervalSeconds={formData.interval_seconds}
          threshold={formData.consecutive_failures_threshold}
          onThresholdChange={(n) => setFormData({ ...formData, consecutive_failures_threshold: n })}
          mode={formData.notification_mode}
          onModeChange={(m) => setFormData({ ...formData, notification_mode: m })}
          customChannels={formData.notification_channels}
          onCustomChannelsChange={(next) => setFormData({ ...formData, notification_channels: next })}
        />
      </FormSection>

      <FormSection title="Meta">
        <FormField label="Tags" description={`Comma-separated, e.g. production, ${type}, critical`}>
          <input
            type="text"
            value={formData.tags}
            onChange={(e) => setFormData({ ...formData, tags: e.target.value })}
            placeholder={`production, ${type}, critical`}
            className="input"
          />
        </FormField>

        <ToggleRow
          title="Monitor enabled"
          description={`Run ${meta.label} checks on schedule`}
          checked={formData.enabled}
          onChange={(v) => setFormData({ ...formData, enabled: v })}
        />
      </FormSection>

      <FormActions
        middle={summary}
        cancel={onCancel ? { label: 'Cancel', onClick: onCancel, disabled: loading } : undefined}
        submit={{
          label: loading ? 'Saving…' : isEditMode ? 'Update monitor' : 'Create monitor',
          loading,
          disabled: loading,
          type: 'submit',
        }}
      />
    </form>
  );
}
