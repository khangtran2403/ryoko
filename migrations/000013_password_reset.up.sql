CREATE TABLE password_reset_requests (
    id                     BIGSERIAL PRIMARY KEY,
    user_id                BIGINT NOT NULL
        REFERENCES users (id)
        ON DELETE CASCADE,

    otp_hash               BYTEA NOT NULL
        CHECK (octet_length(otp_hash) = 32),

    attempts               SMALLINT NOT NULL DEFAULT 0
        CHECK (attempts >= 0 AND attempts <= 5),

    expires_at             TIMESTAMPTZ NOT NULL,
    verified_at            TIMESTAMPTZ,

    reset_token_hash       BYTEA UNIQUE
        CHECK (
            reset_token_hash IS NULL
            OR octet_length(reset_token_hash) = 32
        ),

    reset_token_expires_at TIMESTAMPTZ,
    consumed_at            TIMESTAMPTZ,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CHECK (expires_at > created_at),

    CHECK (
        (
            verified_at IS NULL
            AND reset_token_hash IS NULL
            AND reset_token_expires_at IS NULL
        )
        OR
        (
            verified_at IS NOT NULL
            AND reset_token_hash IS NOT NULL
            AND reset_token_expires_at IS NOT NULL
            AND reset_token_expires_at > verified_at
        )
    ),

    CHECK (
        verified_at IS NULL
        OR verified_at >= created_at
    ),

    CHECK (
        consumed_at IS NULL
        OR consumed_at >= created_at
    )
);

-- Only one unconsumed password-reset flow per user.
CREATE UNIQUE INDEX idx_password_reset_active_user
ON password_reset_requests (user_id)
WHERE consumed_at IS NULL;

CREATE INDEX idx_password_reset_user_created
ON password_reset_requests (
    user_id,
    created_at DESC
);

CREATE INDEX idx_password_reset_expires_at
ON password_reset_requests (expires_at);

CREATE INDEX idx_password_reset_token_expires_at
ON password_reset_requests (reset_token_expires_at)
WHERE reset_token_expires_at IS NOT NULL;