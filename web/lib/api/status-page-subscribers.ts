import type {
  RemoveStatusPageSubscribersRequest,
  StatusPageSubscriberListResponse,
  StatusPageSubscriberSummary,
} from '../types';
import { apiDownload, apiRequest, withQuery } from './core';

// Status page email subscribers. Visitors subscribe on the public page;
// the operator lists, removes and exports them.

export async function getStatusPageSubscribers(
  id: string,
  params?: { page?: number; page_size?: number }
): Promise<StatusPageSubscriberListResponse> {
  return apiRequest<StatusPageSubscriberListResponse>(
    'GET',
    withQuery(`/v1/status-pages/${id}/subscribers`, { page: params?.page, page_size: params?.page_size })
  );
}

export async function removeStatusPageSubscriber(id: string, subscriberId: string): Promise<void> {
  return apiRequest<void>('POST', `/v1/status-pages/${id}/subscribers/${subscriberId}/remove`);
}

export async function exportStatusPageSubscribers(id: string): Promise<{ blob: Blob; filename: string }> {
  return apiDownload(`/v1/status-pages/${id}/subscribers/export`, 'status-page-subscribers.csv');
}

/** Counts, mail readiness and recent notice outcomes; readable by every member. */
export async function getStatusPageSubscriberSummary(id: string): Promise<StatusPageSubscriberSummary> {
  return apiRequest<StatusPageSubscriberSummary>('GET', `/v1/status-pages/${id}/subscribers/summary`);
}

export async function removeStatusPageSubscribers(
  id: string,
  data: RemoveStatusPageSubscribersRequest
): Promise<{ removed: number }> {
  return apiRequest<{ removed: number }>('POST', `/v1/status-pages/${id}/subscribers/remove`, data);
}
