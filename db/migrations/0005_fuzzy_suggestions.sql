-- Edit distance, for the search that matched nothing. pg_trgm cannot do this
-- job: a typo in the middle of a short name shares too few trigrams with the
-- name it meant, so `faiafox` never reaches `firefox-esr`.

CREATE EXTENSION IF NOT EXISTS fuzzystrmatch;
