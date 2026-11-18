SELECT 'account',r.entity_id,a.revision_id,r.id,r.parents,r.created_at,r.payload
FROM revisions r JOIN restore_accounts x ON x.id=r.entity_id JOIN accounts a ON a.id=x.id
UNION ALL
SELECT 'message',r.entity_id,m.revision_id,r.id,r.parents,r.created_at,r.payload
FROM message_revisions r JOIN restore_messages x ON x.id=r.entity_id JOIN messages m ON m.id=x.id
WHERE NOT EXISTS(SELECT 1 FROM outbox o WHERE o.message_id=m.id AND o.state IN ('queued','dispatching','unknown'))
  AND m.state IN ('received','submitted')
UNION ALL
SELECT 'star',r.message_id,s.revision_id,r.id,r.parents,r.created_at,r.payload
FROM message_star_revisions r JOIN restore_messages x ON x.id=r.message_id JOIN message_stars s ON s.message_id=x.id
WHERE NOT EXISTS (
  SELECT 1 FROM restore_idless_direct_echoes e
  JOIN message_stars local ON local.message_id<>s.message_id
  LEFT JOIN message_canonical_aliases a ON a.alias_id=local.message_id
  WHERE e.source_id=s.message_id AND COALESCE(a.canonical_id,local.message_id)=e.retained_id
)
ORDER BY 1,2,4;
