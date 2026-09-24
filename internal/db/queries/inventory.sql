-- name: SetBlockedInventory :execrows
UPDATE room_type_availability
SET
    rooms_blocked = sqlc.arg(rooms_blocked)::int,
    block_reason = CASE
        WHEN sqlc.arg(rooms_blocked)::int = 0 THEN NULL
        ELSE btrim(sqlc.arg(block_reason)::text)
    END
WHERE room_type_id = sqlc.arg(room_type_id)
  AND date >= sqlc.arg(date_from)::date
  AND date < sqlc.arg(date_to)::date
  AND rooms_booked + sqlc.arg(rooms_blocked)::int
      <= sqlc.arg(total_rooms)::int;
-- name: ListRoomTypeInventory :many
WITH requested_dates AS (
    SELECT generate_series(
        sqlc.arg(date_from)::date,
        sqlc.arg(date_to)::date - 1,
        interval '1 day'
    )::date AS date
)
SELECT
    requested_dates.date,
    rt.total_rooms,
    COALESCE(rta.rooms_booked, 0)::int AS rooms_booked,
    COALESCE(rta.rooms_blocked, 0)::int AS rooms_blocked,
    rta.block_reason,
    (
        rt.total_rooms
        - COALESCE(rta.rooms_booked, 0)
        - COALESCE(rta.rooms_blocked, 0)
    )::int AS rooms_available
FROM room_types AS rt
CROSS JOIN requested_dates
LEFT JOIN room_type_availability AS rta
    ON rta.room_type_id = rt.id
   AND rta.date = requested_dates.date
WHERE rt.id = sqlc.arg(room_type_id)
ORDER BY requested_dates.date;