-- name: GetRoomTypeForBooking :one
SELECT
    id,
    price_per_night,
    capacity,
    total_rooms
FROM room_types
WHERE id = sqlc.arg(room_type_id)
FOR SHARE;

-- name: EnsureAvailabilityRows :exec
INSERT INTO room_type_availability (
    room_type_id,
    date,
    rooms_booked
)
SELECT
    sqlc.arg(room_type_id)::bigint,
    sqlc.arg(check_in)::date + offsets.day_offset,
    0
FROM generate_series(
    0,
    (sqlc.arg(check_out)::date - sqlc.arg(check_in)::date) - 1
) AS offsets(day_offset)
ON CONFLICT (room_type_id, date) DO NOTHING;

-- name: LockAvailabilityRows :many
SELECT
    date,
    rooms_booked,
    rooms_blocked,
    block_reason
FROM room_type_availability
WHERE room_type_id = sqlc.arg(room_type_id)
  AND date >= sqlc.arg(check_in)::date
  AND date < sqlc.arg(check_out)::date
ORDER BY date
FOR UPDATE;

-- name: IncrementAvailability :execrows
UPDATE room_type_availability
SET rooms_booked = rooms_booked + sqlc.arg(rooms_count)::int
WHERE room_type_id = sqlc.arg(room_type_id)
  AND date >= sqlc.arg(check_in)::date
  AND date < sqlc.arg(check_out)::date
  AND rooms_booked 
      + rooms_blocked
      + sqlc.arg(rooms_count)::int
      <= sqlc.arg(total_rooms)::int;

-- name: CreateBooking :one
INSERT INTO bookings (
    user_id,
    room_type_id,
    check_in,
    check_out,
    rooms_count,
    guest_count,
    price_per_night,
    total_price
)
VALUES (
    sqlc.arg(user_id),
    sqlc.arg(room_type_id),
    sqlc.arg(check_in),
    sqlc.arg(check_out),
    sqlc.arg(rooms_count),
    sqlc.arg(guest_count),
    sqlc.arg(price_per_night),
    sqlc.arg(price_per_night)::numeric
        * (sqlc.arg(check_out)::date - sqlc.arg(check_in)::date)
        * sqlc.arg(rooms_count)::int
)
RETURNING
    id,
    user_id,
    room_type_id,
    check_in,
    check_out,
    rooms_count,
    guest_count,
    price_per_night,
    total_price,
    status,
    created_at,
    updated_at;
-- name: ListBookingsByUser :many
SELECT
    id,
    user_id,
    room_type_id,
    check_in,
    check_out,
    rooms_count,
    guest_count,
    price_per_night,
    total_price,
    status,
    created_at,
    updated_at
FROM bookings
WHERE user_id = sqlc.arg(user_id)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(result_limit)::bigint
OFFSET sqlc.arg(result_offset)::bigint;
-- name: GetBookingByIDForUser :one
SELECT
    id,
    user_id,
    room_type_id,
    check_in,
    check_out,
    rooms_count,
    guest_count,
    price_per_night,
    total_price,
    status,
    created_at,
    updated_at
FROM bookings
WHERE id = sqlc.arg(booking_id)
AND user_id = sqlc.arg(user_id);
-- name: GetBookingByIDForAdmin :one
SELECT
    id,
    user_id,
    room_type_id,
    check_in,
    check_out,
    rooms_count,
    guest_count,
    price_per_night,
    total_price,
    status,
    created_at,
    updated_at
FROM bookings
WHERE id = sqlc.arg(booking_id);
-- name: GetBookingForCancellation :one
SELECT
    id,
    user_id,
    room_type_id,
    check_in,
    check_out,
    rooms_count,
    guest_count,
    price_per_night,
    total_price,
    status,
    created_at,
    updated_at
FROM bookings
WHERE id = sqlc.arg(booking_id)
AND user_id = sqlc.arg(user_id)
FOR UPDATE;
-- name: DecrementAvailability :execrows
UPDATE room_type_availability
SET rooms_booked = rooms_booked - sqlc.arg(rooms_count)::int
WHERE room_type_id = sqlc.arg(room_type_id)
  AND date >= sqlc.arg(check_in)::date
  AND date < sqlc.arg(check_out)::date
  AND rooms_booked >= sqlc.arg(rooms_count)::int;
-- name: CancelBooking :one
UPDATE bookings
SET
    status = 'cancelled',
    updated_at = now()
WHERE id = sqlc.arg(booking_id)
  AND user_id = sqlc.arg(user_id)
  AND status = 'confirmed'
RETURNING
    id,
    user_id,
    room_type_id,
    check_in,
    check_out,
    rooms_count,
    guest_count,
    price_per_night,
    total_price,
    status,
    created_at,
    updated_at;
