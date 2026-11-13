import type {
  AILabel,
  DependencyGraph,
  DependencyLabelRequest,
  DependencyMonitor,
  DependencySuggestionResult,
} from '../types';
import { apiRequest } from './core';

// Dependency API functions
export async function getDependencyGraph(): Promise<DependencyGraph> {
  return apiRequest<DependencyGraph>('GET', '/v1/monitors/dependency-graph');
}

export async function getMonitorDependencies(id: string): Promise<{ items: DependencyMonitor[] }> {
  return apiRequest<{ items: DependencyMonitor[] }>('GET', `/v1/monitors/${id}/dependencies`);
}

export async function getMonitorDependents(id: string): Promise<{ items: DependencyMonitor[] }> {
  return apiRequest<{ items: DependencyMonitor[] }>('GET', `/v1/monitors/${id}/dependents`);
}

export async function addMonitorDependency(id: string, dependsOnId: string): Promise<void> {
  return apiRequest<void>('POST', `/v1/monitors/${id}/dependencies`, { depends_on_id: dependsOnId });
}

export async function suggestDependencies(): Promise<DependencySuggestionResult> {
  return apiRequest<DependencySuggestionResult>('POST', '/v1/monitors/dependency-suggestions');
}

// Records the operator's verdict on a dependency suggestion as a training
// label. The server recomputes the pair evidence; only the verdict is sent.
export async function labelDependencySuggestion(req: DependencyLabelRequest): Promise<AILabel> {
  return apiRequest<AILabel>('POST', '/v1/monitors/dependency-suggestions/label', req);
}

export async function removeMonitorDependency(id: string, dependsOnId: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/monitors/${id}/dependencies/${dependsOnId}`);
}
