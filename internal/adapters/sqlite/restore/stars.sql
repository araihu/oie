-- Idless observations keep immutable star revisions on their physical rows.
-- Compare the verified family together so an explicit operation can descend
-- from the observation on which it inherited its previous choice.
CREATE TEMP TABLE restore_idless_star_members (
  message_id TEXT PRIMARY KEY,canonical_id TEXT NOT NULL
) WITHOUT ROWID;
INSERT INTO restore_idless_star_members
WITH aliases AS MATERIALIZED (
  SELECT * FROM restore_idless_star_aliases
  UNION
  SELECT source_id,retained_id FROM restore_room_original_aliases
), affected AS (
  SELECT DISTINCT a.canonical_id FROM aliases a
  JOIN restore_messages selected ON selected.id IN (a.message_id,a.canonical_id)
)
SELECT a.message_id,a.canonical_id FROM aliases a JOIN affected x ON x.canonical_id=a.canonical_id
UNION
SELECT a.canonical_id,a.canonical_id FROM affected a;

INSERT INTO message_stars SELECT s.* FROM backup.message_stars s JOIN restore_messages x ON x.id=s.message_id
WHERE NOT EXISTS (SELECT 1 FROM restore_idless_star_members member WHERE member.message_id=s.message_id)
ON CONFLICT(message_id) DO NOTHING;

CREATE TEMP TABLE restore_idless_star_choices AS
WITH RECURSIVE current_heads AS MATERIALIZED (
  SELECT member.canonical_id,s.revision_id,
    row_number() OVER (PARTITION BY member.canonical_id ORDER BY s.updated_at DESC,s.revision_id DESC) AS priority
  FROM restore_idless_star_members member JOIN message_stars s ON s.message_id=member.message_id
), incoming AS MATERIALIZED (
  SELECT member.canonical_id,s.*
  FROM restore_idless_star_members member JOIN restore_messages selected ON selected.id=member.message_id
  JOIN backup.message_stars s ON s.message_id=member.message_id
  JOIN message_star_revisions r ON r.id=s.revision_id AND r.message_id=s.message_id
  WHERE r.payload=s.payload AND r.created_at=s.updated_at
    AND json_extract(s.payload,'$.RevisionID')=s.revision_id AND json_extract(s.payload,'$.MessageID')=s.message_id
    AND json_extract(s.payload,'$.Starred')=s.starred
), edges AS MATERIALIZED (
  SELECT member.canonical_id,child.id AS child_id,parent.id AS parent_id
  FROM restore_idless_star_members member
  JOIN message_star_revisions child ON child.message_id=member.message_id
  JOIN json_each(child.parents) edge ON edge.type='text'
  JOIN message_star_revisions parent ON parent.id=edge.value
  JOIN restore_idless_star_members related ON related.message_id=parent.message_id AND related.canonical_id=member.canonical_id
), ancestry(canonical_id,descendant_id,ancestor_id) AS (
  SELECT e.canonical_id,e.child_id,e.parent_id FROM edges e
  JOIN incoming s ON s.canonical_id=e.canonical_id AND s.revision_id=e.child_id
  UNION
  SELECT a.canonical_id,a.descendant_id,e.parent_id FROM ancestry a JOIN edges e
    ON e.canonical_id=a.canonical_id AND e.child_id=a.ancestor_id
), eligible AS MATERIALIZED (
  SELECT s.*,current.revision_id AS current_revision FROM incoming s
  LEFT JOIN current_heads current ON current.canonical_id=s.canonical_id AND current.priority=1
  WHERE (current.revision_id IS NULL OR s.revision_id=current.revision_id OR EXISTS (
      SELECT 1 FROM ancestry a WHERE a.canonical_id=s.canonical_id AND a.descendant_id=s.revision_id
        AND a.ancestor_id=current.revision_id
  )) AND NOT EXISTS (
      SELECT 1 FROM ancestry cycle WHERE cycle.canonical_id=s.canonical_id AND cycle.descendant_id=cycle.ancestor_id
  )
), preferred AS (
  SELECT s.*,row_number() OVER (PARTITION BY s.canonical_id ORDER BY s.updated_at DESC,s.revision_id DESC) AS priority
  FROM eligible s WHERE NOT EXISTS (
      SELECT 1 FROM eligible newer JOIN ancestry a ON a.canonical_id=newer.canonical_id AND a.descendant_id=newer.revision_id
      WHERE newer.canonical_id=s.canonical_id AND a.ancestor_id=s.revision_id
  )
)
SELECT canonical_id,message_id,revision_id,starred,payload,updated_at FROM preferred
WHERE priority=1 AND revision_id IS NOT current_revision;

DELETE FROM message_stars WHERE message_id IN (
  SELECT member.message_id FROM restore_idless_star_members member JOIN restore_idless_star_choices choice
    ON choice.canonical_id=member.canonical_id
);
INSERT INTO message_stars(message_id,revision_id,starred,payload,updated_at)
SELECT message_id,revision_id,starred,payload,updated_at FROM restore_idless_star_choices;

UPDATE restore_chats SET history_changed=1 WHERE id IN (
  SELECT m.conversation_id FROM restore_idless_star_choices choice JOIN messages m ON m.id=choice.canonical_id
);
DROP TABLE restore_idless_star_choices;
