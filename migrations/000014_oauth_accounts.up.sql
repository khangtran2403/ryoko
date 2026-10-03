CREATE TABLE oauth_accounts (
    id               BIGSERIAL PRIMARY KEY,

    user_id          BIGINT NOT NULL
        REFERENCES users(id)
        ON DELETE CASCADE,

    provider         TEXT NOT NULL,
    provider_user_id TEXT NOT NULL,
    provider_email   TEXT NOT NULL,

    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CHECK (provider IN ('google')),
    CHECK (btrim(provider_user_id) <> ''),
    CHECK (btrim(provider_email) <> ''),

    -- One provider identity can belong to only one Ryoko user.
    UNIQUE (provider, provider_user_id),
    UNIQUE (user_id, provider)
);