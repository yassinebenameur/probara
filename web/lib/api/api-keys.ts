import type { ApiKey, ApiKeyListResponse, CreateApiKeyRequest } from '../types';
import { apiRequest, withQuery } from './core';

// API Key functions
export async function getApiKeys(params?: {
  page?: number;
  page_size?: number;
}): Promise<ApiKeyListResponse> {
  return apiRequest<ApiKeyListResponse>(
    'GET',
    withQuery('/v1/api-keys', {
      page: params?.page,
      page_size: params?.page_size,
    })
  );
}

export async function createApiKey(
  data: CreateApiKeyRequest
): Promise<ApiKey> {
  return apiRequest<ApiKey>('POST', '/v1/api-keys', data);
}

export async function revokeApiKey(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/api-keys/${id}`);
}
