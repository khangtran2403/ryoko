CREATE TABLE hotel_images (
    id          BIGSERIAL PRIMARY KEY,
    hotel_id    BIGINT NOT NULL REFERENCES hotels(id) ON DELETE CASCADE,
    image_url   TEXT NOT NULL,
    is_primary  BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_hotel_images_hotel_id
ON hotel_images (hotel_id);

CREATE UNIQUE INDEX idx_hotel_images_one_primary
ON hotel_images (hotel_id)
WHERE is_primary = true;