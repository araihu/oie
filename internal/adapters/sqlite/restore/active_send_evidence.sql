SELECT e.id,e.state,e.at,e.payload,e.timeline_at_ms,e.submitted_at_ms,COALESCE(json_extract(chat.payload,'$.Kind'),'')
FROM restore_send_evidence e
JOIN messages retained ON retained.id=e.id
JOIN conversations chat ON chat.id=retained.conversation_id
LEFT JOIN restore_pre_wire_sends active ON active.id=e.id
LEFT JOIN restore_native_unknown_sends uncertain ON uncertain.id=e.id
WHERE active.id IS NOT NULL OR (uncertain.id IS NOT NULL AND e.state='submitted');
