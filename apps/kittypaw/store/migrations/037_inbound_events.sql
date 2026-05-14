CREATE TABLE IF NOT EXISTS inbound_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id      TEXT    NOT NULL,
    event_type      TEXT    NOT NULL,
    source_event_id TEXT    NOT NULL DEFAULT '',
    payload_json    TEXT    NOT NULL,
    status          TEXT    NOT NULL DEFAULT 'queued'
                    CHECK(status IN ('queued', 'processing', 'done', 'failed')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    claimed_until   TEXT    NOT NULL DEFAULT '',
    last_error      TEXT    NOT NULL DEFAULT '',
    created_at      TEXT    NOT NULL,
    updated_at      TEXT    NOT NULL,
    done_at         TEXT    NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_inbound_events_source_unique
    ON inbound_events(account_id, event_type, source_event_id)
    WHERE source_event_id <> '';

CREATE INDEX IF NOT EXISTS idx_inbound_events_claim
    ON inbound_events(status, claimed_until, id);

