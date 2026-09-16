CREATE TABLE booking_idempotency_keys (
    user_id         BIGINT NOT NULL
        REFERENCES users (id) ON DELETE CASCADE,

    idempotency_key TEXT NOT NULL
        CHECK (
            btrim(idempotency_key) <> ''
            AND char_length(idempotency_key) <= 255
        ),

    request_hash    BYTEA NOT NULL
        CHECK (octet_length(request_hash) = 32),

    booking_id      BIGINT UNIQUE
        REFERENCES bookings (id) ON DELETE CASCADE,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (user_id, idempotency_key)
);

CREATE INDEX idx_booking_idempotency_keys_created_at
ON booking_idempotency_keys (created_at);