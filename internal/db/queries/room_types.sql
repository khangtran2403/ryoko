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