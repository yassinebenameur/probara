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
  RabbitMQMonitorConfig,
  MySQLMonitorConfig,
  DBQueryValueOp,
  MASKED_SECRET,
  Location,
} from '@/lib/types';
import { testMonitorConfig, TestMonitorConfigResponse } from '@/lib/api';
import {
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Eye,
  EyeOff,
  KeyRound,
  Loader2,
  PlugZap,
  ShieldCheck,
  X,
  XCircle,
} from 'lucide-react';
import FormField from '@/components/ui/FormField';
import FormSection from '@/components/ui/FormSection';
import FormActions from '@/components/ui/FormActions';
import { AlertingSection } from './AlertingSection';
import { LocationsSection } from './LocationsSection';

export type DatabaseMonitorType = 'redis' | 'postgres' | 'mongodb' | 'rabbitmq' | 'mysql';

type DatabaseConfig = RedisMonitorConfig &
  PostgresMonitorConfig &
  MongoDBMonitorConfig &
  RabbitMQMonitorConfig &
  MySQLMonitorConfig;

// Types whose checker supports the optional assertion query + value assertion.
const QUERY_CAPABLE_TYPES: DatabaseMonitorType[] = ['postgres', 'mysql'];

type ConnectionMode = 'fields' | 'connection_string';

const DB_TYPE_META: Record<
  DatabaseMonitorType,
  {
    label: string;
    defaultPort: number;
    hostPlaceholder: string;
    csPlaceholder: string;
    csHint: string;
    csSchemes: string[];
    csAllowsKeyValueDSN: boolean;
    // Accepts the driver's native non-URI DSN (mysql: user:pw@tcp(host)/db).
    csAllowsNativeDSN?: boolean;
    supportsTLSToggle: boolean;
    usernameRequired: boolean;
    checkVerb: string;
  }
> = {
  redis: {
    label: 'Redis',
    defaultPort: 6379,
    hostPlaceholder: 'redis.internal',
    csPlaceholder: 'redis://user:password@redis.internal:6379/0',
    csHint: 'redis:// or rediss:// (TLS) URI. May include username, password, and DB index.',
    csSchemes: ['redis://', 'rediss://'],
    csAllowsKeyValueDSN: false,
    supportsTLSToggle: true,
    usernameRequired: false,
    checkVerb: 'connect + PING',
  },
  postgres: {
    label: 'PostgreSQL',
    defaultPort: 5432,
    hostPlaceholder: 'db.internal',
    csPlaceholder: 'postgres://user:password@db.internal:5432/mydb?sslmode=require',
    csHint: 'postgres:// URI or key=value DSN. TLS is controlled by sslmode in the string.',
    csSchemes: ['postgres://', 'postgresql://'],
    csAllowsKeyValueDSN: true,
    supportsTLSToggle: false,
    usernameRequired: true,
    checkVerb: 'connect + ping',
  },
  mongodb: {
    label: 'MongoDB',
    defaultPort: 27017,
    hostPlaceholder: 'mongo.internal',
    csPlaceholder: 'mongodb://user:password@mongo.internal:27017/?authSource=admin',
    csHint: 'mongodb:// or mongodb+srv:// URI. May include credentials and options like tls=true.',
    csSchemes: ['mongodb://', 'mongodb+srv://'],
    csAllowsKeyValueDSN: false,
    supportsTLSToggle: true,
    usernameRequired: false,
    checkVerb: 'connect + ping',
  },
  rabbitmq: {
    label: 'RabbitMQ',
    defaultPort: 5672,
    hostPlaceholder: 'mq.internal',
    csPlaceholder: 'amqp://user:password@mq.internal:5672/vhost',
    csHint: 'amqp:// or amqps:// (TLS) URI. May include credentials and the vhost path.',
    csSchemes: ['amqp://', 'amqps://'],
    csAllowsKeyValueDSN: false,
    supportsTLSToggle: true,
    usernameRequired: true,
    checkVerb: 'AMQP handshake',
  },
  mysql: {
    label: 'MySQL',
    defaultPort: 3306,
    hostPlaceholder: 'db.internal',
    csPlaceholder: 'mysql://user:password@db.internal:3306/mydb',
    csHint: 'mysql:// URI or a driver DSN like user:password@tcp(db.internal:3306)/mydb.',
    csSchemes: ['mysql://'],
    csAllowsKeyValueDSN: false,
    csAllowsNativeDSN: true,
    supportsTLSToggle: true,
    usernameRequired: true,
    checkVerb: 'connect + ping',
  },
};

