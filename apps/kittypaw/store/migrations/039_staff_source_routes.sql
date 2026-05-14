CREATE TABLE IF NOT EXISTS staff_source_routes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_channel TEXT NOT NULL,
    match_field TEXT NOT NULL CHECK (match_field IN ('chat_id', 'source_session_id')),
    pattern_kind TEXT NOT NULL CHECK (pattern_kind IN ('exact', 'prefix', 'glob')),
    pattern TEXT NOT NULL,
    staff_id TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(source_channel, match_field, pattern_kind, pattern)
);

CREATE INDEX IF NOT EXISTS idx_staff_source_routes_channel
    ON staff_source_routes(source_channel, enabled, priority DESC, id ASC);