-- name: CompletePastBookings :one
WITH completed_bookings AS (
    UPDATE bookings
    SET
        status = 'completed',
        updated_at = now()
    WHERE status = 'confirmed'
      AND check_out <= sqlc.arg(today)::date
    RETURNING
        id,
        updated_at
),
recorded_history AS (
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
        'confirmed',
        'completed',
        NULL,
        'Stay completed automatically',
        updated_at
    FROM completed_bookings
    RETURNING booking_id
)
SELECT count(*)::bigint AS completed_count
FROM recorded_history;
-- name: ClaimBookingIdempotencyKey :execrows
INSERT INTO booking_idempotency_keys (
    user_id,
    idempotency_key,
    request_hash
)
VALUES (
    sqlc.arg(user_id),
    sqlc.arg(idempotency_key),
    sqlc.arg(request_hash)
)
ON CONFLICT (user_id, idempotency_key)
DO NOTHING;
-- name: GetBookingIdempotencyKey :one
SELECT
    request_hash,
    booking_id
FROM booking_idempotency_keys
WHERE user_id = sqlc.arg(user_id)
  AND idempotency_key = sqlc.arg(idempotency_key);
-- name: AttachBookingToIdempotencyKey :execrows
UPDATE booking_idempotency_keys
SET booking_id = sqlc.arg(booking_id)
WHERE user_id = sqlc.arg(user_id)
  AND idempotency_key = sqlc.arg(idempotency_key)
  AND request_hash = sqlc.arg(request_hash)
  AND booking_id IS NULL;
-- name: ListBookingsForAdmin :many
SELECT
    b.id AS booking_id,
    b.user_id,
    u.full_name AS customer_name,
    u.email AS customer_email,
    b.room_type_id,
    rt.name AS room_type_name,
    h.id AS hotel_id,
    h.name AS hotel_name,
    b.check_in,
    b.check_out,
    (b.check_out - b.check_in)::int AS number_of_nights,
    b.rooms_count,
    b.guest_count,
    b.price_per_night,
    b.total_price,
    b.status,
    b.created_at,
    b.updated_at
FROM bookings AS b
JOIN users AS u
    ON u.id = b.user_id
JOIN room_types AS rt
    ON rt.id = b.room_type_id
JOIN hotels AS h
    ON h.id = rt.hotel_id
WHERE (
    sqlc.narg(hotel_id)::bigint IS NULL
    OR h.id = sqlc.narg(hotel_id)::bigint
)
AND (
    sqlc.narg(status)::text IS NULL
    OR b.status = sqlc.narg(status)::text
)
AND (
    sqlc.narg(check_in_from)::date IS NULL
    OR b.check_in >= sqlc.narg(check_in_from)::date
)
AND (
    sqlc.narg(check_in_to)::date IS NULL
    OR b.check_in <= sqlc.narg(check_in_to)::date
)
ORDER BY b.created_at DESC, b.id DESC
LIMIT sqlc.arg(result_limit)::bigint
OFFSET sqlc.arg(result_offset)::bigint;
-- name: CreateBookingStatusHistory :one
INSERT INTO booking_status_history (
    booking_id,
    from_status,
    to_status,
    changed_by_user_id,
    reason
)
VALUES (
    sqlc.arg(booking_id),
    sqlc.narg(from_status)::text,
    sqlc.arg(to_status),
    sqlc.narg(changed_by_user_id)::bigint,
    sqlc.narg(reason)::text
)
RETURNING
    id,
    booking_id,
    from_status,
    to_status,
    changed_by_user_id,
    reason,
    created_at;
-- name: ListBookingStatusHistoryForUser :many
SELECT
    bsh.id,
    bsh.booking_id,
    bsh.from_status,
    bsh.to_status,
    bsh.changed_by_user_id,
    bsh.reason,
    bsh.created_at
FROM booking_status_history AS bsh
JOIN bookings AS b
    ON b.id = bsh.booking_id
WHERE bsh.booking_id = sqlc.arg(booking_id)
  AND b.user_id = sqlc.arg(user_id)
ORDER BY bsh.created_at ASC, bsh.id ASC;
-- name: ListBookingStatusHistoryForAdmin :many
SELECT
    id,
    booking_id,
    from_status,
    to_status,
    changed_by_user_id,
    reason,
    created_at
FROM booking_status_history
WHERE booking_id = sqlc.arg(booking_id)
ORDER BY created_at ASC, id ASC;
-- name: GetBookingForAdminCancellation :one
SELECT
    id,
    user_id,
    room_type_id,
    check_in,
    check_out,
    rooms_count,
    guest_count,
    price_per_night,
    total_price,
    status,
    created_at,
    updated_at
FROM bookings
WHERE id = sqlc.arg(booking_id)
FOR UPDATE;
-- name: CancelBookingForAdmin :one
UPDATE bookings
SET
    status = 'cancelled',
    updated_at = now()
WHERE id = sqlc.arg(booking_id)
  AND status = 'confirmed'
RETURNING
    id,
    user_id,
    room_type_id,
    check_in,
    check_out,
    rooms_count,
    guest_count,
    price_per_night,
    total_price,
    status,
    created_at,
    updated_at;