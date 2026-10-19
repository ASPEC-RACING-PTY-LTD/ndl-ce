-- Automation became a feature switch. Clusters that already have automation
-- policies keep it on, so their Automation page stays reachable.
INSERT INTO features (cluster_id, id, enabled, package_status, runtime_status, reason, updated_at)
SELECT DISTINCT p.cluster_id, 'automation', true, 'not_configured', 'not_started',
       'Automation is optional. Flows only run once they are published and turned on; disabling it pauses every flow and deletes none.', now()
FROM policies p
ON CONFLICT (cluster_id, id) DO NOTHING;
