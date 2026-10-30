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
    updated_at,
    refund_attempts,
    refund_available_at,
    refund_locked_at;

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
    updated_at,
    refund_attempts,
    refund_available_at,
    refund_locked_at;

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
    updated_at,
    refund_attempts,
    refund_available_at,
    refund_locked_at
FROM payments
WHERE booking_id = sqlc.arg(booking_id);

-- name: ListPaymentsForAdmin :many
SELECT
    p.id,
    p.booking_id,
    b.status AS booking_status,
    u.id AS customer_id,
    u.full_name AS customer_name,
    u.email AS customer_email,
    h.id AS hotel_id,
    h.name AS hotel_name,
    p.provider,
    p.provider_payment_id,
    p.amount_minor,
    p.currency,
    p.status,
    p.failure_code,
    p.failure_message,
    p.paid_at,
    p.refunded_at,
    p.created_at,
    p.updated_at,
    p.refund_attempts,
    p.refund_available_at,
    p.refund_locked_at
FROM payments AS p
JOIN bookings AS b
    ON b.id = p.booking_id
JOIN users AS u
    ON u.id = b.user_id
JOIN room_types AS rt
    ON rt.id = b.room_type_id
JOIN hotels AS h
    ON h.id = rt.hotel_id
WHERE (
        sqlc.narg(payment_status)::text IS NULL
        OR p.status = sqlc.narg(payment_status)
    )
  AND (
        sqlc.narg(provider)::text IS NULL
        OR p.provider = sqlc.narg(provider)
    )
  AND (
        sqlc.narg(booking_id)::bigint IS NULL
        OR p.booking_id = sqlc.narg(booking_id)
    )
ORDER BY p.created_at DESC, p.id DESC
LIMIT sqlc.arg(result_limit)::int
OFFSET sqlc.arg(result_offset)::bigint;

-- name: GetPaymentOperationsSummaryForAdmin :one
SELECT
    count(*) FILTER (WHERE status = 'pending')::bigint AS pending_count,
    count(*) FILTER (WHERE status = 'succeeded')::bigint AS succeeded_count,
    count(*) FILTER (WHERE status = 'expired')::bigint AS expired_count,
    count(*) FILTER (WHERE status = 'refund_pending')::bigint AS refund_pending_count,
    count(*) FILTER (WHERE status = 'refunded')::bigint AS refunded_count,
    count(*) FILTER (
        WHERE status = 'refund_pending'
          AND refund_locked_at IS NULL
          AND refund_available_at <= now()
    )::bigint AS refunds_ready_count,
    count(*) FILTER (
        WHERE status = 'refund_pending'
          AND refund_locked_at IS NULL
          AND refund_available_at > now()
    )::bigint AS refunds_delayed_count,
    count(*) FILTER (
        WHERE status = 'refund_pending'
          AND refund_locked_at IS NOT NULL
    )::bigint AS refunds_leased_count,
    count(*) FILTER (
        WHERE status = 'refund_pending'
          AND failure_code IS NOT NULL
    )::bigint AS refunds_failed_count,
    (min(created_at) FILTER (WHERE status = 'pending'))::timestamptz AS oldest_pending_payment_at,
    (min(refund_available_at) FILTER (WHERE status = 'refund_pending'))::timestamptz AS oldest_pending_refund_at
FROM payments;

-- name: ListPaymentCurrencyTotalsForAdmin :many
SELECT
    currency,
    count(*) FILTER (WHERE paid_at IS NOT NULL)::bigint AS paid_count,
    COALESCE(
        sum(amount_minor) FILTER (WHERE paid_at IS NOT NULL),
        0
    )::bigint AS paid_amount_minor,
    count(*) FILTER (WHERE status = 'refunded')::bigint AS refunded_count,
    COALESCE(
        sum(amount_minor) FILTER (WHERE status = 'refunded'),
        0
    )::bigint AS refunded_amount_minor
FROM payments
GROUP BY currency
ORDER BY currency;

-- name: GetPaymentForAdmin :one
SELECT
    p.id,
    p.booking_id,
    b.status AS booking_status,
    u.id AS customer_id,
    u.full_name AS customer_name,
    u.email AS customer_email,
    h.id AS hotel_id,
    h.name AS hotel_name,
    p.provider,
    p.provider_payment_id,
    p.amount_minor,
    p.currency,
    p.status,
    p.failure_code,
    p.failure_message,
    p.paid_at,
    p.refunded_at,
    p.created_at,
    p.updated_at,
    p.refund_attempts,
    p.refund_available_at,
    p.refund_locked_at
