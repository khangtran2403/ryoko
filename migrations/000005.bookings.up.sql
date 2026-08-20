CREATE TABLE room_type_availability (
    room_type_id BIGINT NOT NULL
        REFERENCES room_types (id) ON DELETE CASCADE,
    date         DATE NOT NULL,
    rooms_booked INT NOT NULL DEFAULT 0
        CHECK (rooms_booked >= 0),

    PRIMARY KEY (room_type_id, date)
);

CREATE TABLE bookings (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL
        REFERENCES users (id) ON DELETE RESTRICT,
    room_type_id    BIGINT NOT NULL
        REFERENCES room_types (id) ON DELETE RESTRICT,
    check_in        DATE NOT NULL,
    check_out       DATE NOT NULL,
    rooms_count     INT NOT NULL CHECK (rooms_count > 0),
    guest_count     INT NOT NULL CHECK (guest_count > 0),
    price_per_night NUMERIC(10, 2) NOT NULL
        CHECK (price_per_night >= 0),
    total_price     NUMERIC(14, 2) NOT NULL
        CHECK (total_price >= 0),
    status          TEXT NOT NULL DEFAULT 'confirmed'
        CHECK (status IN ('confirmed', 'cancelled', 'completed')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (check_out > check_in)
);

CREATE INDEX idx_bookings_user_id_created_at
ON bookings (user_id, created_at DESC);

CREATE INDEX idx_bookings_room_type_id
ON bookings (room_type_id);