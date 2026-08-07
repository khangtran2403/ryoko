-- name: CreateAmenity :one
INSERT INTO amenities (name)
VALUES ($1)
RETURNING id, name, created_at;

-- name: ListAmenities :many
SELECT id, name, created_at
FROM amenities
ORDER BY name;

-- name: AddAmenityToHotel :one
INSERT INTO hotel_amenities (hotel_id, amenity_id)
VALUES ($1, $2)
RETURNING hotel_id, amenity_id;

-- name: ListAmenitiesByHotel :many
SELECT
    a.id,
    a.name,
    a.created_at
FROM amenities AS a
JOIN hotel_amenities AS ha
    ON ha.amenity_id = a.id
WHERE ha.hotel_id = $1
ORDER BY a.name;

-- name: RemoveAmenityFromHotel :one
DELETE FROM hotel_amenities
WHERE hotel_id = $1
  AND amenity_id = $2
RETURNING amenity_id;