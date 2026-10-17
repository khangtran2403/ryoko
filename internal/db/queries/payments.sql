-- name: CreatePaymentForBooking :one
INSERT INTO payments (
    booking_id,
    provider,
    provider_idempotency_key,
    amount_minor,
    currency
)
SELECT
    b.id,
    sqlc.arg(provider),
    sqlc.arg(provider_idempotency_key),
    CASE b.currency
        WHEN 'USD' THEN (b.total_price * 100)::bigint
        WHEN 'VND' THEN b.total_price::bigint
    END,
    b.currency
FROM bookings AS b
WHERE b.id = sqlc.arg(booking_id)
  AND b.user_id = sqlc.arg(user_id)
  AND b.status = 'pending_payment'
  AND (
      b.currency <> 'VND'
      OR b.total_price = trunc(b.total_price)
  )
ON CONFLICT (booking_id) DO NOTHING
RETURNING
    id,
    booking_id,
    provider,
    provider_payment_id,
    provider_idempotency_key,
    amount_minor,
    currency,
    status,
    failure_code,
    failure_message,
    paid_at,
    refunded_at,
    created_at,
    updated_at;

-- name: SetPaymentProviderID :one
UPDATE payments
SET
    provider_payment_id = sqlc.arg(provider_payment_id),
    updated_at = now()
WHERE id = sqlc.arg(payment_id)
  AND status = 'pending'
  AND provider_payment_id IS NULL
RETURNING
    id,
    booking_id,
    provider,
    provider_payment_id,
    provider_idempotency_key,
    amount_minor,
    currency,
    status,
    failure_code,
    failure_message,
    paid_at,
    refunded_at,
    created_at,
    updated_at;

-- name: GetPaymentByBookingID :one
SELECT
    id,
    booking_id,
    provider,
    provider_payment_id,
    provider_idempotency_key,
    amount_minor,
    currency,
    status,
    failure_code,
    failure_message,
    paid_at,
    refunded_at,
    created_at,
    updated_at
FROM payments
WHERE booking_id = sqlc.arg(booking_id);

-- name: GetPaymentByBookingIDForUser :one
SELECT
    p.id,
    p.booking_id,
    p.provider,
    p.provider_payment_id,
    p.provider_idempotency_key,
    p.amount_minor,
    p.currency,
    p.status,
    p.failure_code,
    p.failure_message,
    p.paid_at,
    p.refunded_at,
    p.created_at,
    p.updated_at
FROM payments AS p
JOIN bookings AS b
    ON b.id = p.booking_id
WHERE p.booking_id = sqlc.arg(booking_id)
  AND b.user_id = sqlc.arg(user_id);

-- name: GetPaymentByBookingIDForUpdate :one
SELECT
    id,
    booking_id,
    provider,
    provider_payment_id,
    provider_idempotency_key,
    amount_minor,
    currency,
    status,
    failure_code,
    failure_message,
    paid_at,
    refunded_at,
    created_at,
    updated_at
FROM payments
WHERE booking_id = sqlc.arg(booking_id)
FOR UPDATE;

-- name: GetPaymentByProviderIDForUpdate :one
SELECT
    id,
    booking_id,
    provider,
    provider_payment_id,
    provider_idempotency_key,
    amount_minor,
    currency,
    status,
    failure_code,
    failure_message,
    paid_at,
    refunded_at,
    created_at,
    updated_at
FROM payments
WHERE provider = sqlc.arg(provider)
  AND provider_payment_id = sqlc.arg(provider_payment_id)
FOR UPDATE;

-- name: GetPaymentByProviderID :one
SELECT
    id,
    booking_id,
    provider,
    provider_payment_id,
    provider_idempotency_key,
    amount_minor,
    currency,
    status,
    failure_code,
    failure_message,
    paid_at,
    refunded_at,
    created_at,
    updated_at
FROM payments
WHERE provider = sqlc.arg(provider)
  AND provider_payment_id = sqlc.arg(provider_payment_id);

-- name: RecordPaymentFailure :one
UPDATE payments
SET
    failure_code = sqlc.narg(failure_code),
    failure_message = sqlc.narg(failure_message),
    updated_at = now()
WHERE id = sqlc.arg(payment_id)
  AND status = 'pending'
RETURNING
    id,
    booking_id,
    provider,
    provider_payment_id,
    provider_idempotency_key,
    amount_minor,
    currency,
    status,
    failure_code,
    failure_message,
    paid_at,
    refunded_at,
    created_at,
    updated_at;

