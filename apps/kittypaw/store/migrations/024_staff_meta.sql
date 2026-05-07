CREATE TABLE IF NOT EXISTS staff_meta (
    id TEXT PRIMARY KEY,
    description TEXT NOT NULL DEFAULT '',
    equipped_skills TEXT NOT NULL DEFAULT '[]',
    active INTEGER NOT NULL DEFAULT 1,
    created_by TEXT NOT NULL DEFAULT 'manual',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

INSERT OR IGNORE INTO staff_meta (id, description, equipped_skills, active, created_by, created_at)
SELECT id, description, equipped_skills, active, created_by, created_at
FROM profile_meta;

DROP TABLE IF EXISTS profile_meta;
