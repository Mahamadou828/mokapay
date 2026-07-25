-- Matching inputs, derived — never stored (teardown #6). success_rate is
-- computed in Go from done/decided to keep floats out of the read model.
CREATE OR REPLACE VIEW payments_ext.agent_stats AS
SELECT ap.id AS agent_profile_fk,
       COUNT(t.id) FILTER (WHERE t.status = 'done')::BIGINT AS done_count,
       COUNT(t.id) FILTER (WHERE t.status IN ('done', 'cancelled'))::BIGINT AS decided_count,
       COUNT(t.id) FILTER (WHERE t.status IN
           ('pending_client_transfer', 'pending_agent_transfer',
            'pending_client_confirmation'))::BIGINT AS active_load,
       (SELECT COUNT(*) FROM offers o
        WHERE o.agent_profile_fk = ap.id AND o.status = 'declined')::BIGINT AS declined_count
FROM agent_profiles ap
LEFT JOIN transfers t ON t.agent_profile_fk = ap.id AND t.deleted_at IS NULL
WHERE ap.deleted_at IS NULL
GROUP BY ap.id;