FROM payments AS p
JOIN bookings AS b
    ON b.id = p.booking_id
JOIN users AS u
    ON u.id = b.user_id
JOIN room_types AS rt
    ON rt.id = b.room_type_id
JOIN hotels AS h
    ON h.id = rt.hotel_id
WHERE p.id = sqlc.arg(payment_id);

-- name: RetryFailedPaymentRefundForAdmin :one
WITH retried_refund AS (
    UPDATE payments AS payment
    SET
        refund_available_at = now(),
        failure_code = NULL,
        failure_message = NULL,
        updated_at = now()
    WHERE payment.id = sqlc.arg(payment_id)
      AND payment.status = 'refund_pending'
      AND payment.failure_code IS NOT NULL
      AND payment.refund_locked_at IS NULL
    RETURNING payment.id
)
SELECT
    p.id,
    p.booking_id,
    b.status AS booking_status,
    u.id AS customer_id,
    u.full_name AS customer_name,
    u.email AS customer_email,
    h.id AS hotel_id,
    h.name AS hotel_name,
    p.provider,
    p.provider_payment_id,
    p.amount_minor,
    p.currency,
    p.status,
    p.failure_code,
    p.failure_message,
    p.paid_at,
    p.refunded_at,
    p.created_at,
    p.updated_at,
    p.refund_attempts,
    p.refund_available_at,
    p.refund_locked_at
FROM retried_refund AS rr
JOIN payments AS p
    ON p.id = rr.id
JOIN bookings AS b
    ON b.id = p.booking_id
JOIN users AS u
    ON u.id = b.user_id
JOIN room_types AS rt
    ON rt.id = b.room_type_id
JOIN hotels AS h
    ON h.id = rt.hotel_id;

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
    p.updated_at,
    p.refund_attempts,
    p.refund_available_at,
    p.refund_locked_at
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
    updated_at,
    refund_attempts,
    refund_available_at,
    refund_locked_at
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
    updated_at,
    refund_attempts,
    refund_available_at,
    refund_locked_at
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
    updated_at,
    refund_attempts,
    refund_available_at,
    refund_locked_at
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
    updated_at,
    refund_attempts,
    refund_available_at,
    refund_locked_at;

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
    updated_at,
    refund_attempts,
    refund_available_at,
    refund_locked_at;

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
    updated_at,
    refund_attempts,
    refund_available_at,
    refund_locked_at;

-- name: MarkPaymentRefundPending :one
UPDATE payments
SET
    status = 'refund_pending',
    refund_attempts = 0,
    refund_available_at = now(),
    refund_locked_at = NULL,
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
    updated_at,
    refund_attempts,
    refund_available_at,
    refund_locked_at;

-- name: MarkExpiredPaymentRefundPending :one
UPDATE payments
SET
    status = 'refund_pending',
    refund_attempts = 0,
    refund_available_at = now(),
    refund_locked_at = NULL,
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
    updated_at,
    refund_attempts,
    refund_available_at,
    refund_locked_at;

-- name: ClaimRefundPendingPayments :many
WITH claimable AS (
    SELECT id
    FROM payments
    WHERE status = 'refund_pending'
      AND refund_available_at <= now()
      AND (
          refund_locked_at IS NULL
          OR refund_locked_at <= now()
              - make_interval(secs => sqlc.arg(lease_seconds)::int)
      )
    ORDER BY refund_available_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg(batch_size)::int
)
UPDATE payments AS p
SET
    refund_attempts = p.refund_attempts + 1,
    refund_locked_at = now(),
    updated_at = now()
FROM claimable AS c
WHERE p.id = c.id
RETURNING
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
    p.updated_at,
    p.refund_attempts,
    p.refund_available_at,
    p.refund_locked_at;

-- name: ListPaymentsForReconciliation :many
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
    updated_at,
    refund_attempts,
    refund_available_at,
    refund_locked_at
FROM payments
WHERE provider = sqlc.arg(provider)
  AND status = 'pending'
  AND provider_payment_id IS NOT NULL
  AND updated_at <= sqlc.arg(updated_before)
ORDER BY updated_at, id
LIMIT sqlc.arg(batch_size)::int;

-- name: TouchPendingPaymentReconciliation :execrows
UPDATE payments
SET updated_at = now()
WHERE id = sqlc.arg(payment_id)
  AND status = 'pending';

