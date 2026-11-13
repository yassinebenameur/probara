import type { ApiKeyScope } from './rbac';

// API Key types
export interface ApiKey {
  id: string;
  name: string;
  key_prefix: string;
  key?: string;
  scope: ApiKeyScope;
  expires_at?: string;
  last_used_at?: string;
  created_by?: string;
  created_at: string;
  revoked_at?: string;
}

export interface ApiKeyListResponse {
  items: ApiKey[];
  page: number;
  page_size: number;
  total: number;
}

export interface CreateApiKeyRequest {
  name: string;
  scope?: ApiKeyScope;
  expires_at?: string;
}
