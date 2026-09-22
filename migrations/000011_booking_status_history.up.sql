CREATE TABLE booking_status_history (
    id                  BIGSERIAL PRIMARY KEY,
    booking_id          BIGINT NOT NULL
        REFERENCES bookings (id) ON DELETE CASCADE,

    from_status         TEXT
        CHECK (
            from_status IS NULL
            OR from_status IN (
                'confirmed',
                'cancelled',
                'completed'
            )
        ),

    to_status           TEXT NOT NULL
        CHECK (
            to_status IN (
                'confirmed',
                'cancelled',
                'completed'
            )
        ),

    changed_by_user_id  BIGINT
        REFERENCES users (id) ON DELETE SET NULL,

    reason              TEXT
        CHECK (
            reason IS NULL
            OR btrim(reason) <> ''
        ),

    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CHECK (
        from_status IS NULL
        OR from_status <> to_status
    )
);

CREATE INDEX idx_booking_status_history_booking_created
ON booking_status_history (
    booking_id,
    created_at,
    id
);

-- Existing bookings have no reconstructable transition history.
-- Record their current state as the initial imported snapshot.
INSERT INTO booking_status_history (
    booking_id,
    from_status,
    to_status,
    changed_by_user_id,
    reason,
    created_at
)
SELECT
    id,
    NULL,
    status,
    NULL,
    'Initial status history backfill',
    CASE
        WHEN status = 'confirmed' THEN created_at
        ELSE updated_at
    END
FROM bookings;