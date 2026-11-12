ALTER TABLE outbox ADD COLUMN wire_stage TEXT NOT NULL DEFAULT 'untracked'
 CHECK(wire_stage IN ('untracked', 'awaiting', 'started'));
CREATE INDEX outbox_dispatch_due ON outbox(next_attempt_at,message_id) WHERE state='queued';
PRAGMA user_version=23;
