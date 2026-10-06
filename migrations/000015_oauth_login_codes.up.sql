CREATE TABLE oauth_login_codes (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL
        REFERENCES users(id)
        ON DELETE CASCADE,
    code_hash   BYTEA NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CHECK (octet_length(code_hash) = 32),
    CHECK (expires_at > created_at),
    CHECK (consumed_at IS NULL OR consumed_at >= created_at)
);

CREATE INDEX oauth_login_codes_expiry_idx
ON oauth_login_codes (expires_at);
