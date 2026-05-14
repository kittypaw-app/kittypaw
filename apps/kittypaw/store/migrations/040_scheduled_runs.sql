CREATE TABLE IF NOT EXISTS scheduled_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_key TEXT NOT NULL,
    job_type TEXT NOT NULL,
    job_id TEXT NOT NULL,
    trigger_type TEXT NOT NULL,
    due_at TEXT NOT NULL,
    status TEXT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0,
    claim_token TEXT,
    claimed_at TEXT,
    started_at TEXT,
    lease_expires_at TEXT,
    finished_at TEXT,
    error_class TEXT,
    error_message TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(job_key, due_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_scheduled_runs_one_active
ON scheduled_runs(job_key)
WHERE status IN ('queued', 'running');

CREATE INDEX IF NOT EXISTS idx_scheduled_runs_job_status_due
ON scheduled_runs(job_key, status, due_at);

CREATE INDEX IF NOT EXISTS idx_scheduled_runs_status_lease
ON scheduled_runs(status, lease_expires_at);
