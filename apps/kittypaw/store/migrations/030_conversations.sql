CREATE TABLE IF NOT EXISTS conversations (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL DEFAULT '',
    scope_type TEXT NOT NULL CHECK(scope_type IN ('general', 'project', 'ticket')),
    scope_id TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    default_staff_id TEXT NOT NULL DEFAULT '',
    source_channel TEXT NOT NULL DEFAULT '',
    source_session_id TEXT NOT NULL DEFAULT '',
    chat_id TEXT NOT NULL DEFAULT '',
    archived_at TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_conversations_scope
    ON conversations(scope_type, scope_id);

CREATE INDEX IF NOT EXISTS idx_conversations_updated
    ON conversations(updated_at);

INSERT OR IGNORE INTO conversation_scope (conversation_id, scope_type, scope_id)
VALUES ('general:account', 'general', 'account');

INSERT OR IGNORE INTO conversations (
    id, scope_type, scope_id, title, created_at, updated_at
)
VALUES (
    'general:account', 'general', 'account', 'General', datetime('now'), datetime('now')
);

INSERT OR IGNORE INTO conversations (
    id, scope_type, scope_id, chat_id, created_at, updated_at
)
SELECT conversation_id, scope_type, scope_id, conversation_id, created_at, updated_at
FROM conversation_scope;

ALTER TABLE v2_conversation_turns
ADD COLUMN conversation_id TEXT NOT NULL DEFAULT '';

UPDATE v2_conversation_turns
SET conversation_id = CASE
    WHEN chat_id IN (SELECT conversation_id FROM conversation_scope) THEN chat_id
    ELSE 'general:account'
END
WHERE conversation_id = '';

CREATE INDEX IF NOT EXISTS idx_v2_conversation_turns_conversation_id
    ON v2_conversation_turns(conversation_id, id);

ALTER TABLE conversation_checkpoints
ADD COLUMN conversation_id TEXT NOT NULL DEFAULT 'general:account';

CREATE INDEX IF NOT EXISTS idx_conversation_checkpoints_conversation
    ON conversation_checkpoints(conversation_id, id);

ALTER TABLE conversation_compactions
ADD COLUMN conversation_id TEXT NOT NULL DEFAULT 'general:account';

CREATE INDEX IF NOT EXISTS idx_conversation_compactions_conversation_end_turn
    ON conversation_compactions(conversation_id, end_turn_id);
