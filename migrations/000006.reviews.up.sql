CREATE TABLE reviews (
    id         BIGSERIAL PRIMARY KEY,
    booking_id BIGINT NOT NULL UNIQUE
        REFERENCES bookings (id) ON DELETE RESTRICT,
    rating     SMALLINT NOT NULL
        CHECK (rating BETWEEN 1 AND 5),
    comment    TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CHECK (comment IS NULL OR btrim(comment) <> '')
);