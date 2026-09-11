package hotel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

func TestGetHotelDetailsByIDIntegration(t *testing.T) {
	pool, service := newHotelIntegrationService(t)
	ctx := context.Background()

	var hotelID int64
	if err := pool.QueryRow(
		ctx,
		"INSERT INTO hotels (name, address, city, description) "+
			"VALUES ('Riverside Hotel', '1 River Street', 'Da Nang', 'River view') "+
			"RETURNING id",
	).Scan(&hotelID); err != nil {
		t.Fatalf("insert hotel: %v", err)
	}

	var standardID int64
	if err := pool.QueryRow(
		ctx,
		"INSERT INTO room_types "+
			"(hotel_id, name, price_per_night, capacity, total_rooms) "+
			"VALUES ($1, 'Standard', 100.00, 2, 5) RETURNING id",
		hotelID,
	).Scan(&standardID); err != nil {
		t.Fatalf("insert standard room type: %v", err)
	}
	var suiteID int64
	if err := pool.QueryRow(
		ctx,
		"INSERT INTO room_types "+
			"(hotel_id, name, price_per_night, capacity, total_rooms) "+
			"VALUES ($1, 'Suite', 250.00, 4, 2) RETURNING id",
		hotelID,
	).Scan(&suiteID); err != nil {
		t.Fatalf("insert suite room type: %v", err)
	}

	if _, err := pool.Exec(
		ctx,
		"INSERT INTO hotel_images (hotel_id, image_url, is_primary) VALUES "+
			"($1, 'https://example.com/secondary.jpg', false), "+
			"($1, 'https://example.com/primary.jpg', true)",
		hotelID,
	); err != nil {
		t.Fatalf("insert hotel images: %v", err)
	}

	var wifiID int64
	if err := pool.QueryRow(
		ctx,
		"INSERT INTO amenities (name) VALUES ('Wi-Fi') RETURNING id",
	).Scan(&wifiID); err != nil {
		t.Fatalf("insert Wi-Fi amenity: %v", err)
	}
	var poolAmenityID int64
	if err := pool.QueryRow(
		ctx,
		"INSERT INTO amenities (name) VALUES ('Pool') RETURNING id",
	).Scan(&poolAmenityID); err != nil {
		t.Fatalf("insert pool amenity: %v", err)
	}
	if _, err := pool.Exec(
		ctx,
		"INSERT INTO hotel_amenities (hotel_id, amenity_id) VALUES ($1, $2), ($1, $3)",
		hotelID,
		wifiID,
		poolAmenityID,
	); err != nil {
		t.Fatalf("associate amenities: %v", err)
	}

	var userID int64
	if err := pool.QueryRow(
		ctx,
		"INSERT INTO users (email, full_name) "+
			"VALUES ('hotel-details@example.com', 'Hotel Details Reviewer') RETURNING id",
	).Scan(&userID); err != nil {
		t.Fatalf("insert reviewer: %v", err)
	}

	insertReview := func(roomTypeID int64, rating int16, deleted bool) {
		t.Helper()

		var bookingID int64
		if err := pool.QueryRow(
			ctx,
			"INSERT INTO bookings "+
				"(user_id, room_type_id, check_in, check_out, rooms_count, guest_count, "+
				"price_per_night, total_price, status) "+
				"VALUES ($1, $2, '2029-01-10', '2029-01-11', 1, 1, 100.00, 100.00, 'completed') "+
				"RETURNING id",
			userID,
			roomTypeID,
		).Scan(&bookingID); err != nil {
			t.Fatalf("insert booking: %v", err)
		}

		if deleted {
			if _, err := pool.Exec(
				ctx,
				"INSERT INTO reviews (booking_id, rating, deleted_at) VALUES ($1, $2, now())",
				bookingID,
				rating,
			); err != nil {
				t.Fatalf("insert deleted review: %v", err)
			}
			return
		}
		if _, err := pool.Exec(
			ctx,
			"INSERT INTO reviews (booking_id, rating) VALUES ($1, $2)",
			bookingID,
			rating,
		); err != nil {
			t.Fatalf("insert review: %v", err)
		}
	}

	insertReview(standardID, 5, false)
	insertReview(suiteID, 3, false)
	insertReview(standardID, 1, true)

	details, err := service.GetHotelDetailsByID(ctx, hotelID)
	if err != nil {
		t.Fatalf("GetHotelDetailsByID() error = %v", err)
	}
	if details.ID != hotelID ||
		details.Name != "Riverside Hotel" ||
		details.City != "Da Nang" {
		t.Errorf("hotel details = %+v", details)
	}
	if len(details.Images) != 2 {
		t.Fatalf("images = %+v, want 2", details.Images)
	}
	if !details.Images[0].IsPrimary ||
		details.Images[0].ImageUrl != "https://example.com/primary.jpg" {
		t.Errorf("first image = %+v, want primary image", details.Images[0])
	}
	if len(details.Amenities) != 2 ||
		details.Amenities[0].Name != "Pool" ||
		details.Amenities[1].Name != "Wi-Fi" {
		t.Errorf("amenities = %+v, want [Pool, Wi-Fi]", details.Amenities)
	}
	if len(details.RoomTypes) != 2 ||
		details.RoomTypes[0].ID != standardID ||
		details.RoomTypes[1].ID != suiteID {
		t.Errorf("room types = %+v, want price-ordered standard and suite", details.RoomTypes)
	}
	if details.ReviewSummary.ReviewCount != 2 {
		t.Errorf("review count = %d, want 2", details.ReviewSummary.ReviewCount)
	}
	averageRating, err := details.ReviewSummary.AverageRating.Value()
	if err != nil {
		t.Fatalf("read average rating: %v", err)
	}
	if averageRating != "4.00" {
		t.Errorf("average rating = %v, want 4.00", averageRating)
	}

	_, err = service.GetHotelDetailsByID(ctx, hotelID+999999)
	if !errors.Is(err, ErrHotelNotFound) {
		t.Errorf("missing hotel error = %v, want ErrHotelNotFound", err)
	}
}

