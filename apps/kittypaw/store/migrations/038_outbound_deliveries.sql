CREATE TABLE IF NOT EXISTS outbound_deliveries (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id          TEXT    NOT NULL,
    event_type          TEXT    NOT NULL,
    chat_id             TEXT    NOT NULL,
    source              TEXT    NOT NULL,
    status              TEXT    NOT NULL,
    response_preview    TEXT    NOT NULL DEFAULT '',
    pending_response_id INTEGER NOT NULL DEFAULT 0,
    retry_count         INTEGER NOT NULL DEFAULT 0,
    error_class         TEXT    NOT NULL DEFAULT '',
    error_message       TEXT    NOT NULL DEFAULT '',
    created_at          TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at          TEXT    NOT NULL DEFAULT (datetime('now')),
    delivered_at        TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_outbound_deliveries_account_created
    ON outbound_deliveries (account_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_outbound_deliveries_account_status
    ON outbound_deliveries (account_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_outbound_deliveries_pending_response
    ON outbound_deliveries (pending_response_id);
