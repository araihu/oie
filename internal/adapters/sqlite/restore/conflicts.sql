SELECT EXISTS (
    SELECT 1 FROM backup.accounts b JOIN restore_accounts x ON x.id=b.id
    JOIN accounts a ON a.id=b.id OR (a.protocol=b.protocol AND a.address=b.address)
    WHERE a.id<>b.id OR a.protocol<>b.protocol OR a.address<>b.address
UNION ALL
    SELECT 1 FROM backup.conversations b JOIN restore_chats x ON x.id=b.id
    JOIN conversations c ON c.id=b.id OR (c.account_id=b.account_id AND c.address=b.address)
    WHERE c.id<>b.id OR c.account_id<>b.account_id OR c.address<>b.address OR
        json_extract(c.payload,'$.Kind') IS NOT json_extract(b.payload,'$.Kind')
UNION ALL
    SELECT 1 FROM backup.messages b JOIN restore_messages x ON x.id=b.id
    JOIN messages m ON m.id=b.id OR (
       m.account_id=b.account_id AND m.conversation_id=b.conversation_id
       AND m.provider_id=b.provider_id AND m.is_own=b.is_own AND b.provider_id<>''
    )
    WHERE m.id<>b.id OR m.account_id<>b.account_id OR m.conversation_id<>b.conversation_id
UNION ALL
    SELECT 1 FROM backup.revisions b JOIN revisions d ON d.id=b.id WHERE d.entity_id<>b.entity_id
UNION ALL
    SELECT 1 FROM backup.conversation_revisions b JOIN conversation_revisions d ON d.id=b.id WHERE d.entity_id<>b.entity_id
UNION ALL
    SELECT 1 FROM backup.message_revisions b JOIN message_revisions d ON d.id=b.id WHERE d.entity_id<>b.entity_id
UNION ALL
    SELECT 1 FROM backup.message_star_revisions b JOIN message_star_revisions d ON d.id=b.id WHERE d.message_id<>b.message_id
);
