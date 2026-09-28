CREATE TABLE room_types (
    id              BIGSERIAL PRIMARY KEY,
    hotel_id        BIGINT NOT NULL REFERENCES hotels (id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    description     TEXT,
    price_per_night NUMERIC(10, 2) NOT NULL CHECK (price_per_night >= 0),
    capacity        INT NOT NULL CHECK (capacity > 0),
    total_rooms     INT NOT NULL CHECK (total_rooms > 0),
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_room_types_hotel_id ON room_types (hotel_id);
CREATE INDEX idx_room_types_active_hotel_price
ON room_types (hotel_id, price_per_night, id)
WHERE is_active = true;