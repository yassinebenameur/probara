import type {
  AlertChannel,
  AlertChannelListResponse,
  CreateAlertChannelRequest,
  NotificationSettings,
  PluginManifest,
  UpdateAlertChannelRequest,
} from '../types';
import { apiRequest, withQuery } from './core';

// Alert Channel API functions
export async function getAlertChannels(params?: {
  page?: number;
  page_size?: number;
}): Promise<AlertChannelListResponse> {
  return apiRequest<AlertChannelListResponse>(
    'GET',
    withQuery('/v1/alert-channels', {
      page: params?.page,
      page_size: params?.page_size,
    })
  );
}

export async function getAlertChannel(id: string): Promise<AlertChannel> {
  return apiRequest<AlertChannel>('GET', `/v1/alert-channels/${id}`);
}

export async function createAlertChannel(
  data: CreateAlertChannelRequest
): Promise<AlertChannel> {
  return apiRequest<AlertChannel>('POST', '/v1/alert-channels', data);
}

export async function updateAlertChannel(
  id: string,
  data: UpdateAlertChannelRequest
): Promise<AlertChannel> {
  return apiRequest<AlertChannel>('PATCH', `/v1/alert-channels/${id}`, data);
}

export async function deleteAlertChannel(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/alert-channels/${id}`);
}

export async function testAlertChannel(id: string): Promise<void> {
  return apiRequest<void>('POST', `/v1/alert-channels/${id}/test`);
}

export async function getAlertChannelPlugins(): Promise<PluginManifest[]> {
  return apiRequest<PluginManifest[]>('GET', '/v1/alert-channel-plugins');
}

// Notification Settings API functions
export async function getNotificationSettings(): Promise<NotificationSettings> {
  return apiRequest<NotificationSettings>('GET', '/v1/notification-settings');
}

export async function updateNotificationSettings(
  data: Partial<NotificationSettings>
): Promise<NotificationSettings> {
  return apiRequest<NotificationSettings>('PUT', '/v1/notification-settings', data);
}
