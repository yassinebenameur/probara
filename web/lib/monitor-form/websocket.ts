import { CreateMonitorRequest, MASKED_SECRET, Monitor, WebSocketMonitorConfig } from '../types';
import { buildScheduledMonitorRequest, initialScheduledFormFields } from './common';

// WebsocketForm's state and request. Handshake headers are a secret map
// (shared/secrets MonitorSecretMapFields): reads mask every value as "***",
// and a submitted map is edited per key — "***" keeps that key's stored
// value, a key left out is removed — while an absent map keeps them all.

/**
 * One handshake header row. A row loaded with a masked value starts blank
 * with `hasStoredValue`, and keeps the stored value while its name still
 * matches `originalKey`.
 */
export type WebsocketHeaderRow = { key: string; value: string; hasStoredValue?: boolean; originalKey?: string };

/**
 * The rows for a loaded header map. Only an edit has a stored value behind a
 * mask; on create (a clone) a "***" row starts blank and must be re-entered,
 * since the API drops a mask it has nothing to resolve against.
 */
export function headersToRows(headers: Record<string, string> | undefined, isEditMode: boolean): WebsocketHeaderRow[] {
  return Object.entries(headers || {}).map(([key, value]) => {
    const masked = value === MASKED_SECRET;
    const stored = masked && isEditMode;
    return {
      key,
      value: masked ? '' : value,
      hasStoredValue: stored,
      originalKey: stored ? key : undefined,
    };
  });
}

/** A header row after its name is edited: renaming it away drops the stored value. */
export function renameWebsocketHeaderRow(current: WebsocketHeaderRow, key: string): WebsocketHeaderRow {
  const keepsStoredValue = Boolean(
    current.hasStoredValue &&
    current.originalKey &&
    current.originalKey.toLowerCase() === key.trim().toLowerCase()
  );
  return { ...current, key, hasStoredValue: keepsStoredValue };
}

/** WebsocketForm's initial state: the stored monitor on edit, else the clone source. */
export function initialWebsocketFormState(monitor?: Monitor, initialData?: CreateMonitorRequest) {
  const isEditMode = Boolean(monitor);
  const initialWSConfig =
    !isEditMode && initialData?.type === 'websocket' ? (initialData.config as WebSocketMonitorConfig) : undefined;
  const existingConfig = monitor && monitor.type === 'websocket' ? (monitor.config as WebSocketMonitorConfig) : undefined;
  const sourceConfig = existingConfig ?? initialWSConfig;

  return {
    ...initialScheduledFormFields(monitor, initialData),
    url: sourceConfig?.url ?? '',
    headers: headersToRows(sourceConfig?.headers, isEditMode),
    tls_skip_verify: sourceConfig?.tls_skip_verify ?? false,
    send_message: sourceConfig?.send_message ?? '',
    expected_substring: sourceConfig?.expected_substring ?? '',
    warn_latency_ms: sourceConfig?.warn_latency_ms ? String(sourceConfig.warn_latency_ms) : '',
    max_latency_ms: sourceConfig?.max_latency_ms ? String(sourceConfig.max_latency_ms) : '',
  };
}

export type WebsocketFormState = ReturnType<typeof initialWebsocketFormState>;

/** The request WebsocketForm submits, for a form that passed validation. */
export function buildWebsocketRequest(form: WebsocketFormState, isEditMode: boolean): CreateMonitorRequest {
  const url = form.url.trim();
  const isSecure = form.url.trim().startsWith('wss://');
  const maxLatency = form.max_latency_ms.trim() ? parseInt(form.max_latency_ms, 10) : undefined;
  const warnLatency = form.warn_latency_ms.trim() ? parseInt(form.warn_latency_ms, 10) : undefined;

  const config: WebSocketMonitorConfig = { url };
  const headers: Record<string, string> = {};
  for (const row of form.headers) {
    if (row.key.trim()) headers[row.key.trim()] = row.value || (row.hasStoredValue ? '***' : '');
  }
  // On edit, always submit the object: an absent `headers` field means "keep
  // the stored headers" server-side, so removing every row has to send an
  // empty object to clear them.
  if (Object.keys(headers).length > 0 || isEditMode) config.headers = headers;
  if (isSecure && form.tls_skip_verify) config.tls_skip_verify = true;
  if (form.send_message) config.send_message = form.send_message;
  if (form.expected_substring) config.expected_substring = form.expected_substring;
  if (maxLatency) config.max_latency_ms = maxLatency;
  if (warnLatency) config.warn_latency_ms = warnLatency;

  return buildScheduledMonitorRequest('websocket', config, form);
}
