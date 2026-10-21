'use client';

import { useRef, useState } from 'react';
import {
  Monitor,
  CreateMonitorRequest,
  UpdateMonitorRequest,
  DBQueryValueOp,
  Location,
} from '@/lib/types';
import { testMonitorConfig, TestMonitorConfigResponse } from '@/lib/api';
import {
  ConnectionMode,
  DB_TYPE_META,
  DatabaseConfig,
  DatabaseMonitorType,
  QUERY_CAPABLE_TYPES,
  buildDatabaseConfig,
  buildDatabaseRequest,
  databaseSourceConfig,
  databaseTLSMaterialRelevant,
  initialDatabaseFormState,
  storedDatabaseSecrets,
} from '@/lib/monitor-form/database';
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
import ScheduleSection from './ScheduleSection';
import DependenciesField from './DependenciesField';
import { FieldHint, FormPanel, LabelsSection, PlanSummary, PlanTarget, ToggleRow, downAfterLabel, everyLabel, revealFirstError, reportConstraintViolation, useClearErrorsOnEdit, useFollowEnabled } from './form-kit';
import { handleRovingKeyDown, SEGMENT_ITEM, SELECTED_SEGMENT } from '@/components/ui/roving';
import { FOCUS_RING, FOCUS_RING_INSET } from '@/components/ui/focus';

export type { DatabaseMonitorType } from '@/lib/monitor-form/database';

