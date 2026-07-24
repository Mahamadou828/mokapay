CREATE OR REPLACE FUNCTION files_ext.add_file(
    p_owner_nk     UUID,
    p_purpose      file_purpose,
    p_bucket       TEXT,
    p_storage_key  TEXT,
    p_file_name    TEXT,
    p_content_type TEXT,
    p_size_bytes   BIGINT,
    p_related_type TEXT DEFAULT NULL,
    p_related_nk   UUID DEFAULT NULL
) RETURNS files
LANGUAGE sql AS $$
    INSERT INTO files (
            owner_nk,
            purpose,
            related_type,
            related_nk,
            bucket,
            storage_key,
            file_name,
            content_type,
            size_bytes
    )
    VALUES (
            p_owner_nk,
            p_purpose,
            p_related_type,
            p_related_nk,
            p_bucket,
            p_storage_key,
            p_file_name,
            p_content_type,
            p_size_bytes)
    RETURNING *;
$$;
