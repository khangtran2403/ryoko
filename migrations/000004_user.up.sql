CREATE TABLE users (
    id         BIGSERIAL PRIMARY KEY,
    email      TEXT NOT NULL,
    password_hash TEXT,
    role TEXT NOT NULL DEFAULT 'customer',
    full_name  TEXT NOT NULL CHECK (btrim(full_name) <> ''),
    phone      TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CHECK (btrim(email) <> ''),
    CHECK (phone IS NULL OR btrim(phone) <> ''),
    CHECK (role IN ('customer', 'admin'))
);

CREATE UNIQUE INDEX users_email_unique_ci
ON users (lower(email));