WITH required(name) AS (
    VALUES ('accounts'), ('revisions'), ('conversations'), ('conversation_revisions'),
    ('messages'), ('message_revisions'), ('outbox'), ('operations'), ('accepted_direct_history'),
    ('message_star_revisions'), ('message_stars'), ('archive_aliases'), ('message_references'),
    ('message_replies'), ('message_sender_identities'), ('message_retractions'), ('room_message_moderation'),
    ('message_edit_commands'), ('message_correction_facts'), ('message_canonical_aliases'), ('expired_message_ids'),
    ('expired_message_references'), ('staged_attachments'), ('avatars'), ('avatar_revisions'), ('contacts'),
    ('contact_revisions'), ('account_communication_preferences'), ('conversation_read_revisions'),
    ('conversation_read_states'), ('message_reads'), ('message_previews'), ('preview_revisions'), ('media_files'),
    ('device_downloads'), ('download_devices'), ('shared_content'), ('shared_content_state')
)
SELECT NOT EXISTS (
    SELECT 1 FROM required r LEFT JOIN main.sqlite_schema d ON d.name=r.name AND d.type='table'
    LEFT JOIN backup.sqlite_schema b ON b.name=r.name AND b.type='table'
    WHERE d.sql IS NULL OR b.sql IS NULL OR (b.sql IS NOT d.sql AND NOT (
        r.name='account_communication_preferences' AND b.sql=replace(
          d.sql, ', send_read_receipts INTEGER NOT NULL DEFAULT 1 CHECK (send_read_receipts IN (0, 1))', ''
        )
        OR r.name='conversation_read_revisions' AND b.sql=replace(d.sql, ', displayed_policy_epoch TEXT', '')
    ))
);
