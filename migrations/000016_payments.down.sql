DROP TABLE IF EXISTS payment_webhook_events;
DROP TABLE IF EXISTS payments;

DROP INDEX IF EXISTS idx_bookings_pending_payment_expiry;

ALTER TABLE booking_status_history
DROP CONSTRAINT booking_status_history_from_status_check;

ALTER TABLE booking_status_history
DROP CONSTRAINT booking_status_history_to_status_check;

DELETE FROM booking_status_history
WHERE from_status = 'pending_payment'
   OR to_status = 'pending_payment';

-- Preserve inventory reservations when rolling back.
UPDATE bookings
SET
    status = 'confirmed',
    payment_expires_at = NULL,
    updated_at = now()
WHERE status = 'pending_payment';

ALTER TABLE booking_status_history
ADD CONSTRAINT booking_status_history_from_status_check
CHECK (
    from_status IS NULL
    OR from_status IN (
        'confirmed',
        'cancelled',
        'completed'
    )
);

ALTER TABLE booking_status_history
ADD CONSTRAINT booking_status_history_to_status_check
CHECK (
    to_status IN (
        'confirmed',
        'cancelled',
        'completed'
    )
);

ALTER TABLE bookings
DROP CONSTRAINT bookings_payment_expiry_check;

ALTER TABLE bookings
DROP CONSTRAINT bookings_status_check;

ALTER TABLE bookings
ADD CONSTRAINT bookings_status_check
CHECK (
    status IN (
        'confirmed',
        'cancelled',
        'completed'
    )
);

ALTER TABLE bookings
DROP COLUMN payment_expires_at,
DROP COLUMN currency;

ALTER TABLE room_types
DROP COLUMN currency;