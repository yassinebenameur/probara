import type { Monitor } from '../types';
import type {
  MonitorVersion,
  MonitorVersionDiff,
  MonitorVersionListResponse,
  RestoreMonitorVersionResponse,
} from '../monitor-versions';
import { apiRequest, withQuery } from './core';

// Monitor configuration history: list, read, compare and restore versions.

export async function getMonitorVersions(
  id: string,
  params?: { page?: number; page_size?: number },
): Promise<MonitorVersionListResponse> {
  return apiRequest<MonitorVersionListResponse>(
    'GET',
    withQuery(`/v1/monitors/${id}/versions`, { page: params?.page, page_size: params?.page_size }),
  );
}

export async function getMonitorVersion(id: string, version: number): Promise<MonitorVersion> {
  return apiRequest<MonitorVersion>('GET', `/v1/monitors/${id}/versions/${version}`);
}

/** Changes from `against` (default: the previous version; "current": the live monitor) to `version`. */
export async function diffMonitorVersion(
  id: string,
  version: number,
  against?: number | 'current',
): Promise<MonitorVersionDiff> {
  return apiRequest<MonitorVersionDiff>(
    'GET',
    withQuery(`/v1/monitors/${id}/versions/${version}/diff`, { against }),
  );
}

export async function restoreMonitorVersion(
  id: string,
  version: number,
): Promise<RestoreMonitorVersionResponse<Monitor>> {
  return apiRequest<RestoreMonitorVersionResponse<Monitor>>('POST', `/v1/monitors/${id}/versions/${version}/restore`);
}
