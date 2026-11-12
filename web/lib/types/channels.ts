// Alert Channel types
// Plugin types are dynamic now (registry-driven), so this is a free-form string.
export type AlertChannelType = string;

export interface AlertChannel {
  id: string;
  tenant_id: string;
  name: string;
  type: AlertChannelType;
  config: Record<string, any>;
  is_active: boolean;
  created_at: string;
  updated_at: string;
}

export interface CreateAlertChannelRequest {
  name: string;
  type: AlertChannelType;
  config: Record<string, any>;
  is_active?: boolean;
}

export interface UpdateAlertChannelRequest {
  name?: string;
  config?: Record<string, any>;
  is_active?: boolean;
}

export interface AlertChannelListResponse {
  items: AlertChannel[];
  page: number;
  page_size: number;
  total: number;
}

// Plugin manifest (mirrors shared/notifications/plugin.Manifest).
export type PluginFieldType =
  | 'string'
  | 'url'
  | 'email_list'
  | 'textarea'
  | 'secret'
  | 'bool'
  | 'select';

export type PluginCapability = 'testable' | 'acknowledge' | 'follow_up';

export interface PluginFieldOption {
  value: string;
  label: string;
}

export interface PluginField {
  key: string;
  label: string;
  placeholder?: string;
  help?: string;
  type: PluginFieldType;
  required?: boolean;
  secret?: boolean;
  default?: unknown;
  options?: PluginFieldOption[];
}

export interface PluginManifest {
  type: string;
  display_name: string;
  description: string;
  icon_key: string;
  docs_url?: string;
  version: string;
  capabilities: PluginCapability[];
  fields: PluginField[];
}
