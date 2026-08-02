-- Transform: udd_raw.popcon -> popcon__new
--
-- Popularity ranks autocompletion results. Without it a prefix search for "pyt"
-- returns whatever the index happens to reach first, which is never what the
-- reader meant.

INSERT INTO popcon__new (package, installed, votes, recent)
SELECT
    p.package,
    coalesce(p.insts, 0),
    coalesce(p.vote, 0),
    coalesce(p.recent, 0)
FROM udd_raw.popcon AS p
ON CONFLICT (package) DO NOTHING;
