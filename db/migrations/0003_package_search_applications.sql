-- What the front page needs: applications a person would recognise, ranked by
-- regular use. `votes` is popcon's count of systems that actually use the
-- package; `application` is decided during the transform from screenshots,
-- debtags, and dependency metadata.

ALTER TABLE package_search
ADD COLUMN votes integer NOT NULL DEFAULT 0;

ALTER TABLE package_search
ADD COLUMN application boolean NOT NULL DEFAULT false;

CREATE INDEX package_search_applications_idx ON package_search (
    votes DESC
) WHERE application AND kind = 'binary';
