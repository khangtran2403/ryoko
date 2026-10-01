CREATE TABLE refresh_tokens (
    id                   BIGSERIAL PRIMARY KEY,
    user_id              BIGINT NOT NULL
        REFERENCES users (id)
        ON DELETE CASCADE,

    token_hash           BYTEA NOT NULL UNIQUE
        CHECK (octet_length(token_hash) = 32),

    expires_at           TIMESTAMPTZ NOT NULL,
    revoked_at           TIMESTAMPTZ,

    replaced_by_token_id BIGINT
        REFERENCES refresh_tokens (id)
        ON DELETE SET NULL,

    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CHECK (expires_at > created_at),
    CHECK (
        revoked_at IS NULL
        OR revoked_at >= created_at
    ),
    CHECK (
        replaced_by_token_id IS NULL
        OR replaced_by_token_id <> id
    )
);

-- Speeds up listing or revoking a user's active sessions.
CREATE INDEX idx_refresh_tokens_active_user
ON refresh_tokens (
    user_id,
    expires_at
)
WHERE revoked_at IS NULL;

-- Speeds up removal of expired refresh tokens.
CREATE INDEX idx_refresh_tokens_expires_at
ON refresh_tokens (expires_at);