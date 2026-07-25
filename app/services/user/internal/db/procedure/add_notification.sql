CREATE OR REPLACE FUNCTION users_ext.add_notification(
    p_account    UUID,
    p_event_type TEXT,
    p_title      TEXT,
    p_body       TEXT,
    p_data       JSONB DEFAULT NULL
) RETURNS notifications
LANGUAGE sql AS $$
    INSERT INTO notifications (account_fk, event_type, title, body, data)
    VALUES (p_account, p_event_type, p_title, p_body, p_data)
    RETURNING *;
$$;