func TestGetHotelDetailsByIDReturnsEmptyCollections(t *testing.T) {
	pool, service := newHotelIntegrationService(t)

	var hotelID int64
	if err := pool.QueryRow(
		context.Background(),
		"INSERT INTO hotels (name, address, city) "+
			"VALUES ('Empty Hotel', '1 Empty Street', 'Hue') RETURNING id",
	).Scan(&hotelID); err != nil {
		t.Fatalf("insert empty hotel: %v", err)
	}

	details, err := service.GetHotelDetailsByID(context.Background(), hotelID)
	if err != nil {
		t.Fatalf("GetHotelDetailsByID() error = %v", err)
	}
	if details.Images == nil || details.Amenities == nil || details.RoomTypes == nil {
		t.Fatalf(
			"empty collections contain nil: images=%v amenities=%v room_types=%v",
			details.Images,
			details.Amenities,
			details.RoomTypes,
		)
	}
	if len(details.Images) != 0 ||
		len(details.Amenities) != 0 ||
		len(details.RoomTypes) != 0 {
		t.Errorf("empty hotel details = %+v", details)
	}
	if details.ReviewSummary.ReviewCount != 0 {
		t.Errorf("review count = %d, want 0", details.ReviewSummary.ReviewCount)
	}
}

func newHotelIntegrationService(t *testing.T) (*pgxpool.Pool, *Service) {
	t.Helper()

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL hotel integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	adminPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create integration admin pool: %v", err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		t.Fatalf("ping integration database: %v", err)
	}

	schemaName := randomHotelSchemaName(t)
	schemaIdentifier := pgx.Identifier{schemaName}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schemaIdentifier); err != nil {
		adminPool.Close()
		t.Fatalf("create integration schema: %v", err)
	}

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		adminPool.Exec(context.Background(), "DROP SCHEMA "+schemaIdentifier+" CASCADE")
		adminPool.Close()
		t.Fatalf("parse integration database URL: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schemaName

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		adminPool.Exec(context.Background(), "DROP SCHEMA "+schemaIdentifier+" CASCADE")
		adminPool.Close()
		t.Fatalf("create isolated integration pool: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := adminPool.Exec(
			cleanupCtx,
			"DROP SCHEMA "+schemaIdentifier+" CASCADE",
		); err != nil {
			t.Errorf("drop integration schema: %v", err)
		}
		adminPool.Close()
	})

	applyHotelIntegrationMigrations(t, pool)
	return pool, NewService(sqlc.New(pool))
}

func applyHotelIntegrationMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("find migrations: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no up migrations found")
	}
	sort.Strings(paths)

	for _, path := range paths {
		migration, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read migration %s: %v", path, err)
		}
		if _, err := pool.Exec(context.Background(), string(migration)); err != nil {
			t.Fatalf("apply migration %s: %v", path, err)
		}
	}
}

func randomHotelSchemaName(t *testing.T) string {
	t.Helper()

	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		t.Fatalf("generate random schema name: %v", err)
	}
	return fmt.Sprintf("ryoko_hotel_test_%s", hex.EncodeToString(randomBytes[:]))
}
