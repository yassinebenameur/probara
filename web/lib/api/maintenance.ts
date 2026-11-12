import type {
  CreateMaintenanceWindowRequest,
  MaintenanceWindow,
  MaintenanceWindowListResponse,
  MaintenanceWindowStatus,
  SnoozeMonitorRequest,
  UpdateMaintenanceWindowRequest,
} from '../types';
import { apiRequest, withQuery } from './core';

// Maintenance window API functions
export async function getMaintenanceWindows(params?: {
  status?: MaintenanceWindowStatus;
  monitor_id?: string;
  page?: number;
  page_size?: number;
}): Promise<MaintenanceWindowListResponse> {
  return apiRequest<MaintenanceWindowListResponse>(
    'GET',
    withQuery('/v1/maintenance-windows', {
      status: params?.status,
      monitor_id: params?.monitor_id,
      page: params?.page,
      page_size: params?.page_size,
    })
  );
}

export async function createMaintenanceWindow(
  data: CreateMaintenanceWindowRequest
): Promise<MaintenanceWindow> {
  return apiRequest<MaintenanceWindow>('POST', '/v1/maintenance-windows', data);
}

export async function updateMaintenanceWindow(
  id: string,
  data: UpdateMaintenanceWindowRequest
): Promise<MaintenanceWindow> {
  return apiRequest<MaintenanceWindow>('PATCH', `/v1/maintenance-windows/${id}`, data);
}

export async function deleteMaintenanceWindow(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/maintenance-windows/${id}`);
}

// Creates a single-monitor maintenance window from now until the given time.
export async function snoozeMonitor(
  monitorId: string,
  data: SnoozeMonitorRequest
): Promise<MaintenanceWindow> {
  return apiRequest<MaintenanceWindow>('POST', `/v1/monitors/${monitorId}/snooze`, data);
}
