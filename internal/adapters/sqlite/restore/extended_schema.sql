WITH evidence(name, introduced) AS (
    VALUES ('deleted_account_ids',41), ('recovered_send_revisions',41), ('restored_own_echoes',42),
           ('download_cleanup_revisions',43), ('download_cleanup_parents',44), ('download_membership_cleanup_revisions',44),
           ('legacy_download_memberships',45), ('legacy_download_restore_admissions',46),
           ('unclaimed_legacy_download_restore_admissions',47), ('legacy_image_claims',48),
           ('active_legacy_image_claims',51), ('retired_recovered_sends',52)
)
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
       EXISTS(SELECT 1 FROM backup.sqlite_schema WHERE name='retired_recovered_sends'),
       NOT EXISTS(
           SELECT 1 FROM evidence e
           LEFT JOIN main.sqlite_schema d ON d.name=e.name AND d.type='table'
           LEFT JOIN backup.sqlite_schema b ON b.name=e.name
           WHERE (b.name IS NULL AND migration.version>=e.introduced)
              OR (b.name IS NOT NULL AND (b.type<>'table' OR b.sql IS NOT d.sql))
       ) AND (SELECT COUNT(*)<>1 FROM backup.sqlite_schema
             WHERE name IN ('download_cleanup_parents','download_membership_cleanup_revisions'))
FROM backup.schema_migrations migration;
