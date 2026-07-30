-- Opens an investigation. The reference (CASE-2026-000042) comes from the
-- column default — generated here like MKP-/DEP-, never supplied by the
-- caller, so two concurrent creates cannot claim the same one.
CREATE OR REPLACE FUNCTION compliance_ext.add_case(
    p_summary TEXT DEFAULT NULL
) RETURNS cases
LANGUAGE sql AS $$
    INSERT INTO cases (summary)
    VALUES (p_summary)
    RETURNING *;
$$;
