// Audit log types
export type AuditOutcome = "success" | "failure" | "denied";

export interface AuditEvent {
  id: number;
  occurred_at: string;
  tenant_id?: string;
  actor_type: "admin_user" | "api_key" | "anonymous";
  actor_id?: string;
  actor_label: string;
  action: string;
  resource_type: string;
  resource_id: string;
  outcome: AuditOutcome;
  status_code?: number;
  ip: string;
  user_agent: string;
  details?: Record<string, unknown>;
}

/** `GET /audit-log?sort=` columns (with `dir=asc|desc`). */
export type AuditSortColumn = 'occurred_at' | 'action' | 'resource' | 'outcome';

export interface AuditListResponse {
  items: AuditEvent[];
  page: number;
  page_size: number;
  total: number;
}
