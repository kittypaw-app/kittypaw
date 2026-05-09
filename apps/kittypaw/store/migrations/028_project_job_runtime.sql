ALTER TABLE jobs ADD COLUMN exit_code INTEGER NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_one_running_per_ticket
    ON jobs(ticket_id)
    WHERE status = 'running';