-- name: SchedulePaymentRefundRetry :execrows
WITH scheduled_payment AS (
    UPDATE payments AS payment
    SET
        failure_code = 'refund_provider_error',
        failure_message = sqlc.arg(failure_message),
        refund_available_at = now()
            + make_interval(secs => sqlc.arg(retry_after_seconds)::int),
        refund_locked_at = NULL,
        updated_at = now()
    WHERE payment.id = sqlc.arg(payment_id)
      AND payment.status = 'refund_pending'
      AND payment.refund_locked_at = sqlc.arg(refund_locked_at)
    RETURNING payment.id, payment.refund_attempts
)
INSERT INTO payment_refund_attempts (
    payment_id,
    attempt_number,
    status,
    failure_code,
    failure_message,
    started_at,
    completed_at
)
SELECT
    payment.id,
    payment.refund_attempts,
    'failed',
    'refund_provider_error',
    sqlc.arg(failure_message),
    sqlc.arg(refund_locked_at),
    now()
FROM scheduled_payment AS payment;

-- name: RecordSuccessfulPaymentRefundAttempt :execrows
INSERT INTO payment_refund_attempts (
    payment_id,
    attempt_number,
    status,
    started_at,
    completed_at
)
SELECT
    payment.id,
    payment.refund_attempts,
    'succeeded',
    payment.refund_locked_at,
    now()
FROM payments AS payment
WHERE payment.id = sqlc.arg(payment_id)
  AND payment.status = 'refund_pending'
  AND payment.refund_locked_at = sqlc.arg(refund_locked_at);

-- name: MarkPaymentRefunded :one
WITH refunded_payment AS (
    UPDATE payments AS payment
    SET
        status = 'refunded',
        failure_code = NULL,
        failure_message = NULL,
        refunded_at = now(),
        refund_available_at = NULL,
        refund_locked_at = NULL,
        updated_at = now()
    WHERE payment.id = sqlc.arg(payment_id)
      AND payment.status = 'refund_pending'
      AND payment.refund_locked_at = sqlc.arg(refund_locked_at)
    RETURNING
        payment.id,
        payment.booking_id,
        payment.provider,
        payment.provider_payment_id,
        payment.provider_idempotency_key,
        payment.amount_minor,
        payment.currency,
        payment.status,
        payment.failure_code,
        payment.failure_message,
        payment.paid_at,
        payment.refunded_at,
        payment.created_at,
        payment.updated_at,
        payment.refund_attempts,
        payment.refund_available_at,
        payment.refund_locked_at
),
enqueued_email AS (
    INSERT INTO email_outbox (
        booking_id,
        event_type,
        recipient_email,
        payload
    )
    SELECT
        b.id,
        'payment_refunded',
        u.email,
        jsonb_build_object(
            'booking_id', b.id,
            'guest_name', u.full_name,
            'hotel_name', h.name,
            'room_type_name', rt.name,
            'check_in', b.check_in,
            'check_out', b.check_out,
            'rooms_count', b.rooms_count,
            'guest_count', b.guest_count,
            'price_per_night', b.price_per_night::text,
            'total_price', b.total_price::text,
            'currency', b.currency
        )
    FROM refunded_payment AS p
    JOIN bookings AS b
        ON b.id = p.booking_id
    JOIN users AS u
        ON u.id = b.user_id
    JOIN room_types AS rt
        ON rt.id = b.room_type_id
    JOIN hotels AS h
        ON h.id = rt.hotel_id
    ON CONFLICT (booking_id, event_type) DO NOTHING
    RETURNING booking_id
)
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
    p.updated_at,
    p.refund_attempts,
    p.refund_available_at,
    p.refund_locked_at
FROM refunded_payment AS p
LEFT JOIN enqueued_email AS e
    ON e.booking_id = p.booking_id;

-- name: ListPaymentRefundAttemptsForAdmin :many
SELECT
    id,
    payment_id,
    attempt_number,
    status,
    failure_code,
    failure_message,
    started_at,
    completed_at
FROM payment_refund_attempts
WHERE payment_id = sqlc.arg(payment_id)
ORDER BY attempt_number DESC
OFFSET sqlc.arg(result_offset)::bigint
LIMIT sqlc.arg(result_limit)::int;
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
    p.updated_at,
    p.refund_attempts,
    p.refund_available_at,
    p.refund_locked_at
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
