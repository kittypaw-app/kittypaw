ALTER TABLE staff_meta ADD COLUMN display_name TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS staff_aliases (
    alias TEXT PRIMARY KEY,
    staff_id TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    FOREIGN KEY (staff_id) REFERENCES staff_meta(id) ON DELETE CASCADE
);
