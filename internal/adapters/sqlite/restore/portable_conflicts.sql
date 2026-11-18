-- Suspected matches are actionable only for review. Never merge messages on
-- the basis of text/time or an unverified sender-chosen provider ID.
SELECT s.id,d.id FROM incoming.messages s
JOIN incoming.accounts a ON a.id=s.account_id AND a.protocol='xmpp'
JOIN main.accounts da ON da.protocol=a.protocol AND da.address=a.address
JOIN incoming.conversations c ON c.id=s.conversation_id
JOIN main.conversations dc ON dc.account_id=da.id AND dc.address=c.address
 AND json_extract(dc.payload,'$.Kind')=json_extract(c.payload,'$.Kind')
JOIN main.messages d ON d.conversation_id=dc.id AND d.id<>s.id AND d.is_own=0 AND s.is_own=0
 AND ((d.provider_id=s.provider_id AND s.provider_id<>'') OR
      (d.created_at_ms=s.created_at_ms AND json_extract(d.payload,'$.OriginalText')=json_extract(s.payload,'$.OriginalText')))
WHERE NOT EXISTS(SELECT 1 FROM portable_pairs matched WHERE matched.source=s.id)
AND NOT EXISTS(SELECT 1 FROM incoming.message_revisions sr JOIN main.message_revisions dr USING(id)
  WHERE sr.entity_id IN (s.id,d.id) AND dr.entity_id=sr.entity_id)
AND NOT EXISTS (
 SELECT 1 FROM portable_keys k JOIN portable_keys l
 USING(account,chat,kind,key_type,authority) WHERE k.incoming=1 AND l.incoming=0 AND k.message_id=s.id AND l.message_id=d.id
)
UNION ALL
-- A coinciding row ID alone is insufficient for independent observations.
SELECT s.id,d.id FROM incoming.messages s JOIN main.messages d ON d.id=s.id
WHERE NOT (s.is_own=1 AND d.is_own=1 AND s.provider_id=s.id AND d.provider_id=d.id
 AND EXISTS(SELECT 1 FROM incoming.message_correction_facts f WHERE f.message_id=s.id AND f.remote_id=s.id AND f.target_id<>'')
 AND EXISTS(SELECT 1 FROM main.message_correction_facts f WHERE f.message_id=d.id AND f.remote_id=d.id AND f.target_id<>''))
AND NOT EXISTS (SELECT 1 FROM incoming.message_revisions sr JOIN main.message_revisions dr ON dr.id=sr.id
  WHERE sr.entity_id=s.id AND dr.entity_id=d.id)
AND NOT EXISTS (SELECT 1 FROM portable_keys k JOIN portable_keys l
 USING(account,chat,kind,key_type,authority,remote_id) WHERE k.incoming=1 AND l.incoming=0 AND k.message_id=s.id AND l.message_id=d.id)
UNION ALL
-- A MAM match cannot reconcile incompatible room SIDs or correction targets.
SELECT p.source,p.destination FROM portable_message_matches p
JOIN incoming.messages s ON s.id=p.source JOIN main.messages d ON d.id=p.destination
WHERE p.source<>p.destination AND (
 EXISTS (
   SELECT 1 FROM portable_keys sk JOIN portable_keys dk USING(account,chat,kind,key_type,authority)
   WHERE sk.incoming=1 AND dk.incoming=0 AND sk.message_id=p.source AND dk.message_id=p.destination
     AND sk.key_type='stanza-id' AND sk.remote_id<>dk.remote_id
 )
 OR EXISTS (
   SELECT 1 FROM incoming.message_correction_facts a JOIN main.message_correction_facts b
   WHERE a.message_id=p.source AND b.message_id=p.destination AND (a.target_id<>'' OR b.target_id<>'')
     AND (a.scope<>b.scope OR a.author<>b.author OR a.remote_id<>b.remote_id OR a.target_id<>b.target_id)
 )
 OR (EXISTS (SELECT 1 FROM incoming.message_correction_facts a WHERE a.message_id=p.source AND a.target_id<>'')
     <> EXISTS (SELECT 1 FROM main.message_correction_facts b WHERE b.message_id=p.destination AND b.target_id<>''))
 OR (s.is_own=0 AND d.is_own=0 AND NOT EXISTS (
    SELECT 1 FROM incoming.message_revisions sr JOIN main.message_revisions dr
    WHERE sr.entity_id=s.id AND dr.entity_id=d.id AND
      COALESCE(NULLIF(json_extract(sr.payload,'$.WireBodySHA256'),''),oie_source_hash(json_extract(sr.payload,'$.OriginalText')))
      =COALESCE(NULLIF(json_extract(dr.payload,'$.WireBodySHA256'),''),oie_source_hash(json_extract(dr.payload,'$.OriginalText')))
 ))
)
UNION ALL
SELECT s.id,e.local_id FROM incoming.messages s
JOIN incoming.accounts a ON a.id=s.account_id AND a.protocol='xmpp'
JOIN incoming.conversations c ON c.id=s.conversation_id
JOIN main.xmpp_unidentified_removals e ON e.account=a.address AND e.chat=c.address
 AND e.kind=json_extract(c.payload,'$.Kind') AND e.created_ms=s.created_at_ms
WHERE e.local_id<>s.id AND NOT EXISTS(SELECT 1 FROM main.expired_message_ids x WHERE x.id=s.id)
 AND NOT EXISTS(SELECT 1 FROM main.messages d WHERE d.id=s.id)
 AND NOT EXISTS(SELECT 1 FROM portable_pairs p WHERE p.source=s.id)
 AND NOT EXISTS(SELECT 1 FROM incoming.expired_message_ids x WHERE x.id=e.local_id)
LIMIT 1;
