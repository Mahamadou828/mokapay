CREATE OR REPLACE FUNCTION users_ext.add_account(
    p_phone_number       TEXT,
    p_first_name         TEXT,
    p_last_name          TEXT,
    p_email              TEXT             DEFAULT NULL,
    p_preferred_language account_language DEFAULT 'fr',
    p_role               account_role     DEFAULT 'user',
    p_status             account_status   DEFAULT 'active',
    p_cognito_sub        TEXT             DEFAULT NULL
) RETURNS accounts
LANGUAGE sql AS $$
    INSERT INTO accounts (
            phone_number,
            cognito_sub,
            first_name,
            last_name,
            email,
            preferred_language,
            role,
            status
    )
    VALUES (
            p_phone_number,
            p_cognito_sub,
            p_first_name,
            p_last_name,
            p_email,
            COALESCE(p_preferred_language, 'fr'),
            COALESCE(p_role, 'user'),
            COALESCE(p_status, 'active'))
    RETURNING *;
$$;
