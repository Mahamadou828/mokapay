CREATE OR REPLACE FUNCTION users_ext.update_account(
    p_id                 UUID,
    p_first_name         TEXT             DEFAULT NULL,
    p_last_name          TEXT             DEFAULT NULL,
    p_email              TEXT             DEFAULT NULL,
    p_preferred_language account_language DEFAULT NULL,
    p_role               account_role     DEFAULT NULL,
    p_status             account_status   DEFAULT NULL,
    p_last_login_at      TIMESTAMPTZ      DEFAULT NULL,
    p_cognito_sub        TEXT             DEFAULT NULL
) RETURNS accounts
LANGUAGE sql AS $$
    UPDATE accounts SET
        cognito_sub        = COALESCE(p_cognito_sub,        cognito_sub),
        first_name         = COALESCE(p_first_name,         first_name),
        last_name          = COALESCE(p_last_name,          last_name),
        email              = COALESCE(p_email,              email),
        preferred_language = COALESCE(p_preferred_language, preferred_language),
        role               = COALESCE(p_role,               role),
        status             = COALESCE(p_status,             status),
        last_login_at      = COALESCE(p_last_login_at,      last_login_at)
    WHERE id = p_id AND deleted_at IS NULL
    RETURNING *;
$$;
