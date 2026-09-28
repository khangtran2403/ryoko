CREATE TABLE amenities (
    id   BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL CHECK (btrim(name) <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX amenities_name_unique_ci
ON amenities (lower(name));

CREATE TABLE hotel_amenities (
    hotel_id    BIGINT NOT NULL REFERENCES hotels (id) ON DELETE CASCADE,
    amenity_id  BIGINT NOT NULL REFERENCES amenities (id) ON DELETE CASCADE,
    PRIMARY KEY (hotel_id, amenity_id)
);

CREATE INDEX idx_hotel_amenities_amenity_id ON hotel_amenities (amenity_id);
