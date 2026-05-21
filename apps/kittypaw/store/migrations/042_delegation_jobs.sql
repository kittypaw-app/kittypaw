CREATE TABLE IF NOT EXISTS delegation_jobs (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    staff_id TEXT NOT NULL,
    task TEXT NOT NULL,
    parent_conversation_id TEXT NOT NULL DEFAULT '',
    delegate_conversation_id TEXT NOT NULL DEFAULT '',
    parent_staff_id TEXT NOT NULL DEFAULT '',
    parent_event_json TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0,
    claim_token TEXT NOT NULL DEFAULT '',
    claimed_at TEXT,
    started_at TEXT,
    lease_expires_at TEXT,
    finished_at TEXT,
    result TEXT NOT NULL DEFAULT '',
    error_class TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    token_usage INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    depth INTEGER NOT NULL DEFAULT 1,
    max_depth INTEGER NOT NULL DEFAULT 3,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_delegation_jobs_account_status_created
    ON delegation_jobs(account_id, status, created_at);

CREATE INDEX IF NOT EXISTS idx_delegation_jobs_account_parent_conversation
    ON delegation_jobs(account_id, parent_conversation_id, created_at);

CREATE INDEX IF NOT EXISTS idx_delegation_jobs_lease
    ON delegation_jobs(account_id, status, lease_expires_at);
