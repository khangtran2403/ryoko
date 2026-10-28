CREATE TABLE payment_refund_attempts (
    id              BIGSERIAL PRIMARY KEY,
    payment_id      BIGINT NOT NULL
        REFERENCES payments (id) ON DELETE RESTRICT,
    attempt_number  INTEGER NOT NULL
        CHECK (attempt_number > 0),
    status          TEXT NOT NULL
        CHECK (status IN ('failed', 'succeeded')),
    failure_code    TEXT,
    failure_message TEXT,
    started_at      TIMESTAMPTZ NOT NULL,
    completed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (payment_id, attempt_number),

    CHECK (
        (status = 'succeeded' AND failure_code IS NULL AND failure_message IS NULL)
        OR
        (status = 'failed' AND failure_code IS NOT NULL)
    ),
    CHECK (failure_code IS NULL OR btrim(failure_code) <> ''),
    CHECK (failure_message IS NULL OR btrim(failure_message) <> ''),
    CHECK (completed_at >= started_at)
);

CREATE INDEX idx_payment_refund_attempts_payment
ON payment_refund_attempts (payment_id, attempt_number DESC);
