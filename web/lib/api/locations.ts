import type {
  CreateLocationRequest,
  Location,
  LocationDeployInfo,
  LocationListResponse,
  MeshProbeResponse,
  MeshResponse,
  UpdateLocationRequest,
} from '../types';
import { apiRequest, withQuery } from './core';

// Private location API functions
export async function getLocations(params?: {
  page?: number;
  page_size?: number;
}): Promise<LocationListResponse> {
  return apiRequest<LocationListResponse>(
    'GET',
    withQuery('/v1/locations', {
      page: params?.page,
      page_size: params?.page_size,
    })
  );
}

export async function createLocation(data: CreateLocationRequest): Promise<Location> {
  return apiRequest<Location>('POST', '/v1/locations', data);
}

export async function updateLocation(
  id: string,
  data: UpdateLocationRequest
): Promise<Location> {
  return apiRequest<Location>('PATCH', `/v1/locations/${id}`, data);
}

// Detaches the location from its monitors (their quorum is clamped server-side).
export async function deleteLocation(id: string): Promise<{ monitors_detached: number }> {
  return apiRequest<{ monitors_detached: number }>('DELETE', `/v1/locations/${id}`);
}

export async function getLocationDeployInfo(id: string): Promise<LocationDeployInfo> {
  return apiRequest<LocationDeployInfo>('GET', `/v1/locations/${id}/deploy`);
}

// Inter-location connectivity mesh API functions

export async function getMesh(): Promise<MeshResponse> {
  return apiRequest<MeshResponse>('GET', '/v1/mesh');
}

// Ephemeral probe of one directed edge via the source location's worker.
export async function probeMeshEdge(
  sourceId: string,
  targetId: string
): Promise<MeshProbeResponse> {
  return apiRequest<MeshProbeResponse>('POST', '/v1/mesh/probe', {
    source_location_id: sourceId,
    target_location_id: targetId,
  });
}
