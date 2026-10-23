CREATE TABLE email_outbox (
    id              BIGSERIAL PRIMARY KEY,
    booking_id      BIGINT NOT NULL
        REFERENCES bookings (id)
        ON DELETE RESTRICT,
    event_type      TEXT NOT NULL
        CHECK (
            event_type IN (
                'booking_confirmed',
                'booking_cancelled',
                'payment_refunded'
            )
        ),
    recipient_email TEXT NOT NULL
        CHECK (btrim(recipient_email) <> ''),
    payload         JSONB NOT NULL
        CHECK (jsonb_typeof(payload) = 'object'),
    status          TEXT NOT NULL DEFAULT 'pending'
        CHECK (
            status IN (
                'pending',
                'processing',
                'sent',
                'failed'
            )
        ),
    attempts        INT NOT NULL DEFAULT 0
        CHECK (attempts >= 0),
    available_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_at       TIMESTAMPTZ,
    sent_at         TIMESTAMPTZ,
    last_error      TEXT
        CHECK (
            last_error IS NULL
            OR (
                btrim(last_error) <> ''
                AND char_length(last_error) <= 2000
            )
        ),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (booking_id, event_type),

    CHECK (
        (
            status = 'processing'
            AND locked_at IS NOT NULL
        )
        OR
        (
            status <> 'processing'
            AND locked_at IS NULL
        )
    ),

    CHECK (
        (
            status = 'sent'
            AND sent_at IS NOT NULL
        )
        OR
        (
            status <> 'sent'
            AND sent_at IS NULL
        )
    )
);

CREATE INDEX idx_email_outbox_pending
ON email_outbox (available_at, id)
WHERE status = 'pending';

CREATE INDEX idx_email_outbox_processing
ON email_outbox (locked_at, id)
WHERE status = 'processing';

CREATE INDEX idx_email_outbox_sent
ON email_outbox (sent_at, id)
WHERE status = 'sent';