package appdb

// listAuditSQL is the cluster-scoped audit listing predicate.
//
// PostgreSQL types $1 as uuid when it is compared directly to cluster_id.
// The previous form `cluster_id=$1 OR ($1=” AND cluster_id IS NULL)` then
// evaluated `$1=”` as a uuid comparison and raised
// `invalid input syntax for type uuid: ""` (SQLSTATE 22P02) on every list,
// including when the bound cluster id was itself a valid UUID.
//
// Keep $1 as text. Cast only a non-empty value to uuid. Empty $1 matches
// rows that were inserted without a cluster (system / install events).
const listAuditSQL = `
SELECT a.id::text,
       COALESCE(a.cluster_id::text, ''),
       COALESCE(a.actor_user_id::text, ''),
       COALESCE(u.username, ''),
       a.action,
       a.result,
       COALESCE(a.remote_addr, ''),
       a.detail,
       a.created_at
FROM audit_events a
LEFT JOIN users u ON u.id = a.actor_user_id
WHERE CASE
  WHEN $1::text = '' THEN a.cluster_id IS NULL
  ELSE a.cluster_id = $1::uuid
END
ORDER BY a.created_at DESC
LIMIT $2`