const CONNECTION_MODES: { mode: ConnectionMode; label: string }[] = [
  { mode: 'fields', label: 'Form fields' },
  { mode: 'connection_string', label: 'Connection string' },
];
const CONNECTION_MODE_IDS = CONNECTION_MODES.map((m) => m.mode);

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
  ariaLabel,
}: {
  value: string;
  onChange: (value: string) => void;
  hasStored: boolean;
  onClear?: () => void;
  placeholder: string;
  storedPlaceholder: string;
  mono?: boolean;
  error?: string;
  /** The field's name; FormField cannot link its label through this wrapper. */
  ariaLabel?: string;
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
          aria-label={ariaLabel}
          autoComplete="new-password"
          className={`input ${hasStored && !value ? '!pr-28' : '!pr-10'} ${mono ? 'font-mono' : ''}`}
        />
        <div className="absolute right-2 top-1/2 flex -translate-y-1/2 items-center gap-1">
          {hasStored && !value && (
            <span className={`flex items-center gap-1 rounded-full bg-well-hover py-0.5 pl-2 text-2xs font-medium text-slate-300 ${onClear ? 'pr-1' : 'pr-2'}`} title="A value is stored. Leave blank to keep it.">
              <KeyRound className="h-3 w-3" strokeWidth={1.75} />
              set
              {onClear && (
                <button
                  type="button"
                  onClick={onClear}
                  className={`rounded-full p-0.5 text-slate-400 transition-colors hover:bg-control-hover hover:text-white ${FOCUS_RING}`}
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
            className={`rounded-lg p-1 text-slate-400 transition-colors hover:text-white ${FOCUS_RING}`}
            aria-label={reveal ? 'Hide value' : 'Show value'}
            aria-pressed={reveal}
          >
            {reveal ? <EyeOff className="h-4 w-4" strokeWidth={1.75} /> : <Eye className="h-4 w-4" strokeWidth={1.75} />}
          </button>
        </div>
      </div>
      {error && <p className="mt-1 text-xs text-rose-300">{error}</p>}
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
  const existingConfig = databaseSourceConfig(type, monitor, initialData);

  const stored = storedDatabaseSecrets(existingConfig, isEditMode);
  const storedPassword = stored.password;
  const storedConnString = stored.connectionString;
  const storedClientKey = stored.clientKey;

  const [formData, setFormData] = useState(() => initialDatabaseFormState(type, monitor, initialData));

  // Explicitly dropped stored secrets ("Clear" on the set-chip): the field is
  // then submitted as "" (the explicit clear) instead of "***" (keep).
  const [cleared, setCleared] = useState({ password: false, clientKey: false });
  const hasStoredPassword = storedPassword && !cleared.password;
  const hasStoredClientKey = storedClientKey && !cleared.clientKey;
  const hasStoredConnString = storedConnString;

  const [errors, setErrors] = useState<Record<string, string>>({});
  useClearErrorsOnEdit(formData, setErrors);
  useFollowEnabled(monitor?.enabled, setFormData);
  const formRef = useRef<HTMLFormElement>(null);
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
  const tlsMaterialRelevant = databaseTLSMaterialRelevant(type, formData);
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

  const buildConfig = (): DatabaseConfig => buildDatabaseConfig(type, formData, stored, cleared);

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
    if (reportConstraintViolation(formRef.current)) return;
    const newErrors = validate();
    setErrors(newErrors);
    if (Object.keys(newErrors).length > 0) {
      // Open the disclosures that hold an error so it can be shown and focused.
      if (newErrors.tls_client_key_pem) setCertsOpen(true);
      if (newErrors.max_replication_lag_seconds || newErrors.warn_replication_lag_seconds) setClusterChecksOpen(true);
      revealFirstError(formRef.current);
      return;
    }

    const requestData: CreateMonitorRequest | UpdateMonitorRequest = buildDatabaseRequest(type, formData, stored, cleared);

    await onSubmit(requestData);
  };

  const planParts = (() => {
    const target = usingConnString
      ? formData.connection_string.trim() || hasStoredConnString
        ? `${meta.label} via connection string`
        : null
      : formData.host.trim()
        ? `${meta.label} ${formData.host.trim()}:${formData.port}`
        : null;
    if (!target) return [];
    const check = QUERY_CAPABLE_TYPES.includes(type) && formData.query.trim() ? 'run query' : meta.checkVerb;
    return [
      everyLabel(formData.interval_seconds),
      <PlanTarget key="t">{target}</PlanTarget>,
      !usingConnString && formData.tls_enabled && 'with TLS',
      check,
      downAfterLabel(formData.consecutive_failures_threshold),
    ];
  })();

  return (
    <form ref={formRef} onSubmit={handleSubmit} noValidate className="space-y-5">
      <FormSection title="Target">
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
          <div
            className="mb-2 flex w-fit max-w-full gap-0.5 rounded-xl bg-well p-1 shadow-[inset_0_0_0_1px_var(--glass-edge)]"
            role="radiogroup"
            aria-label="Connection mode"
            onKeyDown={(e) =>
              handleRovingKeyDown(e, CONNECTION_MODE_IDS, formData.connection_mode, (mode) =>
                setFormData({ ...formData, connection_mode: mode })
              )
            }
          >
            {CONNECTION_MODES.map(({ mode, label }) => (
              <button
                key={mode}
                type="button"
                role="radio"
                aria-checked={formData.connection_mode === mode}
                data-roving-id={mode}
                tabIndex={formData.connection_mode === mode ? 0 : -1}
                onClick={() => setFormData({ ...formData, connection_mode: mode })}
                className={`whitespace-nowrap rounded-lg px-3.5 py-1.5 text-ui font-medium ${SEGMENT_ITEM} ${FOCUS_RING} ${
                  formData.connection_mode === mode
                    ? SELECTED_SEGMENT
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
                ariaLabel="Connection string"
              />
              <p className="mt-1 text-xs text-slate-500">{meta.csHint}</p>
            </FormField>
            {meta.supportsTLSToggle && (
              <ToggleRow
                title="Skip TLS certificate verification"
                description="Accept self-signed or mismatched certificates when the URI enables TLS"
                checked={formData.tls_skip_verify}
                onChange={(v) => setFormData({ ...formData, tls_skip_verify: v })}
                tone="warning"
              />
            )}
          </>
        ) : (
          <>
            <div className="grid grid-cols-[1fr_120px] gap-4">
              <FormField label="Host" required error={errors.host}>
                {(control) => (
                  <>
                    <input
                      {...control}
                      type="text"
                      value={formData.host}
                      onChange={(e) => setFormData({ ...formData, host: e.target.value })}
                      placeholder={meta.hostPlaceholder}
                      className="input"
                    />
                    <FieldHint>{meta.label} hostname or IP</FieldHint>
                  </>
                )}
              </FormField>
              <FormField label="Port" error={errors.port}>
                {(control) => (
                  <>
                    <input
                      {...control}
                      type="number"
                      value={formData.port}
                      onChange={(e) => setFormData({ ...formData, port: parseInt(e.target.value, 10) || meta.defaultPort })}
                      min={1}
                      max={65535}
                      className="input"
                    />
                    <FieldHint>Default: {meta.defaultPort}</FieldHint>
                  </>
                )}
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
                  ariaLabel="Password"
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
                {(control) => (
                  <>
                    <input
                      {...control}
                      type="number"
                      value={formData.db_index}
                      onChange={(e) => setFormData({ ...formData, db_index: parseInt(e.target.value, 10) || 0 })}
                      min={0}
                      max={15}
                      className="input"
                    />
                    <FieldHint>0–15, default 0</FieldHint>
                  </>
                )}
              </FormField>
            )}

            {type === 'postgres' && (
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <FormField label="Database">
                  {(control) => (
                    <>
                      <input
                        {...control}
                        type="text"
                        value={formData.database}
                        onChange={(e) => setFormData({ ...formData, database: e.target.value })}
                        placeholder="postgres"
                        className="input"
                      />
                      <FieldHint>Default: postgres</FieldHint>
                    </>
                  )}
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
                  {(control) => (
                    <>
                      <input
                        {...control}
                        type="text"
                        value={formData.auth_source}
                        onChange={(e) => setFormData({ ...formData, auth_source: e.target.value })}
                        placeholder="admin"
                        className="input"
                      />
                      <FieldHint>Database to authenticate against. Default: admin</FieldHint>
                    </>
                  )}
                </FormField>
                <FormField label="Replica set (optional)">
                  {(control) => (
                    <>
                      <input
                        {...control}
                        type="text"
                        value={formData.replica_set}
                        onChange={(e) => setFormData({ ...formData, replica_set: e.target.value })}
                        placeholder="rs0"
                        className="input"
                      />
                      <FieldHint>Discover the topology and assert a reachable primary</FieldHint>
                    </>
                  )}
                </FormField>
              </div>
            )}

            {type === 'rabbitmq' && (
              <FormField label="Virtual host">
                {(control) => (
                  <>
                    <input
                      {...control}
                      type="text"
                      value={formData.vhost}
                      onChange={(e) => setFormData({ ...formData, vhost: e.target.value })}
                      placeholder="/"
                      className="input"
                    />
                    <FieldHint>Default: /</FieldHint>
                  </>
                )}
              </FormField>
            )}

            {type === 'mysql' && (
              <FormField label="Database (optional)">
                {(control) => (
                  <>
                    <input
                      {...control}
                      type="text"
                      value={formData.database}
                      onChange={(e) => setFormData({ ...formData, database: e.target.value })}
                      placeholder="app"
                      className="input"
                    />
                    <FieldHint>Default schema to select after connecting</FieldHint>
                  </>
                )}
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
                    tone="warning"
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
              className={`flex w-full items-center justify-between gap-3 rounded-[inherit] px-4 py-3 text-left transition-colors hover:bg-well-hover ${FOCUS_RING_INSET}`}
              aria-expanded={certsOpen}
              aria-controls="db-tls-material"
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
                <ChevronDown className="h-4 w-4 shrink-0 text-slate-400" strokeWidth={1.75} aria-hidden="true" />
              ) : (
                <ChevronRight className="h-4 w-4 shrink-0 text-slate-400" strokeWidth={1.75} aria-hidden="true" />
              )}
            </button>
            {certsOpen && (
              <div id="db-tls-material" className="space-y-4 px-4 pb-4 pt-1">
                <FormField label="CA certificate (PEM, optional)">
                  {(control) => (
                    <>
                      <textarea
                        {...control}
                        rows={4}
                        value={formData.tls_ca_pem}
                        onChange={(e) => setFormData({ ...formData, tls_ca_pem: e.target.value })}
                        placeholder={'-----BEGIN CERTIFICATE-----\n…\n-----END CERTIFICATE-----'}
                        spellCheck={false}
                        className="input min-h-[88px] font-mono"
                      />
                      <FieldHint>Verify a server certificate signed by a private or internal CA</FieldHint>
                    </>
                  )}
                </FormField>
                <FormField label="Client certificate (PEM, optional)">
                  {(control) => (
                    <>
                      <textarea
                        {...control}
                        rows={4}
                        value={formData.tls_client_cert_pem}
                        onChange={(e) => setFormData({ ...formData, tls_client_cert_pem: e.target.value })}
                        placeholder={'-----BEGIN CERTIFICATE-----\n…\n-----END CERTIFICATE-----'}
                        spellCheck={false}
                        className="input min-h-[88px] font-mono"
                      />
                      <FieldHint>Only for mutual TLS / X.509 authentication</FieldHint>
                    </>
                  )}
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
                      aria-label="Client key (PEM)"
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

      <FormSection title="Check">
        {QUERY_CAPABLE_TYPES.includes(type) && (
          <>
            <FormField
              label="Assertion query (optional)"
              error={errors.query}
            >
              {(control) => (
                <>
                  <input
                    {...control}
                    type="text"
                    value={formData.query}
                    onChange={(e) => setFormData({ ...formData, query: e.target.value })}
                    placeholder="SELECT 1"
                    className="input font-mono"
                  />
                  <FieldHint>Runs after connecting; the check fails if it errors or returns 0 rows. Example: SELECT 1</FieldHint>
                </>
              )}
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
              className={`flex w-full items-center justify-between gap-3 rounded-[inherit] px-4 py-3 text-left transition-colors hover:bg-well-hover ${FOCUS_RING_INSET}`}
              aria-expanded={clusterChecksOpen}
              aria-controls="db-cluster-checks"
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
                <ChevronDown className="h-4 w-4 shrink-0 text-slate-400" strokeWidth={1.75} aria-hidden="true" />
              ) : (
                <ChevronRight className="h-4 w-4 shrink-0 text-slate-400" strokeWidth={1.75} aria-hidden="true" />
              )}
            </button>
            {clusterChecksOpen && (
              <div id="db-cluster-checks" className="space-y-3 px-4 pb-4 pt-1">
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
          onLocationsLoaded: setAvailableLocations,
        }}
        enabled={formData.enabled}
        onEnabledChange={(v) => setFormData((prev) => ({ ...prev, enabled: v }))}
        enabledDescription={`Run ${meta.label} checks on this schedule`}
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
        placeholder={`production, ${type}, critical`}
      />

      <FormActions
        middle={<PlanSummary parts={planParts} errors={errors} />}
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
