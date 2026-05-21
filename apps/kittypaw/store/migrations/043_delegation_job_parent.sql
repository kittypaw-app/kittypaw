ALTER TABLE delegation_jobs
    ADD COLUMN parent_job_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_delegation_jobs_account_parent_job
    ON delegation_jobs(account_id, parent_job_id, created_at);
