import type {
  AILabel,
  Alert,
  AlertBulkResult,
  AlertListResponse,
  AlertSortColumn,
  AlertTriageLabelRequest,
} from '../types';
import { apiRequest, withQuery } from './core';

// Alert API functions
export async function getAlerts(params?: {
  status?: string;
  monitor_id?: string;
  since?: string;
  /** Server-side search: subject (monitor or mesh location name) substring, or an exact alert ID. */
  q?: string;
  page?: number;
  page_size?: number;
  /** Server-side order for paged views (default triggered_at, newest first). */
  sort?: AlertSortColumn;
  dir?: 'asc' | 'desc';
}): Promise<AlertListResponse> {
  return apiRequest<AlertListResponse>(
    'GET',
    withQuery('/v1/alerts', {
      status: params?.status,
      monitor_id: params?.monitor_id,
      since: params?.since,
      q: params?.q,
      page: params?.page,
      page_size: params?.page_size,
      sort: params?.sort,
      dir: params?.dir,
    })
  );
}

// One alert; carries triage.label_context when its triage is labelable.
export async function getAlert(id: string): Promise<Alert> {
  return apiRequest<Alert>('GET', `/v1/alerts/${id}`);
}

// Confirms or corrects an alert's ready triage as a training label (a write:
// viewers and read-scope keys get 403). 409 when the triage is not ready or
// sent the model no decisions.
export async function labelAlertTriage(alertId: string, req: AlertTriageLabelRequest): Promise<AILabel> {
  return apiRequest<AILabel>('POST', `/v1/alerts/${alertId}/triage/label`, req);
}

export async function acknowledgeAlert(id: string): Promise<Alert> {
  return apiRequest<Alert>('POST', `/v1/alerts/${id}/acknowledge`);
}

export async function resolveAlert(id: string): Promise<Alert> {
  return apiRequest<Alert>('POST', `/v1/alerts/${id}/resolve`);
}

// Bulk triage (writes; at most 500 ids). Never all-or-nothing: `items` are
// the alerts changed, `failed` the rest with a reason.
export async function bulkAcknowledgeAlerts(ids: string[]): Promise<AlertBulkResult> {
  return apiRequest<AlertBulkResult>('POST', '/v1/alerts/bulk/acknowledge', { ids });
}

export async function bulkResolveAlerts(ids: string[]): Promise<AlertBulkResult> {
  return apiRequest<AlertBulkResult>('POST', '/v1/alerts/bulk/resolve', { ids });
}
