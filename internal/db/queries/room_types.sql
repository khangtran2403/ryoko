-- name: CreateRoomType :one
INSERT INTO room_types (
    hotel_id,
    name,
    description,
    price_per_night,
    capacity,
    total_rooms
)
SELECT
    h.id,
    sqlc.arg(name)::text,
    sqlc.narg(description)::text,
    sqlc.arg(price_per_night)::numeric,
    sqlc.arg(capacity)::int,
    sqlc.arg(total_rooms)::int
FROM hotels AS h
WHERE h.id = sqlc.arg(hotel_id)
  AND h.is_active = true
RETURNING *;

-- name: GetRoomTypeByID :one
SELECT rt.*
FROM room_types AS rt
JOIN hotels AS h
    ON h.id = rt.hotel_id
WHERE rt.id = $1
  AND rt.is_active = true
  AND h.is_active = true;

-- name: GetRoomTypeForUpdate :one
SELECT rt.*
FROM room_types AS rt
JOIN hotels AS h
    ON h.id = rt.hotel_id
WHERE rt.id = $1
  AND rt.is_active = true
  AND h.is_active = true
FOR SHARE OF h
FOR UPDATE OF rt;

-- name: GetMaxRoomTypeInventoryUsage :one
SELECT COALESCE(MAX(rooms_booked + rooms_blocked), 0)::int
FROM room_type_availability
WHERE room_type_id = $1;

-- name: UpdateRoomType :one
UPDATE room_types
SET
    name = $2,
    description = $3,
    price_per_night = $4,
    capacity = $5,
    total_rooms = $6
WHERE id = $1
RETURNING *;

-- name: DeactivateRoomType :one
UPDATE room_types
SET is_active = false
WHERE id = $1
  AND is_active = true
RETURNING id;
-- name: ListAvailableRoomTypes :many
SELECT
    rt.id,
    rt.hotel_id,
    rt.name,
    rt.description,
    rt.price_per_night,
    rt.capacity,
    rt.total_rooms,
    rt.created_at,
    (
         rt.total_rooms
    - COALESCE(MAX(rta.rooms_booked + rta.rooms_blocked), 0)
    )::int AS rooms_available
FROM room_types AS rt
JOIN hotels AS h
    ON h.id = rt.hotel_id
LEFT JOIN room_type_availability AS rta
    ON rta.room_type_id = rt.id
   AND rta.date >= sqlc.arg(check_in)::date
   AND rta.date < sqlc.arg(check_out)::date
WHERE rt.hotel_id = sqlc.arg(hotel_id)
  AND rt.is_active = true
  AND  h.is_active = true
GROUP BY rt.id
HAVING
    rt.total_rooms
        - COALESCE(MAX(rta.rooms_booked + rta.rooms_blocked), 0)
        >= sqlc.arg(rooms_count)::int
    AND rt.capacity * sqlc.arg(rooms_count)::int
        >= sqlc.arg(guest_count)::int
ORDER BY rt.price_per_night, rt.id;
-- name: ListRoomTypesByHotel :many
SELECT rt.*
FROM room_types AS rt
JOIN hotels AS h
    ON h.id = rt.hotel_id
WHERE rt.hotel_id = $1
  AND rt.is_active = true
  AND h.is_active = true
ORDER BY rt.price_per_night, rt.id;