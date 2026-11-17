SELECT EXISTS(SELECT 1 FROM backup.sqlite_schema WHERE name='deleted_account_ids'),
       EXISTS(SELECT 1 FROM backup.sqlite_schema WHERE name='recovered_send_revisions'),
       NOT EXISTS(
           SELECT 1 FROM backup.sqlite_schema b
           LEFT JOIN main.sqlite_schema d ON d.name=b.name AND d.type='table'
           WHERE b.name IN ('deleted_account_ids','recovered_send_revisions')
             AND (b.type<>'table' OR b.sql IS NOT d.sql)
       );
