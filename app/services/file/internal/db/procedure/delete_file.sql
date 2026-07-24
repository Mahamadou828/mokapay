CREATE OR REPLACE FUNCTION files_ext.delete_file(
    p_id UUID
) RETURNS VOID
LANGUAGE sql AS $$
    UPDATE files SET deleted_at = now()
    WHERE id = p_id AND deleted_at IS NULL;
$$;
