ALTER TABLE outbound_deliveries
    ADD COLUMN origin_type TEXT NOT NULL DEFAULT '';

ALTER TABLE outbound_deliveries
    ADD COLUMN origin_id TEXT NOT NULL DEFAULT '';

ALTER TABLE outbound_deliveries
    ADD COLUMN origin_name TEXT NOT NULL DEFAULT '';

ALTER TABLE outbound_deliveries
    ADD COLUMN scheduled_run_id INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_outbound_deliveries_account_origin
    ON outbound_deliveries (account_id, origin_type, origin_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_outbound_deliveries_scheduled_run
    ON outbound_deliveries (scheduled_run_id);
