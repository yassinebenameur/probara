ALTER TABLE tenants DROP COLUMN IF EXISTS personal_sms_channel_id;
DROP TABLE IF EXISTS user_contact_sends;
DROP TABLE IF EXISTS user_contacts;
