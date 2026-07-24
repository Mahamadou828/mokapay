-- Flags a stored file as rejected (bad proof). The row and the object stay
-- for audit. Zero rows = not currently stored.
CREATE OR REPLACE FUNCTION files_ext.reject_file(
    p_id UUID
) RETURNS SETOF files
LANGUAGE sql AS $$
    UPDATE files SET
        status = 'rejected'
    WHERE id = p_id
      AND status = 'stored'
      AND deleted_at IS NULL
    RETURNING *;
$$;
