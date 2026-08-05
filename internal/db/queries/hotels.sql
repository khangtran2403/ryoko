-- name: CreateHotel :one
INSERT INTO hotels (name, address, city, description)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetHotelByID :one
SELECT * FROM hotels
WHERE id = $1;

-- name: ListHotelsByCity :many
SELECT * FROM hotels
WHERE city = $1
ORDER BY name;
-- name: UpdateHotel :one
UPDATE hotels
SET
    name = $2,
    address = $3,
    city = $4,
    description = $5
WHERE id = $1
RETURNING *;

-- name: DeleteHotel :one
DELETE FROM hotels
WHERE id = $1
RETURNING id;