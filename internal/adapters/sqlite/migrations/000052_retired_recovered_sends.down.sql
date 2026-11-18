INSERT INTO discarded_recovered_sends
SELECT message_id FROM retired_recovered_sends WHERE TRUE ON CONFLICT DO NOTHING;
DROP TABLE retired_recovered_sends;
