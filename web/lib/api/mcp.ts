import type { McpConnectionListResponse } from '../types';
import { apiRequest, getApiUrl } from './core';

// MCP: the agent endpoint and the signed-in user's OAuth connections to it.
export function getMcpEndpointUrl(): string {
  const path = getApiUrl('/v1/mcp');
  if (path.startsWith('/') && typeof window !== 'undefined') {
    return `${window.location.origin}${path}`;
  }
  return path;
}

export async function getMcpConnections(): Promise<McpConnectionListResponse> {
  return apiRequest<McpConnectionListResponse>('GET', '/v1/oauth/grants');
}

export async function revokeMcpConnection(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/oauth/grants/${id}`);
}
