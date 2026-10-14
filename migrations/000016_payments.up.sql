ALTER TABLE room_types
ADD COLUMN currency TEXT NOT NULL DEFAULT 'VND'
    CONSTRAINT room_types_currency_check
    CHECK (currency IN ('USD', 'VND'));

ALTER TABLE bookings
ADD COLUMN currency TEXT NOT NULL DEFAULT 'VND'
    CONSTRAINT bookings_currency_check
    CHECK (currency IN ('USD', 'VND')),
ADD COLUMN payment_expires_at TIMESTAMPTZ;

ALTER TABLE bookings
DROP CONSTRAINT bookings_status_check;

ALTER TABLE bookings
ADD CONSTRAINT bookings_status_check
CHECK (
    status IN (
        'pending_payment',
        'confirmed',
        'cancelled',
        'completed'
    )
);

ALTER TABLE bookings
ADD CONSTRAINT bookings_payment_expiry_check
CHECK (
    status <> 'pending_payment'
    OR payment_expires_at IS NOT NULL
);

ALTER TABLE booking_status_history
DROP CONSTRAINT booking_status_history_from_status_check;

ALTER TABLE booking_status_history
DROP CONSTRAINT booking_status_history_to_status_check;

ALTER TABLE booking_status_history
ADD CONSTRAINT booking_status_history_from_status_check
CHECK (
    from_status IS NULL
    OR from_status IN (
        'pending_payment',
        'confirmed',
        'cancelled',
        'completed'
    )
);

ALTER TABLE booking_status_history
ADD CONSTRAINT booking_status_history_to_status_check
CHECK (
    to_status IN (
        'pending_payment',
        'confirmed',
        'cancelled',
        'completed'
    )
);

CREATE TABLE payments (
    id                       BIGSERIAL PRIMARY KEY,
    booking_id               BIGINT NOT NULL UNIQUE
        REFERENCES bookings (id) ON DELETE RESTRICT,

    provider                 TEXT NOT NULL
        CHECK (btrim(provider) <> ''),

    provider_payment_id      TEXT,
    provider_idempotency_key TEXT NOT NULL,

    amount_minor             BIGINT NOT NULL
        CHECK (amount_minor > 0),

    currency                 TEXT NOT NULL
        CHECK (currency IN ('USD', 'VND')),

    status                   TEXT NOT NULL DEFAULT 'pending'
        CHECK (
            status IN (
                'pending',
                'succeeded',
                'expired',
                'refund_pending',
                'refunded'
            )
        ),

    failure_code             TEXT,
    failure_message          TEXT,

    paid_at                  TIMESTAMPTZ,
    refunded_at              TIMESTAMPTZ,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (provider, provider_payment_id),
    UNIQUE (provider, provider_idempotency_key),

    CHECK (
        provider_payment_id IS NULL
        OR btrim(provider_payment_id) <> ''
    ),

    CHECK (
        failure_code IS NULL
        OR btrim(failure_code) <> ''
    ),

    CHECK (
        failure_message IS NULL
        OR btrim(failure_message) <> ''
    ),

    CHECK (
        status NOT IN ('succeeded', 'refund_pending', 'refunded')
        OR paid_at IS NOT NULL
    ),

    CHECK (
        status <> 'refunded'
        OR refunded_at IS NOT NULL
    )
);

CREATE TABLE payment_webhook_events (
    id                BIGSERIAL PRIMARY KEY,
    provider          TEXT NOT NULL
        CHECK (btrim(provider) <> ''),
    provider_event_id TEXT NOT NULL
        CHECK (btrim(provider_event_id) <> ''),
    event_type        TEXT NOT NULL
        CHECK (btrim(event_type) <> ''),
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (provider, provider_event_id)
);

CREATE INDEX idx_bookings_pending_payment_expiry
ON bookings (payment_expires_at, id)
WHERE status = 'pending_payment';

CREATE INDEX idx_payments_status_created
ON payments (status, created_at, id);

CREATE INDEX idx_payment_webhook_events_received
ON payment_webhook_events (received_at);