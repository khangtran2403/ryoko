-- name: CreateReviewForCompletedBooking :one
INSERT INTO reviews (
    booking_id,
    rating,
    comment
)
SELECT
    b.id,
    sqlc.arg(rating)::smallint,
    sqlc.narg(comment)::text
FROM bookings AS b
WHERE b.id = sqlc.arg(booking_id)
  AND b.user_id = sqlc.arg(user_id)
  AND b.status = 'completed'
RETURNING
    id,
    booking_id,
    rating,
    comment,
    created_at,
    updated_at;
-- name: GetReviewByID :one
SELECT
    r.id,
    r.rating,
    r.comment,
    r.created_at,
    r.updated_at,
    u.full_name AS reviewer_name,
    rt.name AS room_type_name,
    rt.hotel_id
FROM reviews AS r
JOIN bookings AS b
    ON b.id = r.booking_id
JOIN users AS u
    ON u.id = b.user_id
JOIN room_types AS rt
    ON rt.id = b.room_type_id
WHERE r.id = sqlc.arg(review_id);
-- name: ListReviewsByHotel :many
SELECT
    r.id,
    r.rating,
    r.comment,
    r.created_at,
    r.updated_at,
    u.full_name AS reviewer_name,
    rt.name AS room_type_name
FROM reviews AS r
JOIN bookings AS b
    ON b.id = r.booking_id
JOIN users AS u
    ON u.id = b.user_id
JOIN room_types AS rt
    ON rt.id = b.room_type_id
WHERE rt.hotel_id = sqlc.arg(hotel_id)
ORDER BY r.created_at DESC, r.id DESC;