-- name: CreateHotelImage :one
INSERT INTO hotel_images (
    hotel_id,
    image_url
)
VALUES (
    sqlc.arg(hotel_id),
    sqlc.arg(image_url)
)
RETURNING *;

-- name: ListHotelImages :many
SELECT *
FROM hotel_images
WHERE hotel_id = sqlc.arg(hotel_id)
ORDER BY is_primary DESC, created_at ASC, id ASC;

-- name: ClearPrimaryHotelImage :exec
UPDATE hotel_images
SET is_primary = false
WHERE hotel_id = sqlc.arg(hotel_id)
  AND is_primary = true;

-- name: SetPrimaryHotelImage :one
UPDATE hotel_images
SET is_primary = true
WHERE id = sqlc.arg(image_id)
  AND hotel_id = sqlc.arg(hotel_id)
RETURNING *;

-- name: DeleteHotelImage :one
DELETE FROM hotel_images
WHERE id = sqlc.arg(image_id)
  AND hotel_id = sqlc.arg(hotel_id)
RETURNING id;
-- name: LockHotelForImageUpdate :one
SELECT id
FROM hotels
WHERE id = sqlc.arg(hotel_id)
FOR UPDATE;