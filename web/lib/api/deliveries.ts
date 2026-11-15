import type { DeliveryOutcome, NotificationDeliveryPage } from '../types';
import { apiRequest, withQuery } from './core';

// Notification delivery history: every attempt to deliver an alert
// notification, kept 90 days.

/** One alert's attempts across its channels, oldest first. */
export async function getAlertDeliveries(
  alertId: string,
  params?: { page?: number; page_size?: number }
): Promise<NotificationDeliveryPage> {
  return apiRequest<NotificationDeliveryPage>(
    'GET',
    withQuery(`/v1/alerts/${alertId}/deliveries`, { page: params?.page, page_size: params?.page_size })
  );
}

/** One channel's attempts, test sends included, newest first. */
export async function getChannelDeliveries(
  channelId: string,
  params?: { outcome?: DeliveryOutcome[]; page?: number; page_size?: number }
): Promise<NotificationDeliveryPage> {
  return apiRequest<NotificationDeliveryPage>(
    'GET',
    withQuery(`/v1/alert-channels/${channelId}/deliveries`, {
      outcome: params?.outcome?.length ? params.outcome.join(',') : undefined,
      page: params?.page,
      page_size: params?.page_size,
    })
  );
}
