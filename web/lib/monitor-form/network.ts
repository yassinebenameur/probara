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
  };
}

export type TcpFormState = ReturnType<typeof initialTcpFormState>;

/** The request TcpForm submits. Skip-verify only applies over TLS. */
export function buildTcpRequest(form: TcpFormState): CreateMonitorRequest {
  const config: TCPMonitorConfig = {
    host: form.host.trim(),
    port: form.port,
    use_tls: form.use_tls,
  };

  if (form.use_tls && form.tls_skip_verify) {
    config.tls_skip_verify = true;
  }

  return buildScheduledMonitorRequest('tcp', config, form);
}
