'use client';

import { useState } from 'react';
import {
  Monitor,
  CreateMonitorRequest,
  UpdateMonitorRequest,
  MonitorType,
  DependencySuppression,
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
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import {
  Activity,
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
import InfoTip from '@/components/ui/InfoTip';
import FormActions from '@/components/ui/FormActions';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import { AlertingSection } from './AlertingSection';
import { LocationsSection } from './LocationsSection';
import Switch from '@/components/ui/Switch';

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
          className={`input ${hasStored && !value ? '!pr-28' : '!pr-10'} ${mono ? 'font-mono' : ''}`}
        />
        <div className="absolute right-2 top-1/2 flex -translate-y-1/2 items-center gap-1">
          {hasStored && !value && (
            <span className={`flex items-center gap-1 rounded-full bg-well-hover py-0.5 pl-2 text-[11px] font-medium text-slate-300 ${onClear ? 'pr-1' : 'pr-2'}`} title="A value is stored. Leave blank to keep it.">
              <KeyRound className="h-3 w-3" strokeWidth={1.75} />
              set
              {onClear && (
                <button
                  type="button"
                  onClick={onClear}
                  className="rounded-full p-0.5 text-slate-400 transition-colors hover:bg-control-hover hover:text-white"
                  aria-label="Clear stored value"
                  title="Clear the stored value"
                >
                  <X className="h-3 w-3" strokeWidth={1.75} />
                </button>
              )}
            </span>
          )}
          <button
            type="button"
            onClick={() => setReveal(!reveal)}
            className="rounded-lg p-1 text-slate-400 transition-colors hover:text-white"
            aria-label={reveal ? 'Hide value' : 'Show value'}
            tabIndex={-1}
          >
            {reveal ? <EyeOff className="h-4 w-4" strokeWidth={1.75} /> : <Eye className="h-4 w-4" strokeWidth={1.75} />}
          </button>
        </div>
      </div>
      {error && <p className="mt-1 text-xs text-rose-300">{error}</p>}
    </div>
  );
}

