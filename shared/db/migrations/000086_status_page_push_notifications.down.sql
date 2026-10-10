-- 000086_status_page_push_notifications.down.sql
DROP INDEX IF EXISTS idx_monitor_state_intervals_notifiable;
DROP TABLE IF EXISTS status_page_push_deliveries;
DROP TABLE IF EXISTS status_page_push_subscriptions;
