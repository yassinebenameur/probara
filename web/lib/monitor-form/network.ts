import { CreateMonitorRequest, GRPCMonitorConfig, Monitor, TCPMonitorConfig } from '../types';
import { buildScheduledMonitorRequest, initialScheduledFormFields } from './common';

// GrpcForm's and TcpForm's state and requests.

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
