-- +goose Up
ALTER TABLE links
    ADD COLUMN expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '30 days');

CREATE INDEX links_expires_at_idx ON links (expires_at);

-- +goose StatementBegin
-- The runtime role has no DELETE privilege. This SECURITY DEFINER function
-- exposes only bounded deletion of rows that are already expired. Rows that
-- conflict with the current request are ordered first so stale unique keys do
-- not prevent immediate recreation.
CREATE FUNCTION public.purge_expired_links(p_target_url TEXT, p_target_code TEXT)
RETURNS BIGINT
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    removed BIGINT;
BEGIN
    WITH expired AS (
        SELECT candidate.ctid
        FROM public.links AS candidate
        WHERE candidate.expires_at <= statement_timestamp()
        ORDER BY (candidate.original_url = p_target_url OR candidate.code = p_target_code) DESC,
                 candidate.expires_at
        LIMIT 1000
    )
    DELETE FROM public.links AS links
    USING expired
    WHERE links.ctid = expired.ctid;
    GET DIAGNOSTICS removed = ROW_COUNT;
    RETURN removed;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION public.purge_expired_links(TEXT, TEXT) FROM PUBLIC;

-- +goose Down
DROP FUNCTION public.purge_expired_links(TEXT, TEXT);
DROP INDEX links_expires_at_idx;
ALTER TABLE links DROP COLUMN expires_at;
