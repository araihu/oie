SELECT m.id FROM messages m
JOIN restore_messages x ON x.id=m.id
JOIN conversations c ON c.id=m.conversation_id
LEFT JOIN message_canonical_aliases a ON a.alias_id=m.id
LEFT JOIN message_content_heads h ON h.canonical_id=COALESCE(a.canonical_id,m.id) AND h.source_id=m.id
WHERE m.conversation_id=? AND m.is_own=0 AND json_extract(c.payload,'$.Kind')='room'
  AND (COALESCE(json_extract(m.payload,'$.ObservedRoomNickname'),'')<>''
       OR COALESCE(json_extract(h.payload,'$.ObservedRoomNickname'),'')<>'')
  AND EXISTS (SELECT 1 FROM message_replies r WHERE r.message_id=m.id);
