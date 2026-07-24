-- Live files, every status — pending rows are visible so the confirm and
-- sweep paths can read them.
CREATE OR REPLACE VIEW files_ext.files AS
SELECT id,
       owner_nk,
       purpose,
       related_type,
       related_nk,
       bucket,
       storage_key,
       file_name,
       content_type,
       size_bytes,
       sha256,
       etag,
       status,
       created_at,
       updated_at
FROM files
WHERE deleted_at IS NULL;
