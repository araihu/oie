-- An empty value identifies a legacy membership. A real cleanup proof, if
-- present, remains authoritative for that same membership.
INSERT INTO restore_download_membership_proofs
SELECT device_id,source_id,source_revision,kind,'' FROM backup.legacy_download_memberships
WHERE TRUE ON CONFLICT DO NOTHING;
