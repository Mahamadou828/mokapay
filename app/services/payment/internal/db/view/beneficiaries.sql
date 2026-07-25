-- Live saved recipients.
CREATE OR REPLACE VIEW payments_ext.beneficiaries AS
SELECT id,
       client_nk,
       name,
       phone,
       operator,
       city,
       created_at,
       updated_at
FROM beneficiaries
WHERE deleted_at IS NULL;
