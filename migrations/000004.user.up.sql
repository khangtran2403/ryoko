CREATE TABLE users (
    id         BIGSERIAL PRIMARY KEY,
    email      TEXT NOT NULL,
    full_name  TEXT NOT NULL CHECK (btrim(full_name) <> ''),
    phone      TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CHECK (btrim(email) <> ''),
    CHECK (phone IS NULL OR btrim(phone) <> '')
);

CREATE UNIQUE INDEX users_email_unique_ci
ON users (lower(email));