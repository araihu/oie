SELECT EXISTS(
  SELECT 1 FROM backup.sqlite_schema
  WHERE name IN ('recovered_sends','discarded_recovered_sends','recovered_send_operations')
),
(SELECT COUNT(*)=3 FROM backup.sqlite_schema b JOIN main.sqlite_schema d USING (name,type)
  WHERE d.name IN ('recovered_sends','discarded_recovered_sends','recovered_send_operations')
    AND b.type='table' AND b.sql=d.sql);
