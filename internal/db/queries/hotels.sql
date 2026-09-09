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
-- name: SearchAvailableHotels :many
WITH available_room_types AS (
    SELECT
        rt.id,
        rt.hotel_id,
        rt.price_per_night
    FROM room_types AS rt
    LEFT JOIN room_type_availability AS rta
        ON rta.room_type_id = rt.id
       AND rta.date >= sqlc.arg(check_in)::date
       AND rta.date < sqlc.arg(check_out)::date
    GROUP BY rt.id
    HAVING
        rt.total_rooms - COALESCE(MAX(rta.rooms_booked), 0)
            >= sqlc.arg(rooms_count)::int
        AND rt.capacity::bigint
            * sqlc.arg(rooms_count)::int
            >= sqlc.arg(guest_count)::int
),
matching_hotels AS (
    SELECT
        h.id,
        h.name,
        h.address,
        h.city,
        h.description,
        h.created_at,
        hi.image_url AS primary_image_url,
        MIN(art.price_per_night)::numeric(10,2) AS starting_price,
        COUNT(art.id)::int AS available_room_type_count
    FROM hotels AS h
    JOIN available_room_types AS art
        ON art.hotel_id = h.id
    LEFT JOIN hotel_images AS hi
        ON hi.hotel_id = h.id
       AND hi.is_primary = true
    WHERE lower(h.city) = lower(sqlc.arg(city))
    GROUP BY
        h.id,
        hi.image_url
)
SELECT *
FROM matching_hotels
ORDER BY
    CASE
        WHEN sqlc.arg(sort)::text = 'price_asc' THEN starting_price
    END ASC,
    CASE
        WHEN sqlc.arg(sort)::text = 'price_desc' THEN starting_price
    END DESC,
    id ASC
LIMIT sqlc.arg(result_limit)::bigint
OFFSET sqlc.arg(result_offset)::bigint;
