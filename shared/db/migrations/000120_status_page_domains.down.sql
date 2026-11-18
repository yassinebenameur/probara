-- Rolling back stops every custom domain from being served; pages stay
-- reachable on the platform host.
ALTER TABLE status_page_push_subscriptions DROP COLUMN IF EXISTS origin_host;

DROP TABLE IF EXISTS status_page_domains;
