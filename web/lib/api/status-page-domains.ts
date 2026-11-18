import type { StatusPageDomain, StatusPageDomainListResponse } from '../types';
import { apiRequest, withQuery } from './core';

// Status page custom domains

export async function getStatusPageDomains(statusPageId: string): Promise<StatusPageDomainListResponse> {
  const response = await apiRequest<StatusPageDomainListResponse>(
    'GET',
    withQuery('/v1/status-page-domains', { status_page_id: statusPageId })
  );
  return { items: response?.items || [], enabled: response?.enabled ?? false, cname_target: response?.cname_target };
}

export async function addStatusPageDomain(statusPageId: string, hostname: string): Promise<StatusPageDomain> {
  return apiRequest<StatusPageDomain>('POST', '/v1/status-page-domains', {
    status_page_id: statusPageId,
    hostname,
  });
}

export async function verifyStatusPageDomain(id: string): Promise<StatusPageDomain> {
  return apiRequest<StatusPageDomain>('POST', `/v1/status-page-domains/${id}/verify`);
}

export async function removeStatusPageDomain(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/status-page-domains/${id}`);
}
