SELECT EXISTS(SELECT 1 FROM backup.sqlite_schema WHERE name='deleted_account_ids'),
       EXISTS(SELECT 1 FROM backup.sqlite_schema WHERE name='recovered_send_revisions'),
       EXISTS(SELECT 1 FROM backup.sqlite_schema WHERE name='restored_own_echoes'),
       EXISTS(SELECT 1 FROM backup.sqlite_schema WHERE name='download_cleanup_revisions'),
       EXISTS(SELECT 1 FROM backup.sqlite_schema WHERE name='download_cleanup_parents'),
       EXISTS(SELECT 1 FROM backup.sqlite_schema WHERE name='legacy_download_memberships'),
       EXISTS(SELECT 1 FROM backup.sqlite_schema WHERE name='legacy_download_restore_admissions'),
       EXISTS(SELECT 1 FROM backup.sqlite_schema WHERE name='unclaimed_legacy_download_restore_admissions'),
       EXISTS(SELECT 1 FROM backup.sqlite_schema WHERE name='legacy_image_claims'),
       EXISTS(SELECT 1 FROM backup.sqlite_schema WHERE name='active_legacy_image_claims'),
       NOT EXISTS(
           SELECT 1 FROM backup.sqlite_schema b
           LEFT JOIN main.sqlite_schema d ON d.name=b.name AND d.type='table'
           WHERE b.name IN ('deleted_account_ids','recovered_send_revisions','restored_own_echoes','download_cleanup_revisions',
             'download_cleanup_parents','download_membership_cleanup_revisions','legacy_download_memberships',
             'legacy_download_restore_admissions','unclaimed_legacy_download_restore_admissions','legacy_image_claims',
             'active_legacy_image_claims')
             AND (b.type<>'table' OR b.sql IS NOT d.sql)
       ) AND (SELECT COUNT(*)<>1 FROM backup.sqlite_schema
             WHERE name IN ('download_cleanup_parents','download_membership_cleanup_revisions'));
