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
  starts_at: string;
  ends_at: string;
  monitor_ids: string[];
  monitors?: MaintenanceWindowMonitorRef[];
  status: MaintenanceWindowStatus;
  created_at: string;
  updated_at: string;
}

export interface MaintenanceWindowListResponse {
  items: MaintenanceWindow[];
  page: number;
  page_size: number;
  total: number;
}

export interface CreateMaintenanceWindowRequest {
  title: string;
  description?: string;
  starts_at: string;
  ends_at: string;
  monitor_ids: string[];
}

export interface UpdateMaintenanceWindowRequest {
  title?: string;
  description?: string;
  starts_at?: string;
  ends_at?: string;
  monitor_ids?: string[];
}

export interface SnoozeMonitorRequest {
  until?: string;
  duration_minutes?: number;
}
