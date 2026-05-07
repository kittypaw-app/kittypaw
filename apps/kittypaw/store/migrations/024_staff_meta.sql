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

INSERT OR IGNORE INTO user_context (key, value, source, updated_at)
SELECT 'active_staff:' || substr(key, length('active_profile:') + 1), value, source, updated_at
FROM user_context
WHERE key LIKE 'active_profile:%';

DELETE FROM user_context
WHERE key LIKE 'active_profile:%';

DROP TABLE IF EXISTS profile_meta;
