-- Soft delete, owner-scoped: only the agent removes their own wallet.
CREATE OR REPLACE FUNCTION payments_ext.delete_agent_wallet(
    p_id    UUID,
    p_agent UUID
) RETURNS VOID
LANGUAGE sql AS $$
    UPDATE agent_wallets SET deleted_at = now()
    WHERE id = p_id AND agent_profile_fk = p_agent AND deleted_at IS NULL;
$$;
