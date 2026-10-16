-- Key-value facts about this data folder (creation time, app version that created it).
CREATE TABLE app_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;
