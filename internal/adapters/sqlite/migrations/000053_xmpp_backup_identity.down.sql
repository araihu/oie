DROP TRIGGER forget_removed_xmpp_scopes;
DROP TRIGGER remember_removed_xmpp_address;
DROP TABLE xmpp_removed_accounts;
DROP TRIGGER retain_xmpp_exclusion;
DROP TABLE xmpp_message_exclusions;
DROP TABLE xmpp_unidentified_removals;
DROP VIEW xmpp_message_keys;
PRAGMA user_version=52;
