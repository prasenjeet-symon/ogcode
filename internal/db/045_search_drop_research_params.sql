-- +goose Up
-- The deep-research knobs (pages fetched, characters per page) are fixed at the
-- values that read well across queries; they are no longer editable from the
-- settings screen, and nothing reads these columns any more. A deployment that
-- wants different numbers sets OGCODE_SEARCH_FETCH_TOP_K / OGCODE_SEARCH_PAGE_CHARS.
-- Drop them.
ALTER TABLE search_config DROP COLUMN fetch_top_k;
ALTER TABLE search_config DROP COLUMN page_chars;

-- +goose Down
ALTER TABLE search_config ADD COLUMN fetch_top_k INTEGER NOT NULL DEFAULT 4;
ALTER TABLE search_config ADD COLUMN page_chars  INTEGER NOT NULL DEFAULT 6000;
