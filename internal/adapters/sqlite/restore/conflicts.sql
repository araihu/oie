SELECT EXISTS (
    SELECT 1 FROM backup.accounts b JOIN restore_accounts x ON x.id=b.id
    JOIN accounts a ON a.id=b.id OR (a.protocol=b.protocol AND a.address=b.address)
    WHERE a.id<>b.id OR a.protocol<>b.protocol OR a.address<>b.address
UNION ALL
    SELECT 1 FROM backup.conversations b JOIN restore_chats x ON x.id=b.id
    JOIN conversations c ON c.id=b.id OR (c.account_id=b.account_id AND c.address=b.address)
    WHERE c.id<>b.id OR c.account_id<>b.account_id OR c.address<>b.address OR
        (x.promote_room=0 AND json_extract(c.payload,'$.Kind') IS NOT json_extract(b.payload,'$.Kind'))
UNION ALL
    SELECT 1 FROM backup.messages b JOIN restore_messages x ON x.id=b.id
    LEFT JOIN restore_direct_mention_echo_keys source ON source.id=b.id AND source.is_local=0
    LEFT JOIN restore_idless_direct_echoes idless ON idless.source_id=b.id
    JOIN messages m ON m.id=b.id OR (
       m.account_id=b.account_id AND m.conversation_id=b.conversation_id
       AND m.provider_id=COALESCE(source.provider_id,b.provider_id) AND m.is_own=b.is_own AND m.provider_id<>''
       AND idless.source_id IS NULL
    )
    WHERE m.account_id<>b.account_id OR m.conversation_id<>b.conversation_id OR (m.id<>b.id AND NOT EXISTS (
      SELECT 1 FROM restore_own_echo_aliases e
      WHERE (e.echo_id=m.id AND e.original_id=b.id) OR (e.echo_id=b.id AND e.original_id=m.id)
    ) AND NOT EXISTS (SELECT 1 FROM restore_direct_mention_echo_keys local WHERE local.id=m.id AND local.is_local=1))
UNION ALL
    -- A retained correction's local ID binds its authenticated wire identity.
    -- A room's native and archived forms can also share that wire identity.
    SELECT 1 FROM restore_messages selected
    JOIN backup.messages incoming ON incoming.id=selected.id AND incoming.is_own=1 AND incoming.state='submitted'
    JOIN conversations chat ON chat.id=incoming.conversation_id AND chat.account_id=incoming.account_id
    JOIN accounts account ON account.id=incoming.account_id
    JOIN backup.message_correction_facts observed ON observed.message_id=incoming.id AND observed.target_id<>''
    JOIN backup.message_revisions observed_proof ON observed_proof.id=observed.source_revision_id AND observed_proof.entity_id=incoming.id
    JOIN message_correction_facts known ON known.target_id<>''
      AND (known.message_id=incoming.id OR (
        known.scope=observed.scope AND known.author=observed.author AND known.remote_id=observed.remote_id
      ))
    JOIN messages retained ON retained.id=known.message_id
      AND retained.account_id=incoming.account_id AND retained.conversation_id=incoming.conversation_id
      AND retained.is_own=1 AND retained.state='submitted'
    JOIN message_revisions known_proof ON known_proof.id=known.source_revision_id AND known_proof.entity_id=retained.id
    WHERE (
        json_extract(chat.payload,'$.Kind')='direct'
          AND observed.scope='xmpp:correct:sender:'||account.address AND observed.author=account.address
          AND known.scope=observed.scope AND known.author=observed.author
        OR json_extract(chat.payload,'$.Kind')='room'
          AND instr(observed.scope,'xmpp:correct:room:'||chat.address||':')=1 AND instr(observed.author,chat.address||'/')=1
          AND instr(known.scope,'xmpp:correct:room:'||chat.address||':')=1 AND instr(known.author,chat.address||'/')=1
      )
      AND (
        retained.id=incoming.id AND (
          known.scope<>observed.scope OR known.author<>observed.author OR known.remote_id<>observed.remote_id
          OR known.target_id<>observed.target_id OR known.source_hash<>observed.source_hash
        )
          AND (incoming.provider_id=observed.remote_id OR (
            json_extract(chat.payload,'$.Kind')='room'
            AND incoming.provider_id=oie_room_correction_provider_id(observed.author,observed.remote_id)
          ))
          AND (retained.provider_id=known.remote_id OR (
            json_extract(chat.payload,'$.Kind')='room'
            AND retained.provider_id=oie_room_correction_provider_id(known.author,known.remote_id)
          ))
        OR selected.is_new=1 AND retained.id<>incoming.id AND json_extract(chat.payload,'$.Kind')='room'
          AND ((incoming.id=incoming.provider_id AND incoming.provider_id=observed.remote_id
                AND retained.provider_id=oie_room_correction_provider_id(known.author,known.remote_id))
               OR (retained.id=retained.provider_id AND retained.provider_id=known.remote_id
                   AND incoming.provider_id=oie_room_correction_provider_id(observed.author,observed.remote_id)))
      )
      AND observed.source_hash=COALESCE(NULLIF(json_extract(incoming.payload,'$.WireBodySHA256'),''),
                                        oie_source_hash(json_extract(incoming.payload,'$.OriginalText')))
      AND observed.source_hash=COALESCE(NULLIF(json_extract(observed_proof.payload,'$.WireBodySHA256'),''),
                                        oie_source_hash(json_extract(observed_proof.payload,'$.OriginalText')))
      AND known.source_hash=COALESCE(NULLIF(json_extract(retained.payload,'$.WireBodySHA256'),''),
                                     oie_source_hash(json_extract(retained.payload,'$.OriginalText')))
      AND known.source_hash=COALESCE(NULLIF(json_extract(known_proof.payload,'$.WireBodySHA256'),''),
                                     oie_source_hash(json_extract(known_proof.payload,'$.OriginalText')))
UNION ALL
    SELECT 1 FROM backup.archive_aliases b JOIN restore_messages x ON x.id=b.message_id
    JOIN archive_aliases d ON d.account_id=b.account_id AND d.scope=b.scope AND d.archive_id=b.archive_id
    WHERE d.message_id<>b.message_id AND NOT EXISTS (
      SELECT 1 FROM restore_own_echo_aliases e
      WHERE (e.echo_id=d.message_id AND e.original_id=b.message_id)
         OR (e.echo_id=b.message_id AND e.original_id=d.message_id)
    )
    AND NOT EXISTS (
      SELECT 1 FROM restore_conflicting_relayed_echo_aliases a
      WHERE a.account_id=b.account_id AND a.scope=b.scope AND a.archive_id=b.archive_id
    )
UNION ALL
    SELECT 1 FROM backup.revisions b JOIN revisions d ON d.id=b.id WHERE d.entity_id<>b.entity_id
UNION ALL
    SELECT 1 FROM backup.conversation_revisions b JOIN conversation_revisions d ON d.id=b.id WHERE d.entity_id<>b.entity_id
UNION ALL
    SELECT 1 FROM backup.message_revisions b JOIN message_revisions d ON d.id=b.id WHERE d.entity_id<>b.entity_id
UNION ALL
    SELECT 1 FROM backup.message_star_revisions b JOIN message_star_revisions d ON d.id=b.id WHERE d.message_id<>b.message_id
);
