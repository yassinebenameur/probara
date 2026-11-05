import { CreateMonitorRequest, GRPCMonitorConfig, Monitor, TCPMonitorConfig } from '../types';
import { buildScheduledMonitorRequest, initialScheduledFormFields } from './common';
import { isSensitiveHeaderName } from './http';
import { SecretKV, plainRowsFromMap, secretRowsError, secretRowsFromMap, splitSecretRows } from './secret-map';

// GrpcForm's and TcpForm's state and requests.

// gRPC metadata: plain `metadata` beside the secret map `secret_metadata`
// (shared/secrets MonitorSecretMapFields). Reads mask secret values as "***";
// a submitted secret map is edited per key, an absent one keeps them all, so
// a form that loaded stored secret metadata always submits the map.
// Credential-named keys (authorization, x-api-key, …: the header rule) are
// always secret, as in the HTTP form.

/** The metadata rows of a loaded gRPC config (stored values only on edit). */
export function grpcMetadataRows(config: GRPCMonitorConfig | undefined, isEditMode: boolean): SecretKV[] {
  return [...plainRowsFromMap(config?.metadata), ...secretRowsFromMap(config?.secret_metadata, isEditMode)];
}

/** The first problem with the metadata rows, or undefined. */
export function grpcMetadataError(rows: SecretKV[]): string | undefined {
  const error = secretRowsError(rows, 'Metadata entries', isSensitiveHeaderName);
  if (error) return error;
  const reserved = rows.find((row) => row.key.trim().toLowerCase().startsWith('grpc-'));
  if (reserved) return `"${reserved.key.trim()}" is reserved: grpc- metadata keys belong to the protocol`;
  return undefined;
}

/** GrpcForm's initial state: the stored monitor on edit, else the clone source. */
export function initialGrpcFormState(monitor?: Monitor, initialData?: CreateMonitorRequest) {
  const isEditMode = Boolean(monitor);
  const initialGrpcConfig =
    !isEditMode && initialData?.type === 'grpc' ? (initialData.config as GRPCMonitorConfig) : undefined;

  return {
    ...initialScheduledFormFields(monitor, initialData),
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
    metadata: grpcMetadataRows(
      monitor && monitor.type === 'grpc' ? (monitor.config as GRPCMonitorConfig) : initialGrpcConfig,
      isEditMode
    ),
    secret_metadata_stored:
      isEditMode && monitor?.type === 'grpc'
        ? Object.keys((monitor.config as GRPCMonitorConfig)?.secret_metadata || {}).length > 0
        : false,
  };
}

export type GrpcFormState = ReturnType<typeof initialGrpcFormState>;

/** The request GrpcForm submits. */
export function buildGrpcRequest(form: GrpcFormState): CreateMonitorRequest {
  const config: GRPCMonitorConfig = {
    host: form.host.trim(),
    port: form.port,
    use_tls: form.use_tls,
  };

  if (form.service.trim()) {
    config.service = form.service.trim();
  }

  const { plain, secret } = splitSecretRows(form.metadata, isSensitiveHeaderName);
  if (Object.keys(plain).length > 0) config.metadata = plain;
  if (Object.keys(secret).length > 0 || form.secret_metadata_stored) config.secret_metadata = secret;

  return buildScheduledMonitorRequest('grpc', config, form);
}

/** TcpForm's initial state: the stored monitor on edit, else the clone source. */
export function initialTcpFormState(monitor?: Monitor, initialData?: CreateMonitorRequest) {
  const isEditMode = Boolean(monitor);
  const initialTcpConfig =
    !isEditMode && initialData?.type === 'tcp' ? (initialData.config as TCPMonitorConfig) : undefined;
  const existingConfig = monitor && monitor.type === 'tcp' ? (monitor.config as TCPMonitorConfig) : undefined;

  return {
    ...initialScheduledFormFields(monitor, initialData),
    host: existingConfig?.host ?? initialTcpConfig?.host ?? '',
    port: existingConfig?.port ?? initialTcpConfig?.port ?? 0,
    use_tls: existingConfig?.use_tls ?? initialTcpConfig?.use_tls ?? false,
    tls_skip_verify: existingConfig?.tls_skip_verify ?? initialTcpConfig?.tls_skip_verify ?? false,
    tls_min_days_valid: String(existingConfig?.tls_min_days_valid ?? initialTcpConfig?.tls_min_days_valid ?? ''),
    tls_server_name: existingConfig?.tls_server_name ?? initialTcpConfig?.tls_server_name ?? '',
    tls_ca_pem: existingConfig?.tls_ca_pem ?? initialTcpConfig?.tls_ca_pem ?? '',
    // Chain-wide expiry is on for new monitors; a stored or cloned config
    // keeps what it has (absent: the leaf only).
    tls_check_chain: (existingConfig ?? initialTcpConfig)
      ? Boolean((existingConfig ?? initialTcpConfig)?.tls_check_chain)
      : true,
  };
}

export type TcpFormState = ReturnType<typeof initialTcpFormState>;

/** `14` → 14; anything but a whole number of days → null. */
export function parseTcpMinDays(s: string): number | null {
  return /^\d+$/.test(s.trim()) ? parseInt(s.trim(), 10) : null;
}

/**
 * The request TcpForm submits. The TLS options only apply over TLS (the API
 * refuses them on a plain connect), so turning TLS off drops them.
 */
export function buildTcpRequest(form: TcpFormState): CreateMonitorRequest {
  const config: TCPMonitorConfig = {
    host: form.host.trim(),
    port: form.port,
    use_tls: form.use_tls,
  };

  if (form.use_tls) {
    if (form.tls_skip_verify) config.tls_skip_verify = true;
    if (form.tls_server_name.trim()) config.tls_server_name = form.tls_server_name.trim();
    if (form.tls_ca_pem.trim()) config.tls_ca_pem = form.tls_ca_pem.trim();
    const minDays = parseTcpMinDays(form.tls_min_days_valid);
    if (minDays !== null) {
      config.tls_min_days_valid = minDays;
      if (form.tls_check_chain) config.tls_check_chain = true;
    }
  }

  return buildScheduledMonitorRequest('tcp', config, form);
}
