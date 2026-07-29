CREATE TABLE hotels (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    address     TEXT NOT NULL,
    city        TEXT NOT NULL,
    description TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_hotels_city ON hotels (city);