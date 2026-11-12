import type { MonitorType } from './monitor-configs';
import type { MonitorState } from './monitors';

// Dependency graph types
export interface DependencyMonitor {
  id: string;
  name: string;
  type: MonitorType;
  current_state: MonitorState;
  last_state_change_at?: string;
}

export interface DependencyGraphEdge {
  from: string; // downstream monitor (depends on `to`)
  to: string; // upstream monitor
}

export interface DependencyGraph {
  nodes: DependencyMonitor[];
  edges: DependencyGraphEdge[];
}
