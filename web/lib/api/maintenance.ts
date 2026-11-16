import type {
  CreateMaintenanceWindowRequest,
  MaintenanceOccurrence,
  MaintenanceOccurrenceListResponse,
  MaintenanceRecurrence,
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

// The next occurrences of a weekly rule, computed without saving anything.
export async function previewMaintenanceRecurrence(
  recurrence: MaintenanceRecurrence,
  count?: number
): Promise<{ occurrences: MaintenanceOccurrence[] }> {
  return apiRequest<{ occurrences: MaintenanceOccurrence[] }>('POST', '/v1/maintenance-windows/preview', {
    recurrence,
    count,
  });
}

export async function getMaintenanceOccurrences(
  id: string,
  params?: { status?: 'upcoming' | 'past'; page?: number; page_size?: number }
): Promise<MaintenanceOccurrenceListResponse> {
  return apiRequest<MaintenanceOccurrenceListResponse>(
    'GET',
    withQuery(`/v1/maintenance-windows/${id}/occurrences`, {
      status: params?.status,
      page: params?.page,
      page_size: params?.page_size,
    })
  );
}

// Ends the occurrence in progress now; a repeating window keeps its schedule.
export async function endMaintenanceOccurrence(id: string): Promise<MaintenanceWindow> {
  return apiRequest<MaintenanceWindow>('POST', `/v1/maintenance-windows/${id}/end-occurrence`);
}

// Skips a repeating window's next occurrence; the series carries on.
export async function skipNextMaintenanceOccurrence(id: string): Promise<MaintenanceWindow> {
  return apiRequest<MaintenanceWindow>('POST', `/v1/maintenance-windows/${id}/skip-next`);
}

// Creates a single-monitor maintenance window from now until the given time.
export async function snoozeMonitor(
  monitorId: string,
  data: SnoozeMonitorRequest
): Promise<MaintenanceWindow> {
  return apiRequest<MaintenanceWindow>('POST', `/v1/monitors/${monitorId}/snooze`, data);
}
