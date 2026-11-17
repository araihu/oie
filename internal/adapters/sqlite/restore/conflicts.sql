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
    JOIN messages m ON m.id=b.id OR (
       m.account_id=b.account_id AND m.conversation_id=b.conversation_id
       AND m.provider_id=COALESCE(source.provider_id,b.provider_id) AND m.is_own=b.is_own AND m.provider_id<>''
    )
    WHERE m.account_id<>b.account_id OR m.conversation_id<>b.conversation_id OR (m.id<>b.id AND NOT EXISTS (
      SELECT 1 FROM restore_own_echo_aliases e
      WHERE (e.echo_id=m.id AND e.original_id=b.id) OR (e.echo_id=b.id AND e.original_id=m.id)
    ) AND NOT EXISTS (SELECT 1 FROM restore_direct_mention_echo_keys local WHERE local.id=m.id AND local.is_local=1))
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
