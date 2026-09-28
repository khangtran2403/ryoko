CREATE INDEX idx_bookings_confirmed_check_out
ON bookings (check_out)
WHERE status = 'confirmed';