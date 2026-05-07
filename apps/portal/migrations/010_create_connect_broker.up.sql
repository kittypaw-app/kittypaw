CREATE TABLE connect_provider_tokens (
    user_id                  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_id              TEXT NOT NULL,
    access_token_ciphertext  BYTEA NOT NULL,
    refresh_token_ciphertext BYTEA,
    token_type               TEXT NOT NULL DEFAULT 'Bearer',
    scope                    TEXT NOT NULL DEFAULT '',
    username                 TEXT NOT NULL DEFAULT '',
    expires_at               TIMESTAMPTZ,
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, provider_id)
);

CREATE TABLE connect_provider_usage_events (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_id   TEXT NOT NULL,
    operation     TEXT NOT NULL,
    quantity      INTEGER NOT NULL CHECK (quantity >= 0),
    quota_key     TEXT NOT NULL DEFAULT 'post_reads',
    window_start  TIMESTAMPTZ NOT NULL,
    metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata_json) = 'object'),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_connect_provider_usage_window
    ON connect_provider_usage_events(user_id, provider_id, quota_key, window_start);
