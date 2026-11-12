'use client';

import { useRef, useState } from 'react';
import { Monitor, CreateMonitorRequest, UpdateMonitorRequest, Location } from '@/lib/types';
import { useConfigTest } from './useConfigTest';
import { DB_TYPE_META, DatabaseConfig, DatabaseMonitorType, QUERY_CAPABLE_TYPES, buildDatabaseConfig, buildDatabaseRequest, databaseSourceConfig, databaseTLSMaterialRelevant, initialDatabaseFormState, storedDatabaseSecrets } from '@/lib/monitor-form/database';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import ScheduleSection from './ScheduleSection';
import { LabelsSection, PlanSummary, PlanTarget, downAfterLabel, everyLabel, revealFirstError, reportConstraintViolation, useClearErrorsOnEdit, useFollowEnabled, FormActionBar } from './form-kit';
import DatabaseTargetPanel from './database/DatabaseTargetPanel';
import DatabaseTestRow from './database/DatabaseTestRow';
import DatabaseCheckPanel from './database/DatabaseCheckPanel';
import DatabaseAlertingPanel from './database/DatabaseAlertingPanel';

export type { DatabaseMonitorType } from '@/lib/monitor-form/database';

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
  const configTest = useConfigTest({
    monitorId: isEditMode && monitor ? monitor.id : undefined,
    locationIds: formData.location_ids,
  });
  // List shared by the Locations section (via onLocationsLoaded) so the
  // test-from picker can resolve location names.
  const [availableLocations, setAvailableLocations] = useState<Location[]>([]);

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

    await configTest.run({
      type,
      config: buildConfig(),
      timeout_seconds: Math.min(formData.timeout_seconds || 10, 30),
    });
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
    <form ref={formRef} onSubmit={handleSubmit} noValidate data-form-stack className="space-y-5">
      <DatabaseTargetPanel
        type={type}
        formData={formData}
        setFormData={setFormData}
        errors={errors}
        usingConnString={usingConnString}
        hasStoredConnString={hasStoredConnString}
        hasStoredPassword={hasStoredPassword}
        hasStoredClientKey={hasStoredClientKey}
        cleared={cleared}
        setCleared={setCleared}
        tlsMaterialRelevant={tlsMaterialRelevant}
        hasTLSMaterial={hasTLSMaterial}
        certsOpen={certsOpen}
        setCertsOpen={setCertsOpen}
      >
        <DatabaseTestRow
          formData={formData}
          configTest={configTest}
          handleTestConnection={handleTestConnection}
          canTestConnection={canTestConnection}
          loading={loading}
          availableLocations={availableLocations}
        />
      </DatabaseTargetPanel>

      <DatabaseCheckPanel
        type={type}
        formData={formData}
        setFormData={setFormData}
        errors={errors}
        clusterChecksEnabled={clusterChecksEnabled}
        clusterChecksOpen={clusterChecksOpen}
        setClusterChecksOpen={setClusterChecksOpen}
      />

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

      <DatabaseAlertingPanel formData={formData} setFormData={setFormData} errors={errors} monitor={monitor} />

      <LabelsSection
        tags={formData.tags}
        onTagsChange={(v) => setFormData({ ...formData, tags: v })}
        placeholder={`production, ${type}, critical`}
      />

      <FormActionBar
        summary={<PlanSummary parts={planParts} errors={errors} />}
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
