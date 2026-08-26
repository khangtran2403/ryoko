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
    rooms_booked
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
  AND rooms_booked + sqlc.arg(rooms_count)::int
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
ORDER BY created_at DESC, id DESC;

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
-- name: CompletePastBookings :execrows
UPDATE bookings
SET
    status = 'completed',
    updated_at = now()
WHERE status = 'confirmed'
  AND check_out <= sqlc.arg(today)::date;