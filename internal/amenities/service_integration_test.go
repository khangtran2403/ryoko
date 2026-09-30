package amenities

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

func TestAmenityServiceLifecycle(t *testing.T) {
	pool, service := newAmenityIntegrationService(t)
	hotelID := insertAmenityHotel(t, pool, "Amenity Hotel")

	poolAmenity, err := service.CreateAmenity(context.Background(), "  Pool  ")
	if err != nil {
		t.Fatalf("CreateAmenity() error = %v", err)
	}
	if poolAmenity.Name != "Pool" {
		t.Errorf("amenity name = %q, want trimmed name", poolAmenity.Name)
	}
	if _, err := service.CreateAmenity(context.Background(), "pool"); !errors.Is(err, ErrAmenityNameConflict) {
		t.Fatalf("duplicate CreateAmenity() error = %v, want ErrAmenityNameConflict", err)
	}

	allAmenities, err := service.ListAmenities(context.Background())
	if err != nil {
		t.Fatalf("ListAmenities() error = %v", err)
	}
	if len(allAmenities) != 1 || allAmenities[0].ID != poolAmenity.ID {
		t.Fatalf("ListAmenities() = %+v, want created amenity", allAmenities)
	}

	attached, err := service.AddAmenityToHotel(context.Background(), hotelID, poolAmenity.ID)
	if err != nil {
		t.Fatalf("AddAmenityToHotel() error = %v", err)
	}
	if attached.HotelID != hotelID || attached.AmenityID != poolAmenity.ID {
		t.Errorf("attached amenity = %+v", attached)
	}
	if _, err := service.AddAmenityToHotel(context.Background(), hotelID, poolAmenity.ID); !errors.Is(err, ErrAmenityAlreadyAdded) {
		t.Fatalf("duplicate AddAmenityToHotel() error = %v, want ErrAmenityAlreadyAdded", err)
	}

	hotelAmenities, err := service.ListAmenitiesByHotel(context.Background(), hotelID)
	if err != nil {
		t.Fatalf("ListAmenitiesByHotel() error = %v", err)
	}
	if len(hotelAmenities) != 1 || hotelAmenities[0].ID != poolAmenity.ID {
		t.Fatalf("ListAmenitiesByHotel() = %+v, want attached amenity", hotelAmenities)
	}

	if err := service.RemoveAmenityFromHotel(context.Background(), hotelID, poolAmenity.ID); err != nil {
		t.Fatalf("RemoveAmenityFromHotel() error = %v", err)
	}
	if err := service.RemoveAmenityFromHotel(context.Background(), hotelID, poolAmenity.ID); !errors.Is(err, ErrHotelOrAmenityNotFound) {
		t.Fatalf("second RemoveAmenityFromHotel() error = %v, want ErrHotelOrAmenityNotFound", err)
	}

	hotelAmenities, err = service.ListAmenitiesByHotel(context.Background(), hotelID)
	if err != nil {
		t.Fatalf("empty ListAmenitiesByHotel() error = %v", err)
	}
	if hotelAmenities == nil || len(hotelAmenities) != 0 {
		t.Errorf("empty ListAmenitiesByHotel() = %#v, want initialized empty slice", hotelAmenities)
	}
}

