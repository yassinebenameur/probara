import type { AuditListResponse, AuditSortColumn } from '../types';
import { apiRequest, withQuery } from './core';

export interface GetAuditLogParams {
  action?: string;
  outcome?: string;
  actor_id?: string;
  from?: string;
  to?: string;
  page?: number;
  page_size?: number;
  /** Server-side order (default occurred_at, newest first). */
  sort?: AuditSortColumn;
  dir?: 'asc' | 'desc';
}

export async function getAuditLog(params?: GetAuditLogParams): Promise<AuditListResponse> {
  return apiRequest<AuditListResponse>(
    'GET',
    withQuery('/v1/audit-log', {
      action: params?.action,
      outcome: params?.outcome,
      actor_id: params?.actor_id,
      from: params?.from,
      to: params?.to,
      page: params?.page,
      page_size: params?.page_size,
      sort: params?.sort,
      dir: params?.dir,
    })
  );
}

export async function getAuditActions(): Promise<{ actions: string[] }> {
  return apiRequest<{ actions: string[] }>('GET', '/v1/audit-log/actions');
}