function MiniToggle({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label: string }) {
  return (
    <Switch checked={checked} onChange={onChange} label={label} />
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
    <div className="flex items-center justify-between gap-4">
      <div className="min-w-0">
        <p className="text-sm font-medium text-slate-200">{title}</p>
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
  const { canWrite, loading: loadingUser } = useCurrentUser();
  const meta = DB_TYPE_META[type];
  const isEditMode = Boolean(monitor);
  const canTestConnection = !loadingUser && (!isEditMode || canWrite);
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
    collect_replication: existingConfig.collect_replication ?? false,
    collect_connections: existingConfig.collect_connections ?? false,
    collect_cache: existingConfig.collect_cache ?? false,
    collect_memory: existingConfig.collect_memory ?? false,
    collect_network: existingConfig.collect_network ?? false,
    collect_cpu: existingConfig.collect_cpu ?? false,
    max_replication_lag_seconds: existingConfig.max_replication_lag_seconds
      ? String(existingConfig.max_replication_lag_seconds)
      : '',
    warn_replication_lag_seconds: existingConfig.warn_replication_lag_seconds
      ? String(existingConfig.warn_replication_lag_seconds)
      : '',
    interval_seconds: monitor?.interval_seconds || initialData?.interval_seconds || 60,
    timeout_seconds: monitor?.timeout_seconds || initialData?.timeout_seconds || 10,
    enabled: monitor?.enabled ?? initialData?.enabled ?? true,
    tags: monitor?.tags?.join(', ') || (initialData?.tags || []).join(', '),
    consecutive_failures_threshold: monitor?.consecutive_failures_threshold ?? 2,
    notification_mode: (monitor?.notification_mode ?? 'default') as NotificationMode,
    dependency_suppression: (monitor?.dependency_suppression ?? 'inherit') as DependencySuppression,
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
  const clusterChecksEnabled = [
    formData.collect_replication,
    formData.collect_connections,
    formData.collect_cache,
    formData.collect_memory,
    formData.collect_network,
    formData.collect_cpu,
  ].filter(Boolean).length;
  const [clusterChecksOpen, setClusterChecksOpen] = useState(clusterChecksEnabled > 0);
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
    if (type === 'mongodb' && formData.collect_replication) {
      const maxLag = formData.max_replication_lag_seconds.trim()
        ? parseInt(formData.max_replication_lag_seconds, 10)
        : undefined;
      const warnLag = formData.warn_replication_lag_seconds.trim()
        ? parseInt(formData.warn_replication_lag_seconds, 10)
        : undefined;
      if (formData.max_replication_lag_seconds.trim() && (!Number.isFinite(maxLag) || (maxLag as number) <= 0)) {
        newErrors.max_replication_lag_seconds = 'Must be a positive number of seconds';
      }
      if (formData.warn_replication_lag_seconds.trim() && (!Number.isFinite(warnLag) || (warnLag as number) <= 0)) {
        newErrors.warn_replication_lag_seconds = 'Must be a positive number of seconds';
      }
      if (maxLag && warnLag && warnLag >= maxLag) {
        newErrors.warn_replication_lag_seconds = 'Must be lower than max replication lag';
      }
    }
    if (formData.timeout_seconds >= formData.interval_seconds) {
      newErrors.timeout_seconds = 'Timeout must be less than interval';
    }
    return newErrors;
  };

  const buildConfig = (): DatabaseConfig => {
    const config: DatabaseConfig = {};

    // An omitted secret field means "keep the stored value" server-side, so
    // every deliberate drop — the Clear affordances and the connection-mode
    // switch — has to submit "" (the explicit clear) instead of leaving the
    // field out.
    if (usingConnString) {
      // Blank + previously stored = keep the stored secret (write-only).
      config.connection_string = formData.connection_string.trim() || MASKED_SECRET;
      if (storedPassword) config.password = '';
      if (meta.supportsTLSToggle && formData.tls_skip_verify) config.tls_skip_verify = true;
    } else {
      if (storedConnString) config.connection_string = '';
      config.host = formData.host.trim();
      config.port = formData.port;
      if (formData.username.trim()) config.username = formData.username.trim();
      if (formData.password) {
        config.password = formData.password;
      } else if (hasStoredPassword) {
        config.password = MASKED_SECRET;
      } else if (storedPassword) {
        // "Clear" on the set-chip.
        config.password = '';
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
      } else if (storedClientKey) {
        // Client cert removed (or the stored key cleared): "" is the explicit
        // clear — omitting the field would keep the stored key.
        config.tls_client_key_pem = '';
      }
    }

    if (type === 'redis' && formData.expected_role) config.expected_role = formData.expected_role;
    if (type === 'mongodb') {
      // Cluster checks apply in both connection modes — post-connect commands.
      if (formData.collect_replication) config.collect_replication = true;
      if (formData.collect_connections) config.collect_connections = true;
      if (formData.collect_cache) config.collect_cache = true;
      if (formData.collect_memory) config.collect_memory = true;
      if (formData.collect_network) config.collect_network = true;
      if (formData.collect_cpu) config.collect_cpu = true;
      if (formData.collect_replication) {
        const maxLag = formData.max_replication_lag_seconds.trim()
          ? parseInt(formData.max_replication_lag_seconds, 10)
          : undefined;
        const warnLag = formData.warn_replication_lag_seconds.trim()
          ? parseInt(formData.warn_replication_lag_seconds, 10)
          : undefined;
        if (maxLag) config.max_replication_lag_seconds = maxLag;
        if (warnLag) config.warn_replication_lag_seconds = warnLag;
      }
    }
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
    if (!canTestConnection) return;
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
    requestData.dependency_suppression = formData.dependency_suppression;
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
          <div className="mb-2 flex w-fit max-w-full gap-0.5 rounded-xl bg-well p-1 shadow-[inset_0_0_0_1px_var(--glass-edge)]" role="radiogroup" aria-label="Connection mode">
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
                className={`whitespace-nowrap rounded-lg px-3.5 py-1.5 text-[13px] font-medium transition-colors ${
                  formData.connection_mode === mode
                    ? 'bg-glass-strong text-white shadow-glass'
                    : 'text-slate-400 hover:text-slate-100'
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
            <FormField label="Connection string" required={!hasStoredConnString} error={errors.connection_string}>
              <SecretInput
                value={formData.connection_string}
                onChange={(v) => setFormData({ ...formData, connection_string: v })}
                hasStored={hasStoredConnString}
                placeholder={meta.csPlaceholder}
                storedPlaceholder="Unchanged — paste a new connection string to replace"
                mono
              />
              <p className="mt-1 text-xs text-slate-500">{meta.csHint}</p>
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
              <FormField label="Host" required error={errors.host}>
                <input
                  type="text"
                  value={formData.host}
                  onChange={(e) => setFormData({ ...formData, host: e.target.value })}
                  placeholder={meta.hostPlaceholder}
                  className="input"
                />
                <p className="mt-1 text-xs text-slate-500">{meta.label} hostname or IP</p>
              </FormField>
              <FormField label="Port" error={errors.port}>
                <input
                  type="number"
                  value={formData.port}
                  onChange={(e) => setFormData({ ...formData, port: parseInt(e.target.value, 10) || meta.defaultPort })}
                  min={1}
                  max={65535}
                  className="input"
                />
                <p className="mt-1 text-xs text-slate-500">Default: {meta.defaultPort}</p>
              </FormField>
            </div>

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
              <FormField label="Database index">
                <input
                  type="number"
                  value={formData.db_index}
                  onChange={(e) => setFormData({ ...formData, db_index: parseInt(e.target.value, 10) || 0 })}
                  min={0}
                  max={15}
                  className="input"
                />
                <p className="mt-1 text-xs text-slate-500">0–15, default 0</p>
              </FormField>
            )}

            {type === 'postgres' && (
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <FormField label="Database">
                  <input
                    type="text"
                    value={formData.database}
                    onChange={(e) => setFormData({ ...formData, database: e.target.value })}
                    placeholder="postgres"
                    className="input"
                  />
                  <p className="mt-1 text-xs text-slate-500">Default: postgres</p>
                </FormField>
                <FormField label="TLS (sslmode)">
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
                  <p className="mt-1 text-xs text-slate-500">How to negotiate TLS</p>
                </FormField>
              </div>
            )}

            {type === 'mongodb' && (
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <FormField label="Auth source">
                  <input
                    type="text"
                    value={formData.auth_source}
                    onChange={(e) => setFormData({ ...formData, auth_source: e.target.value })}
                    placeholder="admin"
                    className="input"
                  />
                  <p className="mt-1 text-xs text-slate-500">Database to authenticate against. Default: admin</p>
                </FormField>
                <FormField label="Replica set (optional)">
                  <input
                    type="text"
                    value={formData.replica_set}
                    onChange={(e) => setFormData({ ...formData, replica_set: e.target.value })}
                    placeholder="rs0"
                    className="input"
                  />
                  <p className="mt-1 text-xs text-slate-500">Discover the topology and assert a reachable primary</p>
                </FormField>
              </div>
            )}

            {type === 'rabbitmq' && (
              <FormField label="Virtual host">
                <input
                  type="text"
                  value={formData.vhost}
                  onChange={(e) => setFormData({ ...formData, vhost: e.target.value })}
                  placeholder="/"
                  className="input"
                />
                <p className="mt-1 text-xs text-slate-500">Default: /</p>
              </FormField>
            )}

            {type === 'mysql' && (
              <FormField label="Database (optional)">
                <input
                  type="text"
                  value={formData.database}
                  onChange={(e) => setFormData({ ...formData, database: e.target.value })}
                  placeholder="app"
                  className="input"
                />
                <p className="mt-1 text-xs text-slate-500">Default schema to select after connecting</p>
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
          <div className="glass-well">
            <button
              type="button"
              onClick={() => setCertsOpen(!certsOpen)}
              className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left"
              aria-expanded={certsOpen}
            >
              <span className="flex min-w-0 flex-wrap items-center gap-2 text-sm font-medium text-slate-200">
                <ShieldCheck className="h-4 w-4 text-slate-400" strokeWidth={1.75} />
                Custom certificates
                <span className="text-xs font-normal text-slate-500">private CA / mutual TLS</span>
                {hasTLSMaterial && (
                  <Pill tone="neutral" size="xs">
                    configured
                  </Pill>
                )}
              </span>
              {certsOpen ? (
                <ChevronDown className="h-4 w-4 shrink-0 text-slate-400" strokeWidth={1.75} />
              ) : (
                <ChevronRight className="h-4 w-4 shrink-0 text-slate-400" strokeWidth={1.75} />
              )}
            </button>
            {certsOpen && (
              <div className="space-y-4 px-4 pb-4 pt-1">
                <FormField label="CA certificate (PEM, optional)">
                  <textarea
                    rows={4}
                    value={formData.tls_ca_pem}
                    onChange={(e) => setFormData({ ...formData, tls_ca_pem: e.target.value })}
                    placeholder={'-----BEGIN CERTIFICATE-----\n…\n-----END CERTIFICATE-----'}
                    spellCheck={false}
                    className="input min-h-[88px] font-mono"
                  />
                  <p className="mt-1 text-xs text-slate-500">Verify a server certificate signed by a private or internal CA</p>
                </FormField>
                <FormField label="Client certificate (PEM, optional)">
                  <textarea
                    rows={4}
                    value={formData.tls_client_cert_pem}
                    onChange={(e) => setFormData({ ...formData, tls_client_cert_pem: e.target.value })}
                    placeholder={'-----BEGIN CERTIFICATE-----\n…\n-----END CERTIFICATE-----'}
                    spellCheck={false}
                    className="input min-h-[88px] font-mono"
                  />
                  <p className="mt-1 text-xs text-slate-500">Only for mutual TLS / X.509 authentication</p>
                </FormField>
                {(formData.tls_client_cert_pem.trim() !== '' || formData.tls_client_key_pem !== '' || hasStoredClientKey) && (
                  <FormField
                    label="Client key (PEM)"
                    error={errors.tls_client_key_pem}
                  >
                    {hasStoredClientKey && !formData.tls_client_key_pem && (
                      <button
                        type="button"
                        onClick={() => setCleared({ ...cleared, clientKey: true })}
                        className="mb-1.5 text-xs text-slate-400 underline-offset-2 hover:text-white hover:underline"
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
                      className="input min-h-[88px] font-mono"
                    />
                    <p className="mt-1 text-xs text-slate-500">
                      {hasStoredClientKey
                        ? 'Stored encrypted — leave blank to keep the current key'
                        : 'Stored encrypted, never displayed again'}
                    </p>
                  </FormField>
                )}
              </div>
            )}
          </div>
        )}

        <div className="glass-well flex flex-wrap items-center gap-3 px-4 py-3">
          <Button
            type="button"
            onClick={handleTestConnection}
            disabled={!canTestConnection || test.phase === 'running' || loading}
            title={!canTestConnection ? 'Write access is required to test saved connections' : undefined}
            icon={
              test.phase === 'running' ? (
                <Loader2 className="h-4 w-4 animate-spin" strokeWidth={1.75} />
              ) : (
                <PlugZap className="h-4 w-4" strokeWidth={1.75} />
              )
            }
          >
            {test.phase === 'running' ? 'Testing…' : 'Test connection'}
          </Button>
          {formData.location_ids.length > 0 && (
            <select
              aria-label="Test from location"
              value={formData.location_ids.includes(testLocationId) ? testLocationId : ''}
              onChange={(e) => setTestLocationId(e.target.value)}
              className="input input-sm !w-auto"
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
            <p className="flex items-center gap-1.5 text-xs text-emerald-300">
              <CheckCircle2 className="h-3.5 w-3.5" strokeWidth={1.75} />
              Connected{typeof test.result.latency_ms === 'number' ? ` in ${test.result.latency_ms}ms` : ''}
            </p>
          )}
          {test.phase === 'done' &&
            test.result.status === 'success' &&
            test.result.metrics_data?.mongodb?.unavailable?.some((u) => u.reason === 'unauthorized') && (
              <p className="text-xs text-amber-300">
                Cluster checks skipped — grant the monitoring user the clusterMonitor role
              </p>
            )}
          {test.phase === 'done' && test.result.status !== 'success' && (
            <p className="flex min-w-0 items-center gap-1.5 text-xs text-rose-300">
              <XCircle className="h-3.5 w-3.5 shrink-0" strokeWidth={1.75} />
              <span className="truncate" title={test.result.error_message}>
                {test.result.error_message || 'Check failed'}
              </span>
            </p>
          )}
          {test.phase === 'request_failed' && (
            <p className="flex min-w-0 items-center gap-1.5 text-xs text-amber-300">
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
            >
              <input
                type="text"
                value={formData.query}
                onChange={(e) => setFormData({ ...formData, query: e.target.value })}
                placeholder="SELECT 1"
                className="input font-mono"
              />
              <p className="mt-1 text-xs text-slate-500">Runs after connecting; the check fails if it errors or returns 0 rows. Example: SELECT 1</p>
            </FormField>
            {formData.query.trim() !== '' && (
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <FormField label="Value assertion (optional)">
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
                  <p className="mt-1 text-xs text-slate-500">Compare the first column of the first row</p>
                </FormField>
                {formData.query_value_op && (
                  <FormField label="Expected value" error={errors.query_value}>
                    <input
                      type="text"
                      value={formData.query_value}
                      onChange={(e) => setFormData({ ...formData, query_value: e.target.value })}
                      placeholder={formData.query_value_op.startsWith('number_') ? '100' : 'ok'}
                      className="input font-mono"
                    />
                  </FormField>
                )}
              </div>
            )}
          </>
        )}
        {type === 'redis' && (
          <FormField label="Expected role (optional)">
            <select
              value={formData.expected_role}
              onChange={(e) => setFormData({ ...formData, expected_role: e.target.value as typeof formData.expected_role })}
              className="input"
            >
              <option value="">Any role</option>
              <option value="master">Master</option>
              <option value="replica">Replica</option>
            </select>
            <p className="mt-1 text-xs text-slate-500">Fail if this node&apos;s replication role changes — catches monitoring the wrong node after a failover</p>
          </FormField>
        )}
        {type === 'mongodb' && (
          <div className="glass-well">
            <button
              type="button"
              onClick={() => setClusterChecksOpen(!clusterChecksOpen)}
              className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left"
              aria-expanded={clusterChecksOpen}
            >
              <span className="flex min-w-0 flex-wrap items-center gap-2 text-sm font-medium text-slate-200">
                <Activity className="h-4 w-4 text-slate-400" strokeWidth={1.75} />
                Cluster checks
                <span className="text-xs font-normal text-slate-500">serverStatus / replSetGetStatus</span>
                <Pill tone="warning" size="xs">
                  requires clusterMonitor
                </Pill>
                {clusterChecksEnabled > 0 && (
                  <Pill tone="neutral" size="xs">
                    {clusterChecksEnabled} enabled
                  </Pill>
                )}
              </span>
              {clusterChecksOpen ? (
                <ChevronDown className="h-4 w-4 shrink-0 text-slate-400" strokeWidth={1.75} />
              ) : (
                <ChevronRight className="h-4 w-4 shrink-0 text-slate-400" strokeWidth={1.75} />
              )}
            </button>
            {clusterChecksOpen && (
              <div className="space-y-3 px-4 pb-4 pt-1">
                <div className="flex items-center gap-1.5 text-xs text-slate-500">
                  Read-only admin commands — skipped and flagged when the role is missing, never failing the check.
                  <InfoTip inLabel ariaLabel="About cluster checks and permissions">
                    Both commands are covered by MongoDB&apos;s built-in <code>clusterMonitor</code> role — no write or
                    admin privileges: <code>db.grantRolesToUser(&quot;&lt;user&gt;&quot;, [&#123;role: &quot;clusterMonitor&quot;,
                    db: &quot;admin&quot;&#125;])</code>. Without it the data is skipped and flagged on the monitor; only a
                    configured max-lag threshold turns missing replication data into a failure.
                  </InfoTip>
                </div>
                <ToggleRow
                  title="Replication status"
                  description="Member health and replication lag (replica sets only)"
                  checked={formData.collect_replication}
                  onChange={(v) =>
                    setFormData({
                      ...formData,
                      collect_replication: v,
                      // Lag thresholds are meaningless without the check.
                      max_replication_lag_seconds: v ? formData.max_replication_lag_seconds : '',
                      warn_replication_lag_seconds: v ? formData.warn_replication_lag_seconds : '',
                    })
                  }
                />
                <ToggleRow
                  title="Connections"
                  description="Current and available connections"
                  checked={formData.collect_connections}
                  onChange={(v) => setFormData({ ...formData, collect_connections: v })}
                />
                <ToggleRow
                  title="WiredTiger cache"
                  description="Cache used, configured max, and dirty bytes"
                  checked={formData.collect_cache}
                  onChange={(v) => setFormData({ ...formData, collect_cache: v })}
                />
                <ToggleRow
                  title="Memory"
                  description="Resident and virtual memory"
                  checked={formData.collect_memory}
                  onChange={(v) => setFormData({ ...formData, collect_memory: v })}
                />
                <ToggleRow
                  title="Network & operations"
                  description="Network I/O and operation counters"
                  checked={formData.collect_network}
                  onChange={(v) => setFormData({ ...formData, collect_network: v })}
                />
                <ToggleRow
                  title="Process CPU"
                  description="CPU time consumed by mongod (Linux servers)"
                  checked={formData.collect_cpu}
                  onChange={(v) => setFormData({ ...formData, collect_cpu: v })}
                />
                {formData.collect_replication && (
                  <div className="grid grid-cols-1 gap-4 pt-1 sm:grid-cols-2">
                    <FormField
                      label="Warn replication lag (s)"
                      error={errors.warn_replication_lag_seconds}
                      infoTip="Optional. Flags the check with a warning above this lag — without failing it."
                    >
                      <input
                        type="number"
                        value={formData.warn_replication_lag_seconds}
                        onChange={(e) => setFormData({ ...formData, warn_replication_lag_seconds: e.target.value })}
                        min={1}
                        placeholder="e.g. 10"
                        className="input"
                      />
                    </FormField>
                    <FormField
                      label="Max replication lag (s)"
                      error={errors.max_replication_lag_seconds}
                      infoTip="Optional. Fails the check when lag exceeds this — or when replication status can't be read at all (missing role, standalone, no primary) — and triggers availability alerts."
                    >
                      <input
                        type="number"
                        value={formData.max_replication_lag_seconds}
                        onChange={(e) => setFormData({ ...formData, max_replication_lag_seconds: e.target.value })}
                        min={1}
                        placeholder="e.g. 30"
                        className="input"
                      />
                    </FormField>
                  </div>
                )}
              </div>
            )}
          </div>
        )}
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <FormField
            label="Warn latency (ms)"
            error={errors.warn_latency_ms}
            infoTip="Optional. Flags the check with a warning above this round-trip time — without failing it."
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
            label="Max latency (ms)"
            error={errors.max_latency_ms}
            infoTip="Optional. Fails the check if the round trip takes longer than this — triggers availability alerts."
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
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <FormField label="Interval (seconds)">
            <input
              type="number"
              value={formData.interval_seconds}
              onChange={(e) => setFormData({ ...formData, interval_seconds: parseInt(e.target.value, 10) || 60 })}
              min={10}
              step={5}
              className="input"
            />
          </FormField>
          <FormField label="Timeout (seconds)" error={errors.timeout_seconds}>
            <input
              type="number"
              value={formData.timeout_seconds}
              onChange={(e) => setFormData({ ...formData, timeout_seconds: parseInt(e.target.value, 10) || 10 })}
              min={1}
              className="input"
            />
            <p className="mt-1 text-xs text-slate-500">Must be less than interval</p>
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
          dependencySuppression={formData.dependency_suppression}
          hasDependencies={(monitor?.depends_on_ids?.length ?? 0) > 0}
          onDependencySuppressionChange={(d) => setFormData({ ...formData, dependency_suppression: d })}
          customChannels={formData.notification_channels}
          onCustomChannelsChange={(next) => setFormData({ ...formData, notification_channels: next })}
        />
      </FormSection>

      <FormSection title="Meta">
        <FormField label="Tags">
          <input
            type="text"
            value={formData.tags}
            onChange={(e) => setFormData({ ...formData, tags: e.target.value })}
            placeholder={`production, ${type}, critical`}
            className="input"
          />
          <p className="mt-1 text-xs text-slate-500">Comma-separated tags for filtering</p>
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
          label: loading ? 'Saving…' : isEditMode ? 'Save changes' : 'Create monitor',
          loading,
          disabled: loading,
          type: 'submit',
        }}
      />
    </form>
  );
}
