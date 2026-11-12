// Private location types
export interface Location {
  id: string;
  tenant_id: string;
  name: string;
  slug: string;
  description?: string;
  enabled: boolean;
  connected: boolean;
  last_seen_at?: string;
  // host:port other locations probe; set = participates in the mesh.
  mesh_endpoint?: string;
  monitor_count: number;
  created_at: string;
  updated_at: string;
}

export interface LocationListResponse {
  items: Location[];
  page: number;
  page_size: number;
  total: number;
}

export interface CreateLocationRequest {
  name: string;
  description?: string;
  mesh_endpoint?: string;
}

export interface UpdateLocationRequest {
  name?: string;
  description?: string;
  enabled?: boolean;
  // Empty string clears the endpoint (opts the location out of the mesh).
  mesh_endpoint?: string;
}

// Inter-location connectivity mesh

export interface MeshLocation {
  id: string;
  name: string;
  connected: boolean;
  mesh_endpoint: string;
}

export type MeshEdgeState = 'unknown' | 'up' | 'suspect' | 'down';

export interface MeshEdge {
  source_location_id: string;
  target_location_id: string;
  state: MeshEdgeState;
  last_latency_ms?: number;
  last_check_at?: string;
  last_error?: string;
  stale: boolean;
  alert_open: boolean;
}

export interface MeshResponse {
  locations: MeshLocation[];
  edges: MeshEdge[];
  probe_interval_seconds: number;
}

export interface MeshEdgeHistoryPoint {
  status: string;
  latency_ms?: number;
  created_at: string;
}

export interface MeshProbeResponse {
  status: string;
  latency_ms?: number;
  error_message?: string;
}

export interface LocationDeployInfo {
  location_id: string;
  nats_url: string;
  docker_run_command: string;
  docker_compose_yaml: string;
  env: Record<string, string>;
}

export interface MonitorLocationStatus {
  id: string;
  name: string;
  connected: boolean;
  current_state: 'unknown' | 'up' | 'suspect' | 'down';
  last_latency_ms?: number;
  last_check_at?: string;
}
