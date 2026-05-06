CREATE TABLE connect_provider_policies (
    provider_id          TEXT PRIMARY KEY,
    enabled              BOOLEAN NOT NULL DEFAULT false,
    default_entitlement  TEXT NOT NULL DEFAULT 'deny'
                         CHECK (default_entitlement IN ('allow', 'deny')),
    requested_scopes     TEXT[] NOT NULL DEFAULT '{}',
    verification_status  TEXT NOT NULL DEFAULT 'unknown'
                         CHECK (verification_status IN ('unknown', 'not_applicable', 'testing', 'submitted', 'verified', 'blocked')),
    cost_mode            TEXT NOT NULL DEFAULT 'none'
                         CHECK (cost_mode IN ('none', 'external_policy', 'kitty_paid')),
    notes                TEXT NOT NULL DEFAULT '',
    updated_by           UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE connect_user_entitlements (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider_id TEXT NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('allowed', 'blocked', 'revoked')),
    quota_json  JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(quota_json) = 'object'),
    reason      TEXT NOT NULL DEFAULT '',
    granted_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ,
    UNIQUE (user_id, provider_id)
);

CREATE INDEX idx_connect_user_entitlements_provider ON connect_user_entitlements(provider_id);
CREATE INDEX idx_connect_user_entitlements_active ON connect_user_entitlements(user_id, provider_id)
    WHERE status = 'allowed' AND revoked_at IS NULL;

CREATE TABLE connect_admin_audit_events (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id  UUID REFERENCES users(id) ON DELETE SET NULL,
    action         TEXT NOT NULL,
    provider_id    TEXT,
    target_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    before_json    JSONB,
    after_json     JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_connect_admin_audit_created ON connect_admin_audit_events(created_at DESC);
CREATE INDEX idx_connect_admin_audit_target ON connect_admin_audit_events(target_user_id, created_at DESC);
