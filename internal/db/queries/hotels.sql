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