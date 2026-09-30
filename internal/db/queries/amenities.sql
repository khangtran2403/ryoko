-- name: CreateAmenity :one
INSERT INTO amenities (name)
VALUES ($1)
RETURNING
    id,
    name,
    created_at;

-- name: ListAmenities :many
SELECT
    id,
    name,
    created_at
FROM amenities
ORDER BY name;

-- name: AddAmenityToHotel :one
WITH active_hotel AS (
    SELECT h.id
    FROM hotels AS h
    WHERE h.id = sqlc.arg(hotel_id)
      AND h.is_active = true
    FOR SHARE OF h
),
requested_amenity AS (
    SELECT a.id
    FROM amenities AS a
    WHERE a.id = sqlc.arg(amenity_id)
)
INSERT INTO hotel_amenities (
    hotel_id,
    amenity_id
)
SELECT
    active_hotel.id,
    requested_amenity.id
FROM active_hotel
CROSS JOIN requested_amenity
RETURNING
    hotel_id,
    amenity_id;

-- name: ListAmenitiesByHotel :many
SELECT
    a.id,
    a.name,
    a.created_at
FROM amenities AS a
JOIN hotel_amenities AS ha
    ON ha.amenity_id = a.id
JOIN hotels AS h
    ON h.id = ha.hotel_id
WHERE ha.hotel_id = sqlc.arg(hotel_id)
  AND h.is_active = true
ORDER BY a.name, a.id;

-- name: RemoveAmenityFromHotel :one
WITH active_hotel AS (
    SELECT h.id
    FROM hotels AS h
    WHERE h.id = sqlc.arg(hotel_id)
      AND h.is_active = true
    FOR SHARE OF h
)
DELETE FROM hotel_amenities AS ha
USING active_hotel
WHERE ha.hotel_id = active_hotel.id
  AND ha.amenity_id = sqlc.arg(amenity_id)
RETURNING ha.amenity_id;
-- name: UpdateAmenity :one
UPDATE amenities
SET name = sqlc.arg(name)
WHERE id = sqlc.arg(amenity_id)
RETURNING
    id,
    name,
    created_at;

-- name: DeleteAmenity :one
DELETE FROM amenities
WHERE id = sqlc.arg(amenity_id)
RETURNING id;