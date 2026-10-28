DROP INDEX idx_payments_refund_claim;

ALTER TABLE payments
DROP CONSTRAINT payments_refund_lease_state_check,
DROP CONSTRAINT payments_refund_attempts_check,
DROP COLUMN refund_locked_at,
DROP COLUMN refund_available_at,
DROP COLUMN refund_attempts;
