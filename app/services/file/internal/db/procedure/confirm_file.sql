-- Marks a pending upload as stored, recording what actually landed in
-- object storage. SETOF + the status guard make it atomic: two racing
-- confirms both pass the checks in Go, only one row update wins — zero
-- rows means already confirmed (or rejected/swept meanwhile).
CREATE OR REPLACE FUNCTION files_ext.confirm_file(
    p_id     UUID,
    p_sha256 TEXT,
    p_etag   TEXT
) RETURNS SETOF files
LANGUAGE sql AS $$
    UPDATE files SET
        status = 'stored',
        sha256 = p_sha256,
        etag   = p_etag
    WHERE id = p_id
      AND status = 'pending'
      AND deleted_at IS NULL
    RETURNING *;
$$;
