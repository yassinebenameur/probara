import type {
  AILabel,
  CreateIncidentRequest,
  CreateIncidentTimelineEntryRequest,
  IncidentAIAnalysis,
  IncidentAILabelRequest,
  IncidentDetail,
  IncidentListResponse,
  IncidentState,
  PublishIncidentToStatusPageRequest,
} from '../types';
import { apiRequest, withQuery } from './core';

// Incident API functions
export async function getIncidents(params?: {
  page?: number;
  page_size?: number;
}): Promise<IncidentListResponse> {
  return apiRequest<IncidentListResponse>(
    'GET',
    withQuery('/v1/incidents', {
      page: params?.page,
      page_size: params?.page_size,
    })
  );
}

export async function getIncident(id: string): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('GET', `/v1/incidents/${id}`);
}

export async function createIncident(data: CreateIncidentRequest): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('POST', '/v1/incidents', data);
}

// requestIncidentAIAnalysis enqueues a local AI root cause analysis and returns
// the pending record. Throws on 503 when local AI is disabled for the workspace
// or installation, or when the analysis queue is not configured or the enqueue
// fails.
export async function requestIncidentAIAnalysis(id: string): Promise<IncidentAIAnalysis> {
  return apiRequest<IncidentAIAnalysis>('POST', `/v1/incidents/${id}/ai-analysis`);
}

// getIncidentAIAnalysis returns the latest analysis, or null when none exists.
export async function getIncidentAIAnalysis(id: string): Promise<IncidentAIAnalysis | null> {
  return apiRequest<IncidentAIAnalysis | null>('GET', `/v1/incidents/${id}/ai-analysis`);
}

// Confirms or corrects a ready analysis as a training label (a write: viewers
// and read-scope keys get 403). 409 when the analysis is not labelable.
export async function labelIncidentAIAnalysis(incidentId: string, req: IncidentAILabelRequest): Promise<AILabel> {
  return apiRequest<AILabel>('POST', `/v1/incidents/${incidentId}/ai-analysis/label`, req);
}

export async function transitionIncidentState(id: string, state: IncidentState): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('POST', `/v1/incidents/${id}/state`, { state });
}

export async function addIncidentTimelineEntry(
  id: string,
  data: CreateIncidentTimelineEntryRequest
): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('POST', `/v1/incidents/${id}/timeline`, data);
}

export async function attachIncidentMonitor(id: string, monitorId: string): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('POST', `/v1/incidents/${id}/monitors`, { monitor_id: monitorId });
}

export async function detachIncidentMonitor(id: string, monitorId: string): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('DELETE', `/v1/incidents/${id}/monitors/${monitorId}`);
}

export async function attachIncidentAlert(id: string, alertId: string): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('POST', `/v1/incidents/${id}/alerts`, { alert_id: alertId });
}

export async function detachIncidentAlert(id: string, alertId: string): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('DELETE', `/v1/incidents/${id}/alerts/${alertId}`);
}

export async function publishIncidentToStatusPage(
  id: string,
  statusPageId: string,
  data: PublishIncidentToStatusPageRequest
): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('PUT', `/v1/incidents/${id}/status-pages/${statusPageId}`, data);
}

export async function unpublishIncidentFromStatusPage(id: string, statusPageId: string): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('DELETE', `/v1/incidents/${id}/status-pages/${statusPageId}`);
}
