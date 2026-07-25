-- Upsert on the live (client, phone) pair: re-sending the same recipient
-- refreshes name/operator/city instead of erroring — CreateTransfer with
-- inline recipient details is naturally idempotent on the beneficiary.
CREATE OR REPLACE FUNCTION payments_ext.add_beneficiary(
    p_client   UUID,
    p_name     TEXT,
    p_phone    TEXT,
    p_operator payment_operator,
    p_city     TEXT DEFAULT NULL
) RETURNS beneficiaries
LANGUAGE sql AS $$
    INSERT INTO beneficiaries (client_nk, name, phone, operator, city)
    VALUES (p_client, p_name, p_phone, p_operator, p_city)
    ON CONFLICT (client_nk, phone) WHERE deleted_at IS NULL
    DO UPDATE SET name = EXCLUDED.name,
                  operator = EXCLUDED.operator,
                  city = COALESCE(EXCLUDED.city, beneficiaries.city)
    RETURNING *;
$$;