-- name: MarkPaymentSucceeded :one
UPDATE payments
SET
    status = 'succeeded',
    failure_code = NULL,
    failure_message = NULL,
    paid_at = now(),
    updated_at = now()
WHERE id = sqlc.arg(payment_id)
  AND status = 'pending'
RETURNING
    id,
    booking_id,
    provider,
    provider_payment_id,
    provider_idempotency_key,
    amount_minor,
    currency,
    status,
    failure_code,
    failure_message,
    paid_at,
    refunded_at,
    created_at,
    updated_at;

-- name: MarkPaymentExpired :one
UPDATE payments
SET
    status = 'expired',
    updated_at = now()
WHERE id = sqlc.arg(payment_id)
  AND status = 'pending'
RETURNING
    id,
    booking_id,
    provider,
    provider_payment_id,
    provider_idempotency_key,
    amount_minor,
    currency,
    status,
    failure_code,
    failure_message,
    paid_at,
    refunded_at,
    created_at,
    updated_at;

-- name: MarkPaymentRefundPending :one
UPDATE payments
SET
    status = 'refund_pending',
    updated_at = now()
WHERE id = sqlc.arg(payment_id)
  AND status = 'succeeded'
RETURNING
    id,
    booking_id,
    provider,
    provider_payment_id,
    provider_idempotency_key,
    amount_minor,
    currency,
    status,
    failure_code,
    failure_message,
    paid_at,
    refunded_at,
    created_at,
    updated_at;

-- name: MarkExpiredPaymentRefundPending :one
UPDATE payments
SET
    status = 'refund_pending',
    failure_code = NULL,
    failure_message = NULL,
    paid_at = COALESCE(paid_at, now()),
    updated_at = now()
WHERE id = sqlc.arg(payment_id)
  AND status = 'expired'
RETURNING
    id,
    booking_id,
    provider,
    provider_payment_id,
    provider_idempotency_key,
    amount_minor,
    currency,
    status,
    failure_code,
    failure_message,
    paid_at,
    refunded_at,
    created_at,
    updated_at;

-- name: ListRefundPendingPayments :many
SELECT
    id,
    booking_id,
    provider,
    provider_payment_id,
    provider_idempotency_key,
    amount_minor,
    currency,
    status,
    failure_code,
    failure_message,
    paid_at,
    refunded_at,
    created_at,
    updated_at
FROM payments
WHERE status = 'refund_pending'
ORDER BY updated_at, id
LIMIT sqlc.arg(batch_size)::int;

-- name: RecordPaymentRefundFailure :exec
UPDATE payments
SET
    failure_code = 'refund_provider_error',
    failure_message = sqlc.arg(failure_message),
    updated_at = now()
WHERE id = sqlc.arg(payment_id)
  AND status = 'refund_pending';

-- name: MarkPaymentRefunded :one
UPDATE payments
SET
    status = 'refunded',
    failure_code = NULL,
    failure_message = NULL,
    refunded_at = now(),
    updated_at = now()
WHERE id = sqlc.arg(payment_id)
  AND status = 'refund_pending'
RETURNING
    id,
    booking_id,
    provider,
    provider_payment_id,
    provider_idempotency_key,
    amount_minor,
    currency,
    status,
    failure_code,
    failure_message,
    paid_at,
    refunded_at,
    created_at,
    updated_at;
-- name: GetInitiatablePaymentForUser :one
SELECT
    p.id,
    p.booking_id,
    p.provider,
    p.provider_payment_id,
    p.provider_idempotency_key,
    p.amount_minor,
    p.currency,
    p.status,
    p.failure_code,
    p.failure_message,
    p.paid_at,
    p.refunded_at,
    p.created_at,
    p.updated_at
FROM payments AS p
JOIN bookings AS b
    ON b.id = p.booking_id
WHERE p.booking_id = sqlc.arg(booking_id)
  AND b.user_id = sqlc.arg(user_id)
  AND p.status = 'pending'
  AND b.status = 'pending_payment'
  AND b.payment_expires_at > now();
-- name: ClaimPaymentWebhookEvent :one
INSERT INTO payment_webhook_events (
    provider,
    provider_event_id,
    event_type
)
VALUES (
    sqlc.arg(provider),
    sqlc.arg(provider_event_id),
    sqlc.arg(event_type)
)
ON CONFLICT (provider, provider_event_id) DO NOTHING
RETURNING
    id,
    provider,
    provider_event_id,
    event_type,
    received_at;
