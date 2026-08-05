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