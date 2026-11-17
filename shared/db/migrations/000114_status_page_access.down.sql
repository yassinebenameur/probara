-- Refuse while any page is not public: dropping the visibility column (and
-- running an older status-page image that ignores it) would publish every
-- password and private page. Make those pages public first, deliberately,
-- then roll back.
DO $$
DECLARE
    gated INTEGER;
BEGIN
    SELECT COUNT(*) INTO gated FROM status_pages WHERE visibility <> 'public';
    IF gated > 0 THEN
        RAISE EXCEPTION 'refusing to roll back 000114: % status page(s) are unlisted, password-protected or private, and would become public; set them to public first', gated;
    END IF;
END $$;

DROP TABLE IF EXISTS status_page_access_tokens;

ALTER TABLE status_pages
    DROP COLUMN IF EXISTS password_version,
    DROP COLUMN IF EXISTS password_hash,
    DROP COLUMN IF EXISTS visibility;
