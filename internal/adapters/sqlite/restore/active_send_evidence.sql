SELECT e.id,e.state,e.at,e.payload FROM restore_send_evidence e
JOIN messages m ON m.id=e.id JOIN outbox o ON o.message_id=m.id
WHERE m.state='queued' AND o.state='queued';
