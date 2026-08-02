-- Transform: udd_raw.bugs_tags -> bug_tags__new
--
-- Restricted to bugs that survived the previous transform, so a tag can never
-- point at a bug number the site cannot render.

INSERT INTO bug_tags__new (bug_number, tag)
SELECT
    t.id,
    t.tag
FROM udd_raw.bugs_tags AS t
INNER JOIN bugs__new AS b ON b.number = t.id
WHERE t.tag <> ''
ON CONFLICT (bug_number, tag) DO NOTHING;
