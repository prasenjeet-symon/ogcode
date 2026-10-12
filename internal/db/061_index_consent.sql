-- +goose Up
-- Records whether a project has consented to being indexed automatically once it
-- grows past the configured file limit (OGCODE_AUTO_INDEX_MAX_FILES, default
-- 500). Keyed by directory, like index_excludes, because a single database can
-- hold scope for more than one workspace root. A missing row reads as "no" — a
-- project nobody has answered for is asked, not assumed.
CREATE TABLE IF NOT EXISTS index_consent (
    directory    TEXT PRIMARY KEY,
    allowed      INTEGER NOT NULL DEFAULT 0,
    time_updated INTEGER NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE IF EXISTS index_consent;
