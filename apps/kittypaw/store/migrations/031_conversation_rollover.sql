ALTER TABLE conversations
ADD COLUMN parent_conversation_id TEXT NOT NULL DEFAULT '';

ALTER TABLE conversations
ADD COLUMN rollover_reason TEXT NOT NULL DEFAULT '';

ALTER TABLE conversations
ADD COLUMN rollover_from_turn_id INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_conversations_parent
    ON conversations(parent_conversation_id);

CREATE TABLE IF NOT EXISTS conversation_routes (
    route_key TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    source_channel TEXT NOT NULL DEFAULT '',
    source_session_id TEXT NOT NULL DEFAULT '',
    chat_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_conversation_routes_conversation
    ON conversation_routes(conversation_id);
