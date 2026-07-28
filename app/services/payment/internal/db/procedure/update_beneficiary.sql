-- Owner-scoped partial update: NULL keeps the current value. SETOF so a
-- foreign or missing id yields zero rows (→ not found) instead of a
-- NULL composite. The live (client_nk, phone) unique index still guards
-- phone collisions with another saved recipient.
CREATE OR REPLACE FUNCTION payments_ext.update_beneficiary(
    p_id       UUID,
    p_client   UUID,
    p_name     TEXT             DEFAULT NULL,
    p_phone    TEXT             DEFAULT NULL,
    p_operator payment_operator DEFAULT NULL,
    p_city     TEXT             DEFAULT NULL
) RETURNS SETOF beneficiaries
LANGUAGE sql AS $$
    UPDATE beneficiaries SET
        name     = COALESCE(p_name,     name),
        phone    = COALESCE(p_phone,    phone),
        operator = COALESCE(p_operator, operator),
        city     = COALESCE(p_city,     city)
    WHERE id = p_id AND client_nk = p_client AND deleted_at IS NULL
    RETURNING *;
$$;
