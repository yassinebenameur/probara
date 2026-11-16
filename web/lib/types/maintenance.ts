// Maintenance window types
export type MaintenanceWindowStatus = 'active' | 'upcoming' | 'past';

export interface MaintenanceWindowMonitorRef {
  id: string;
  name: string;
  type: string;
}

export interface MaintenanceWindow {
  id: string;
  tenant_id: string;
  title: string;
  description: string;
  /** A one-off's span; for a repeating window, the first occurrence of its current schedule. */
  starts_at: string;
  ends_at: string;
  monitor_ids: string[];
  monitors?: MaintenanceWindowMonitorRef[];
  status: MaintenanceWindowStatus;
  /** Set for a repeating window. */
  recurrence?: MaintenanceRecurrence;
  /** The occurrence in progress, else the next one that is not skipped. */
  next_occurrence?: MaintenanceOccurrence;
  created_at: string;
  updated_at: string;
}

/**
 * Weekly repeat rule: on `weekdays` (0 = Sunday) at `start_time` local time
 * in `timezone`, for `duration_minutes`, with no occurrence on a local date
 * after `until` (YYYY-MM-DD). `frequency: 'none'` (update only) makes a
 * repeating window a one-off again.
 */
export interface MaintenanceRecurrence {
  frequency: 'weekly' | 'none';
  weekdays?: number[];
  start_time?: string;
  duration_minutes?: number;
  timezone?: string;
  until?: string;
}

export type MaintenanceOccurrenceStatus = 'active' | 'upcoming' | 'past' | 'skipped';

export interface MaintenanceOccurrence {
  starts_at: string;
  ends_at: string;
  status: MaintenanceOccurrenceStatus;
}

export interface MaintenanceOccurrenceListResponse {
  items: MaintenanceOccurrence[];
  page: number;
  page_size: number;
  total: number;
}

export interface MaintenanceWindowListResponse {
  items: MaintenanceWindow[];
  page: number;
  page_size: number;
  total: number;
}

/** A one-off sends starts_at/ends_at; a repeating window sends recurrence and no times. */
export interface CreateMaintenanceWindowRequest {
  title: string;
  description?: string;
  starts_at?: string;
  ends_at?: string;
  monitor_ids: string[];
  recurrence?: MaintenanceRecurrence;
}

export interface UpdateMaintenanceWindowRequest {
  title?: string;
  description?: string;
  starts_at?: string;
  ends_at?: string;
  monitor_ids?: string[];
  recurrence?: MaintenanceRecurrence;
}

export interface SnoozeMonitorRequest {
  until?: string;
  duration_minutes?: number;
}