const QUERY_VALUE_OPS: { value: DBQueryValueOp; label: string }[] = [
  { value: 'equals', label: 'equals' },
  { value: 'not_equals', label: 'does not equal' },
  { value: 'contains', label: 'contains' },
  { value: 'number_gt', label: '> (number)' },
  { value: 'number_gte', label: '≥ (number)' },
  { value: 'number_lt', label: '< (number)' },
  { value: 'number_lte', label: '≤ (number)' },
];

// Write-only secret input: the API never returns stored secrets (only "***"),
// so in edit mode an untouched blank field means "keep the current value".
// onClear (when provided) lets the user drop the stored secret entirely.
function SecretInput({
  value,
  onChange,
  hasStored,
  onClear,
  placeholder,
  storedPlaceholder,
  mono = false,
  error,
}: {
  value: string;
  onChange: (value: string) => void;
  hasStored: boolean;
  onClear?: () => void;
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
          className={`input pr-20 ${mono ? 'font-mono text-xs' : ''}`}
        />
        <div className="absolute right-2 top-1/2 flex -translate-y-1/2 items-center gap-1.5">
          {hasStored && !value && (
            <span className="flex items-center gap-1 rounded-md bg-cyan-500/10 py-0.5 pl-1.5 pr-1 text-[10px] font-medium text-cyan-400" title="A value is stored. Leave blank to keep it.">
              <KeyRound className="h-3 w-3" strokeWidth={1.75} />
              set
              {onClear && (
                <button
                  type="button"
                  onClick={onClear}
                  className="rounded p-0.5 text-cyan-400/70 transition-colors hover:bg-cyan-500/20 hover:text-cyan-300"
                  aria-label="Clear stored value"
                  title="Clear the stored value"
                >
                  <X className="h-3 w-3" strokeWidth={2} />
                </button>
              )}
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

type TestState =
  | { phase: 'idle' }
  | { phase: 'running' }
  | { phase: 'done'; result: TestMonitorConfigResponse }
  | { phase: 'request_failed'; message: string };

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
  const storedPassword = isEditMode && existingConfig.password === MASKED_SECRET;
  const storedConnString = isEditMode && existingConfig.connection_string === MASKED_SECRET;
  const storedClientKey = isEditMode && existingConfig.tls_client_key_pem === MASKED_SECRET;

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
    replica_set: existingConfig.replica_set || '',
    vhost: existingConfig.vhost || '',
    db_index: existingConfig.db ?? 0,
    tls_enabled: existingConfig.tls_enabled ?? false,
    tls_skip_verify: existingConfig.tls_skip_verify ?? false,
    tls_ca_pem: existingConfig.tls_ca_pem || '',
    tls_client_cert_pem: existingConfig.tls_client_cert_pem || '',
    tls_client_key_pem: '',
    query: existingConfig.query || '',
    query_value_op: existingConfig.query_value_op || ('' as '' | DBQueryValueOp),
    query_value: existingConfig.query_value || '',
    expected_role: existingConfig.expected_role || ('' as '' | 'master' | 'replica'),
    max_latency_ms: existingConfig.max_latency_ms ? String(existingConfig.max_latency_ms) : '',
    warn_latency_ms: existingConfig.warn_latency_ms ? String(existingConfig.warn_latency_ms) : '',
    interval_seconds: monitor?.interval_seconds || initialData?.interval_seconds || 60,
    timeout_seconds: monitor?.timeout_seconds || initialData?.timeout_seconds || 10,
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
    consecutive_failures_threshold: monitor?.consecutive_failures_threshold ?? 2,
    notification_mode: (monitor?.notification_mode ?? 'default') as NotificationMode,
    notification_channels: monitor?.notification_channels ?? ([] as ChannelAssignment[]),
    location_ids: monitor?.location_ids ?? initialData?.location_ids ?? ([] as string[]),
    location_quorum: monitor?.location_quorum ?? initialData?.location_quorum ?? 1,
  });

  // Explicitly dropped stored secrets ("Clear" on the set-chip): the field is
  // then omitted from the submitted config, which clears it server-side.
  const [cleared, setCleared] = useState({ password: false, clientKey: false });
  const hasStoredPassword = storedPassword && !cleared.password;
  const hasStoredClientKey = storedClientKey && !cleared.clientKey;
  const hasStoredConnString = storedConnString;

  const [errors, setErrors] = useState<Record<string, string>>({});
  const [certsOpen, setCertsOpen] = useState(
    Boolean(formData.tls_ca_pem || formData.tls_client_cert_pem || storedClientKey)
  );
  const [test, setTest] = useState<TestState>({ phase: 'idle' });
  // List shared by the Locations section (via onLocationsLoaded) so the
  // test-from picker can resolve location names.
  const [availableLocations, setAvailableLocations] = useState<Location[]>([]);
  // '' = default fleet.
  const [testLocationId, setTestLocationId] = useState('');

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

  const validateConnectionString = (value: string): string | undefined => {
    if (!value) return undefined;
    if (value.includes('://')) {
      if (!meta.csSchemes.some((s) => value.startsWith(s))) {
        return `Must start with ${meta.csSchemes.join(' or ')}`;
      }
      return undefined;
    }
    if (meta.csAllowsKeyValueDSN && value.includes('=')) return undefined;
    if (meta.csAllowsNativeDSN && value.includes('@')) return undefined;
    return `Must start with ${meta.csSchemes.join(' or ')}`;
  };

  const validate = (): Record<string, string> => {
    const newErrors: Record<string, string> = {};
    if (!formData.name.trim()) newErrors.name = 'Name is required';
    if (usingConnString) {
      const cs = formData.connection_string.trim();
      if (!cs && !hasStoredConnString) {
        newErrors.connection_string = 'Connection string is required';
      } else if (cs) {
        const schemeError = validateConnectionString(cs);
        if (schemeError) newErrors.connection_string = schemeError;
      }
    } else {
      if (!formData.host.trim()) newErrors.host = 'Host is required';
      if (formData.port < 1 || formData.port > 65535) newErrors.port = 'Port must be between 1 and 65535';
      if (meta.usernameRequired && !formData.username.trim()) newErrors.username = 'Username is required';
      if (type === 'mongodb' && formData.username.trim() && !formData.password && !hasStoredPassword) {
        newErrors.password = 'Password is required when a username is set';
      }
    }
    const certProvided = formData.tls_client_cert_pem.trim() !== '';
    const keyProvided = formData.tls_client_key_pem.trim() !== '' || hasStoredClientKey;
    if (tlsMaterialRelevant && certProvided && !keyProvided) {
      newErrors.tls_client_key_pem = 'Client key is required alongside a client certificate';
    }
    if (QUERY_CAPABLE_TYPES.includes(type) && formData.query_value_op) {
      if (!formData.query.trim()) {
        newErrors.query = 'A query is required for a value assertion';
      }
      if (formData.query_value_op.startsWith('number_') && formData.query_value.trim() !== '' && !Number.isFinite(Number(formData.query_value))) {
        newErrors.query_value = 'Must be a number for numeric comparisons';
      }
    }
    const maxLatency = formData.max_latency_ms.trim() ? parseInt(formData.max_latency_ms, 10) : undefined;
    const warnLatency = formData.warn_latency_ms.trim() ? parseInt(formData.warn_latency_ms, 10) : undefined;
    if (formData.max_latency_ms.trim() && (!Number.isFinite(maxLatency) || (maxLatency as number) <= 0)) {
      newErrors.max_latency_ms = 'Must be a positive number of milliseconds';
    }
    if (formData.warn_latency_ms.trim() && (!Number.isFinite(warnLatency) || (warnLatency as number) <= 0)) {
      newErrors.warn_latency_ms = 'Must be a positive number of milliseconds';
    }
    if (maxLatency && warnLatency && warnLatency >= maxLatency) {
      newErrors.warn_latency_ms = 'Must be lower than max latency';
    }
    if (formData.timeout_seconds >= formData.interval_seconds) {
      newErrors.timeout_seconds = 'Timeout must be less than interval';
    }
    return newErrors;
  };

  const buildConfig = (): DatabaseConfig => {
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
      if (type === 'mongodb') {
        if (formData.auth_source.trim()) config.auth_source = formData.auth_source.trim();
        if (formData.replica_set.trim()) config.replica_set = formData.replica_set.trim();
      }
      if (type === 'rabbitmq' && formData.vhost.trim()) config.vhost = formData.vhost.trim();
      if (type === 'mysql' && formData.database.trim()) config.database = formData.database.trim();
      if (meta.supportsTLSToggle && formData.tls_enabled) {
        config.tls_enabled = true;
        if (formData.tls_skip_verify) config.tls_skip_verify = true;
      }
    }

    if (tlsMaterialRelevant) {
      if (formData.tls_ca_pem.trim()) config.tls_ca_pem = formData.tls_ca_pem.trim();
      if (formData.tls_client_cert_pem.trim()) {
        config.tls_client_cert_pem = formData.tls_client_cert_pem.trim();
        // Blank + previously stored = keep the stored key (write-only).
        config.tls_client_key_pem = formData.tls_client_key_pem.trim() || MASKED_SECRET;
      }
    }

    if (type === 'redis' && formData.expected_role) config.expected_role = formData.expected_role;
    if (QUERY_CAPABLE_TYPES.includes(type) && formData.query.trim()) {
      config.query = formData.query.trim();
      if (formData.query_value_op) {
        config.query_value_op = formData.query_value_op;
        config.query_value = formData.query_value;
      }
    }
    const maxLatency = formData.max_latency_ms.trim() ? parseInt(formData.max_latency_ms, 10) : undefined;
    const warnLatency = formData.warn_latency_ms.trim() ? parseInt(formData.warn_latency_ms, 10) : undefined;
    if (maxLatency) config.max_latency_ms = maxLatency;
    if (warnLatency) config.warn_latency_ms = warnLatency;

    return config;
  };

  const handleTestConnection = async () => {
    // Test cares about connection validity, not monitor naming.
    const connectionErrors = validate();
    delete connectionErrors.name;
    delete connectionErrors.timeout_seconds;
    setErrors(connectionErrors);
    if (Object.keys(connectionErrors).length > 0) return;

    // Ignore a stale pick if the location was deselected since.
    const chosenLocationId = formData.location_ids.includes(testLocationId) ? testLocationId : '';

    setTest({ phase: 'running' });
    try {
      const result = await testMonitorConfig({
        type,
        config: buildConfig(),
        timeout_seconds: Math.min(formData.timeout_seconds || 10, 30),
        ...(isEditMode && monitor ? { monitor_id: monitor.id } : {}),
        ...(chosenLocationId ? { location_id: chosenLocationId } : {}),
      });
      setTest({ phase: 'done', result });
    } catch (err) {
      setTest({ phase: 'request_failed', message: err instanceof Error ? err.message : 'Test request failed' });
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const newErrors = validate();
    setErrors(newErrors);
    if (Object.keys(newErrors).length > 0) return;

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = {
      name: formData.name.trim(),
      type: type as MonitorType,
      config: buildConfig(),
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
    requestData.location_ids = formData.location_ids;
    requestData.location_quorum =
      formData.location_ids.length >= 2
        ? Math.min(formData.location_quorum, formData.location_ids.length)
        : 1;

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
    const check = QUERY_CAPABLE_TYPES.includes(type) && formData.query.trim() ? 'run query' : meta.checkVerb;
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
                  placeholder={meta.hostPlaceholder}
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

            {/* Hints go below the inputs here (not via FormField's description,
                which sits above) so the two inputs stay vertically aligned. */}
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <FormField
                label={meta.usernameRequired ? 'Username' : 'Username (optional)'}
                required={meta.usernameRequired}
                error={errors.username}
              >
                <input
                  type="text"
                  value={formData.username}
                  onChange={(e) => setFormData({ ...formData, username: e.target.value })}
                  placeholder={meta.usernameRequired ? 'monitoring' : ''}
                  autoComplete="off"
                  className="input"
                />
                {type === 'redis' && (
                  <p className="mt-1 text-xs text-slate-500">ACL user — leave blank for the default user</p>
                )}
              </FormField>
              <FormField label="Password" error={errors.password}>
                <SecretInput
                  value={formData.password}
                  onChange={(v) => setFormData({ ...formData, password: v })}
                  hasStored={hasStoredPassword}
                  onClear={() => setCleared({ ...cleared, password: true })}
                  placeholder="••••••••"
                  storedPlaceholder="Unchanged"
                />
                {!errors.password && (
                  <p className="mt-1 text-xs text-slate-500">
                    {hasStoredPassword ? 'Leave blank to keep the current password' : 'Stored encrypted, never displayed'}
                  </p>
                )}
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
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <FormField label="Auth source" description="Database to authenticate against. Default: admin">
                  <input
                    type="text"
                    value={formData.auth_source}
                    onChange={(e) => setFormData({ ...formData, auth_source: e.target.value })}
                    placeholder="admin"
                    className="input"
                  />
                </FormField>
                <FormField label="Replica set (optional)" description="Discover the topology and assert a reachable primary">
                  <input
                    type="text"
                    value={formData.replica_set}
                    onChange={(e) => setFormData({ ...formData, replica_set: e.target.value })}
                    placeholder="rs0"
                    className="input"
                  />
                </FormField>
              </div>
            )}

            {type === 'rabbitmq' && (
              <FormField label="Virtual host" description="Default: /">
                <input
                  type="text"
                  value={formData.vhost}
                  onChange={(e) => setFormData({ ...formData, vhost: e.target.value })}
                  placeholder="/"
                  className="input"
                />
              </FormField>
            )}

            {type === 'mysql' && (
              <FormField label="Database (optional)" description="Default schema to select after connecting">
                <input
                  type="text"
                  value={formData.database}
                  onChange={(e) => setFormData({ ...formData, database: e.target.value })}
                  placeholder="app"
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
                    {hasStoredClientKey && !formData.tls_client_key_pem && (
                      <button
                        type="button"
                        onClick={() => setCleared({ ...cleared, clientKey: true })}
                        className="mb-1.5 text-[11px] text-slate-500 underline-offset-2 hover:text-slate-300 hover:underline"
                      >
                        Clear the stored key
                      </button>
                    )}
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

        <div className="flex flex-wrap items-center gap-3 rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3">
          <button
            type="button"
            onClick={handleTestConnection}
            disabled={test.phase === 'running' || loading}
            className="inline-flex items-center gap-2 rounded-[12px] border border-white/10 bg-white/[0.04] px-3 py-2 text-xs font-medium text-slate-200 transition-colors hover:bg-white/[0.08] hover:text-white focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/40 disabled:cursor-not-allowed disabled:opacity-40"
          >
            {test.phase === 'running' ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" strokeWidth={1.75} />
            ) : (
              <PlugZap className="h-3.5 w-3.5" strokeWidth={1.75} />
            )}
            {test.phase === 'running' ? 'Testing…' : 'Test connection'}
          </button>
          {formData.location_ids.length > 0 && (
            <select
              aria-label="Test from location"
              value={formData.location_ids.includes(testLocationId) ? testLocationId : ''}
              onChange={(e) => setTestLocationId(e.target.value)}
              className="rounded-md border border-white/[0.08] bg-slate-900 px-2 py-1.5 text-xs text-slate-200"
            >
              <option value="">Test from: default fleet</option>
              {availableLocations
                .filter((loc) => formData.location_ids.includes(loc.id))
                .map((loc) => (
                  <option key={loc.id} value={loc.id}>Test from: {loc.name}</option>
                ))}
            </select>
          )}
          {test.phase === 'idle' && (
            <p className="text-xs text-slate-500">Runs one check from a worker without saving anything.</p>
          )}
          {test.phase === 'done' && test.result.status === 'success' && (
            <p className="flex items-center gap-1.5 text-xs text-emerald-400">
              <CheckCircle2 className="h-3.5 w-3.5" strokeWidth={1.75} />
              Connected{typeof test.result.latency_ms === 'number' ? ` in ${test.result.latency_ms}ms` : ''}
            </p>
          )}
          {test.phase === 'done' && test.result.status !== 'success' && (
            <p className="flex min-w-0 items-center gap-1.5 text-xs text-rose-400">
              <XCircle className="h-3.5 w-3.5 shrink-0" strokeWidth={1.75} />
              <span className="truncate" title={test.result.error_message}>
                {test.result.error_message || 'Check failed'}
              </span>
            </p>
          )}
          {test.phase === 'request_failed' && (
            <p className="flex min-w-0 items-center gap-1.5 text-xs text-amber-400">
              <XCircle className="h-3.5 w-3.5 shrink-0" strokeWidth={1.75} />
              <span className="truncate" title={test.message}>{test.message}</span>
            </p>
          )}
        </div>
      </FormSection>

      <FormSection title="Checks">
        {QUERY_CAPABLE_TYPES.includes(type) && (
          <>
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
            {formData.query.trim() !== '' && (
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <FormField label="Value assertion (optional)" description="Compare the first column of the first row">
                  <select
                    value={formData.query_value_op}
                    onChange={(e) => setFormData({ ...formData, query_value_op: e.target.value as typeof formData.query_value_op })}
                    className="input"
                  >
                    <option value="">Row count only (≥ 1 row)</option>
                    {QUERY_VALUE_OPS.map((op) => (
                      <option key={op.value} value={op.value}>
                        Value {op.label}
                      </option>
                    ))}
                  </select>
                </FormField>
                {formData.query_value_op && (
                  <FormField label="Expected value" error={errors.query_value}>
                    <input
                      type="text"
                      value={formData.query_value}
                      onChange={(e) => setFormData({ ...formData, query_value: e.target.value })}
                      placeholder={formData.query_value_op.startsWith('number_') ? '100' : 'ok'}
                      className="input font-mono text-xs"
                    />
                  </FormField>
                )}
              </div>
            )}
          </>
        )}
        {type === 'redis' && (
          <FormField label="Expected role (optional)" description="Fail if this node's replication role changes — catches monitoring the wrong node after a failover">
            <select
              value={formData.expected_role}
              onChange={(e) => setFormData({ ...formData, expected_role: e.target.value as typeof formData.expected_role })}
              className="input"
            >
              <option value="">Any role</option>
              <option value="master">Master</option>
              <option value="replica">Replica</option>
            </select>
          </FormField>
        )}
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <FormField
            label="Warn latency (ms, optional)"
            error={errors.warn_latency_ms}
            description="Flag the check with a warning above this — without failing it"
          >
            <input
              type="number"
              value={formData.warn_latency_ms}
              onChange={(e) => setFormData({ ...formData, warn_latency_ms: e.target.value })}
              min={1}
              placeholder="e.g. 100"
              className="input"
            />
          </FormField>
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
        </div>
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

      <FormSection
        title="Locations"
        summary={formData.location_ids.length > 0 ? `${formData.location_ids.length} selected` : 'Default fleet'}
        collapsible
        defaultOpen={formData.location_ids.length > 0}
      >
        <LocationsSection
          selectedIds={formData.location_ids}
          onChange={(ids) => setFormData({ ...formData, location_ids: ids })}
          quorum={formData.location_quorum}
          onQuorumChange={(n) => setFormData({ ...formData, location_quorum: n })}
          onLocationsLoaded={setAvailableLocations}
        />
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
