-- Only the v1 snapshot layout is supported. Validate every schema object
-- before using the verified XMPP view or making a writable staging copy.
WITH expected AS (
 SELECT type,name,tbl_name,sql FROM main.sqlite_schema WHERE name NOT GLOB 'sqlite_*'
), actual AS (
 SELECT type,name,tbl_name,sql FROM incoming.sqlite_schema WHERE name NOT GLOB 'sqlite_*'
)
SELECT NOT EXISTS (SELECT * FROM expected EXCEPT SELECT * FROM actual)
 AND NOT EXISTS (SELECT * FROM actual EXCEPT SELECT * FROM expected);
