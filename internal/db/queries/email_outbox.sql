-- name: EnqueueBookingEmail :execrows
INSERT INTO email_outbox (
    booking_id,
    event_type,
    recipient_email,
    payload
)
SELECT
    b.id,
    sqlc.arg(event_type),
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
FROM bookings AS b
JOIN users AS u
    ON u.id = b.user_id
JOIN room_types AS rt
    ON rt.id = b.room_type_id
JOIN hotels AS h
    ON h.id = rt.hotel_id
WHERE b.id = sqlc.arg(booking_id)
ON CONFLICT (booking_id, event_type) DO NOTHING;


-- name: ClaimEmailOutboxMessages :many
WITH claimable AS (
    SELECT id
    FROM email_outbox
    WHERE (
        status = 'pending'
        AND available_at <= now()
    )
    OR (
        status = 'processing'
        AND locked_at <= now()
            - make_interval(
                secs => sqlc.arg(lease_seconds)::int
            )
    )
    ORDER BY
        CASE
            WHEN status = 'processing' THEN locked_at
            ELSE available_at
        END,
        id
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg(batch_size)::int
)
UPDATE email_outbox AS e
SET
    status = 'processing',
    attempts = e.attempts + 1,
    locked_at = now(),
    last_error = NULL,
    updated_at = now()
FROM claimable AS c
WHERE e.id = c.id
RETURNING
    e.id,
    e.booking_id,
    e.event_type,
    e.recipient_email,
    e.payload,
    e.status,
    e.attempts,
    e.available_at,
    e.locked_at,
    e.sent_at,
    e.last_error,
    e.created_at,
    e.updated_at;


-- name: MarkEmailOutboxSent :execrows
UPDATE email_outbox
SET
    status = 'sent',
    sent_at = now(),
    locked_at = NULL,
    last_error = NULL,
    updated_at = now()
WHERE id = sqlc.arg(email_id)
  AND status = 'processing';


-- name: ScheduleEmailOutboxRetry :execrows
UPDATE email_outbox
SET
    status = 'pending',
    available_at = now()
        + make_interval(
            secs => sqlc.arg(retry_after_seconds)::int
        ),
    locked_at = NULL,
    last_error = sqlc.arg(last_error),
    updated_at = now()
WHERE id = sqlc.arg(email_id)
  AND status = 'processing';


-- name: MarkEmailOutboxFailed :execrows
UPDATE email_outbox
SET
    status = 'failed',
    locked_at = NULL,
    last_error = sqlc.arg(last_error),
    updated_at = now()
WHERE id = sqlc.arg(email_id)
  AND status = 'processing';


-- name: GetEmailOutboxByID :one
SELECT
    id,
    booking_id,
    event_type,
    recipient_email,
    payload,
    status,
    attempts,
    available_at,
    locked_at,
    sent_at,
    last_error,
    created_at,
    updated_at
FROM email_outbox
WHERE id = sqlc.arg(email_id);


-- name: ListEmailOutboxForAdmin :many
SELECT
    id,
    booking_id,
    event_type,
    recipient_email,
    status,
    attempts,
    available_at,
    locked_at,
    sent_at,
    last_error,
    created_at,
    updated_at
FROM email_outbox
WHERE (
    sqlc.narg(status)::text IS NULL
    OR status = sqlc.narg(status)
)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(result_limit)::int
OFFSET sqlc.arg(result_offset)::bigint;


-- name: RetryFailedEmailOutbox :one
UPDATE email_outbox
SET
    status = 'pending',
    attempts = 0,
    available_at = now(),
    locked_at = NULL,
    sent_at = NULL,
    last_error = NULL,
    updated_at = now()
WHERE id = sqlc.arg(email_id)
  AND status = 'failed'
RETURNING
    id,
    booking_id,
    event_type,
    recipient_email,
    status,
    attempts,
    available_at,
    locked_at,
    sent_at,
    last_error,
    created_at,
    updated_at;


-- name: DeleteSentEmailOutboxBefore :execrows
DELETE FROM email_outbox
WHERE status = 'sent'
  AND sent_at < sqlc.arg(sent_before);
