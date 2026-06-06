-- Down 024: drop users. Safe because no data-plane table FKs owner_id yet (the
-- backfill is a later slice); dropping the table here cannot orphan referencing rows.
DROP TABLE IF EXISTS users;
