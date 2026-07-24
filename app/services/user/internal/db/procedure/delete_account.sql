CREATE OR REPLACE FUNCTION users_ext.delete_account(
    p_id UUID
) RETURNS VOID
LANGUAGE sql AS $$
    UPDATE accounts SET deleted_at = now()
    WHERE id = p_id AND deleted_at IS NULL;
$$;