func TestAmenityServiceUpdateAndDelete(t *testing.T) {
	pool, service := newAmenityIntegrationService(t)
	hotelID := insertAmenityHotel(t, pool, "Amenity Update Hotel")
	poolAmenity, err := service.CreateAmenity(context.Background(), "Pool")
	if err != nil {
		t.Fatalf("create pool amenity: %v", err)
	}
	spaAmenity, err := service.CreateAmenity(context.Background(), "Spa")
	if err != nil {
		t.Fatalf("create spa amenity: %v", err)
	}
	if _, err := service.AddAmenityToHotel(context.Background(), hotelID, poolAmenity.ID); err != nil {
		t.Fatalf("attach pool amenity: %v", err)
	}

	updated, err := service.UpdateAmenity(context.Background(), poolAmenity.ID, "  Rooftop Pool  ")
	if err != nil {
		t.Fatalf("UpdateAmenity() error = %v", err)
	}
	if updated.Name != "Rooftop Pool" {
		t.Errorf("updated name = %q, want Rooftop Pool", updated.Name)
	}
	if _, err := service.UpdateAmenity(context.Background(), poolAmenity.ID, "spa"); !errors.Is(err, ErrAmenityNameConflict) {
		t.Fatalf("conflicting UpdateAmenity() error = %v, want ErrAmenityNameConflict", err)
	}
	if _, err := service.UpdateAmenity(context.Background(), 999999, "Missing"); !errors.Is(err, ErrAmenityNotFound) {
		t.Fatalf("missing UpdateAmenity() error = %v, want ErrAmenityNotFound", err)
	}

	if err := service.DeleteAmenity(context.Background(), poolAmenity.ID); err != nil {
		t.Fatalf("DeleteAmenity() error = %v", err)
	}
	if err := service.DeleteAmenity(context.Background(), poolAmenity.ID); !errors.Is(err, ErrAmenityNotFound) {
		t.Fatalf("second DeleteAmenity() error = %v, want ErrAmenityNotFound", err)
	}

	var relationExists bool
	if err := pool.QueryRow(
		context.Background(),
		"SELECT EXISTS (SELECT 1 FROM hotel_amenities WHERE hotel_id = $1 AND amenity_id = $2)",
		hotelID,
		poolAmenity.ID,
	).Scan(&relationExists); err != nil {
		t.Fatalf("check cascaded relation: %v", err)
	}
	if relationExists {
		t.Error("hotel amenity relation still exists after deleting the amenity")
	}

	remaining, err := service.ListAmenities(context.Background())
	if err != nil {
		t.Fatalf("ListAmenities() error = %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != spaAmenity.ID {
		t.Errorf("remaining amenities = %+v, want only spa", remaining)
	}
}

func TestAmenityServiceRejectsMissingAndInactiveHotels(t *testing.T) {
	pool, service := newAmenityIntegrationService(t)
	amenity, err := service.CreateAmenity(context.Background(), "Spa")
	if err != nil {
		t.Fatalf("CreateAmenity() error = %v", err)
	}

	if _, err := service.AddAmenityToHotel(context.Background(), 999999, amenity.ID); !errors.Is(err, ErrHotelOrAmenityNotFound) {
		t.Fatalf("missing-hotel AddAmenityToHotel() error = %v, want ErrHotelOrAmenityNotFound", err)
	}
	if _, err := service.ListAmenitiesByHotel(context.Background(), 999999); !errors.Is(err, ErrHotelNotFound) {
		t.Fatalf("missing-hotel ListAmenitiesByHotel() error = %v, want ErrHotelNotFound", err)
	}

	hotelID := insertAmenityHotel(t, pool, "Inactive Amenity Hotel")
	if _, err := service.AddAmenityToHotel(context.Background(), hotelID, amenity.ID); err != nil {
		t.Fatalf("initial AddAmenityToHotel() error = %v", err)
	}
	if _, err := pool.Exec(context.Background(), "UPDATE hotels SET is_active = false WHERE id = $1", hotelID); err != nil {
		t.Fatalf("deactivate hotel: %v", err)
	}

	secondAmenity, err := service.CreateAmenity(context.Background(), "Sauna")
	if err != nil {
		t.Fatalf("create second amenity: %v", err)
	}
	if _, err := service.AddAmenityToHotel(context.Background(), hotelID, secondAmenity.ID); !errors.Is(err, ErrHotelOrAmenityNotFound) {
		t.Fatalf("inactive-hotel AddAmenityToHotel() error = %v, want ErrHotelOrAmenityNotFound", err)
	}
	if _, err := service.ListAmenitiesByHotel(context.Background(), hotelID); !errors.Is(err, ErrHotelNotFound) {
		t.Fatalf("inactive-hotel ListAmenitiesByHotel() error = %v, want ErrHotelNotFound", err)
	}
	if err := service.RemoveAmenityFromHotel(context.Background(), hotelID, amenity.ID); !errors.Is(err, ErrHotelOrAmenityNotFound) {
		t.Fatalf("inactive-hotel RemoveAmenityFromHotel() error = %v, want ErrHotelOrAmenityNotFound", err)
	}

	var relationStillExists bool
	if err := pool.QueryRow(
		context.Background(),
		"SELECT EXISTS (SELECT 1 FROM hotel_amenities WHERE hotel_id = $1 AND amenity_id = $2)",
		hotelID,
		amenity.ID,
	).Scan(&relationStillExists); err != nil {
		t.Fatalf("check preserved relation: %v", err)
	}
	if !relationStillExists {
		t.Error("inactive-hotel removal deleted the hotel amenity relation")
	}
}

func newAmenityIntegrationService(t *testing.T) (*pgxpool.Pool, *Service) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL amenity integration tests")
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

	schemaName := randomAmenitySchemaName(t)
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
		if _, err := adminPool.Exec(cleanupCtx, "DROP SCHEMA "+schemaIdentifier+" CASCADE"); err != nil {
			t.Errorf("drop integration schema: %v", err)
		}
		adminPool.Close()
	})

	applyAmenityMigrations(t, pool)
	queries := sqlc.New(pool)
	return pool, NewService(queries)
}

func applyAmenityMigrations(t *testing.T, pool *pgxpool.Pool) {
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

func insertAmenityHotel(t *testing.T, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	var hotelID int64
	if err := pool.QueryRow(
		context.Background(),
		`INSERT INTO hotels (name, address, city)
		 VALUES ($1, '1 Amenity Street', 'Amenity City')
		 RETURNING id`,
		name,
	).Scan(&hotelID); err != nil {
		t.Fatalf("insert hotel: %v", err)
	}
	return hotelID
}

func randomAmenitySchemaName(t *testing.T) string {
	t.Helper()
	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		t.Fatalf("generate random schema name: %v", err)
	}
	return fmt.Sprintf("ryoko_amenity_test_%s", hex.EncodeToString(randomBytes[:]))
}
