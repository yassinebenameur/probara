import type {
  DiscoveryCoverage,
  DiscoveryCoverageQuery,
  DiscoveryIntegration,
  DiscoveryIntegrationPatch,
  DiscoveryNamespace,
} from '../discovery';
import { coverageQueryString } from '../discovery';
import { apiRequest } from './core';

// Discovery uses the same tenant selection, session refresh, and API-key headers.
export function getDiscoveryIntegrations() {
  return apiRequest<DiscoveryIntegration[]>('GET', '/v1/discovery/integrations');
}

export function createDiscoveryIntegration(data: { name: string; namespaces: string[]; location_id: string | null }) {
  return apiRequest<DiscoveryIntegration>('POST', '/v1/discovery/integrations', data);
}

export function updateDiscoveryIntegration(id: string, data: DiscoveryIntegrationPatch) {
  return apiRequest<DiscoveryIntegration>('PATCH', `/v1/discovery/integrations/${id}`, data);
}

export function getDiscoveryNamespaces(id: string) {
  return apiRequest<{ items: DiscoveryNamespace[] }>('GET', `/v1/discovery/integrations/${id}/namespaces`);
}

export function getDiscoveryCoverage(id: string, query: DiscoveryCoverageQuery) {
  return apiRequest<DiscoveryCoverage>('GET', `/v1/discovery/integrations/${id}/coverage?${coverageQueryString(query)}`);
}

export function rotateDiscoveryToken(id: string) {
  return apiRequest<{ token: string }>('POST', `/v1/discovery/integrations/${id}/rotate-token`);
}

export function deleteDiscoveryIntegration(id: string) {
  return apiRequest<void>('DELETE', `/v1/discovery/integrations/${id}`);
}

export function approveDiscoverySuggestion(id: string, data: { key: string; name: string; interval_seconds: number; expected_status: number; location_id: string | null; notification_mode: string; channel_ids: string[] }) {
  return apiRequest<{ monitor_id: string }>('POST', `/v1/discovery/services/${id}/approve`, data);
}

export function linkDiscoveryMonitor(id: string, monitor_id: string, key?: string) {
  return apiRequest<void>('POST', `/v1/discovery/services/${id}/links`, { monitor_id, key });
}

export function unlinkDiscoveryMonitor(id: string, monitor: string) {
  return apiRequest<void>('DELETE', `/v1/discovery/services/${id}/links/${monitor}`);
}
