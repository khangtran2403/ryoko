-- name: CreateRoomType :one
INSERT INTO room_types (hotel_id, name, description, price_per_night, capacity, total_rooms)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetRoomTypeByID :one
SELECT * FROM room_types
WHERE id = $1;

-- name: ListRoomTypesByHotel :many
SELECT * FROM room_types
WHERE hotel_id = $1
ORDER BY price_per_night;

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

-- name: DeleteRoomType :one
DELETE FROM room_types
WHERE id = $1
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
        - COALESCE(MAX(rta.rooms_booked), 0)
    )::int AS rooms_available
FROM room_types AS rt
LEFT JOIN room_type_availability AS rta
    ON rta.room_type_id = rt.id
   AND rta.date >= sqlc.arg(check_in)::date
   AND rta.date < sqlc.arg(check_out)::date
WHERE rt.hotel_id = sqlc.arg(hotel_id)
GROUP BY rt.id
HAVING
    rt.total_rooms - COALESCE(MAX(rta.rooms_booked), 0)
        >= sqlc.arg(rooms_count)::int
    AND rt.capacity * sqlc.arg(rooms_count)::int
        >= sqlc.arg(guest_count)::int
ORDER BY rt.price_per_night, rt.id;