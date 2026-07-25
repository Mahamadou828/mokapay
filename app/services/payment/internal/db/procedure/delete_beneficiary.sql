-- Soft delete, owner-scoped: only the client removes their own recipient.
CREATE OR REPLACE FUNCTION payments_ext.delete_beneficiary(
    p_id     UUID,
    p_client UUID
) RETURNS VOID
LANGUAGE sql AS $$
    UPDATE beneficiaries SET deleted_at = now()
    WHERE id = p_id AND client_nk = p_client AND deleted_at IS NULL;
$$;
