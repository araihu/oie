INSERT INTO discarded_recovered_sends
SELECT s.message_id FROM backup.discarded_recovered_sends s
WHERE NOT EXISTS(SELECT 1 FROM messages m WHERE m.id=s.message_id)
ON CONFLICT(message_id) DO NOTHING;

INSERT INTO archive_held_operations
SELECT operation_id,result FROM backup.recovered_send_operations;
