ALTER TABLE payments
ADD COLUMN refund_attempts INTEGER NOT NULL DEFAULT 0,
ADD COLUMN refund_available_at TIMESTAMPTZ,
ADD COLUMN refund_locked_at TIMESTAMPTZ;

UPDATE payments
SET refund_available_at = now()
WHERE status = 'refund_pending';

ALTER TABLE payments
ADD CONSTRAINT payments_refund_attempts_check
CHECK (refund_attempts >= 0),
ADD CONSTRAINT payments_refund_lease_state_check
CHECK (
    (
        status = 'refund_pending'
        AND refund_available_at IS NOT NULL
    )
    OR (
        status <> 'refund_pending'
        AND refund_available_at IS NULL
        AND refund_locked_at IS NULL
    )
);

CREATE INDEX idx_payments_refund_claim
ON payments (refund_available_at, id)
WHERE status = 'refund_pending';
