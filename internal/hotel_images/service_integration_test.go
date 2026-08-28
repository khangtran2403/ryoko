package hotel_images

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

func TestCreateAndListHotelImages(t *testing.T) {
	pool, service := newHotelImageIntegrationService(t)
	hotelID := insertHotelImageHotel(t, pool, "Image Hotel")

	first, err := service.CreateHotelImage(
		context.Background(),
		hotelID,
		"  https://example.com/first.jpg  ",
	)
	if err != nil {
		t.Fatalf("first CreateHotelImage() error = %v", err)
	}
	second, err := service.CreateHotelImage(
		context.Background(),
		hotelID,
		"https://example.com/second.jpg",
	)
	if err != nil {
		t.Fatalf("second CreateHotelImage() error = %v", err)
	}
	if first.ImageUrl != "https://example.com/first.jpg" {
		t.Errorf("first URL = %q, want trimmed URL", first.ImageUrl)
	}
	if first.IsPrimary || second.IsPrimary {
		t.Errorf("new images primary state = {%v %v}, want {false false}", first.IsPrimary, second.IsPrimary)
	}

	listed, err := service.ListHotelImages(context.Background(), hotelID)
	if err != nil {
		t.Fatalf("ListHotelImages() error = %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("ListHotelImages() length = %d, want 2", len(listed))
	}
	if listed[0].ID != first.ID || listed[1].ID != second.ID {
		t.Errorf("listed IDs = {%d %d}, want {%d %d}", listed[0].ID, listed[1].ID, first.ID, second.ID)
	}
}

func TestListHotelImagesReturnsInitializedEmptySlice(t *testing.T) {
	pool, service := newHotelImageIntegrationService(t)
	hotelID := insertHotelImageHotel(t, pool, "Empty Image Hotel")

	listed, err := service.ListHotelImages(context.Background(), hotelID)
	if err != nil {
		t.Fatalf("ListHotelImages() error = %v", err)
	}
	if listed == nil {
		t.Fatal("ListHotelImages() returned nil, want initialized empty slice")
	}
	if len(listed) != 0 {
		t.Errorf("ListHotelImages() length = %d, want 0", len(listed))
	}
}

func TestSetPrimaryHotelImageSwitchesPrimary(t *testing.T) {
	pool, service := newHotelImageIntegrationService(t)
	hotelID := insertHotelImageHotel(t, pool, "Primary Hotel")
	first := createHotelImageFixture(t, service, hotelID, "https://example.com/first.jpg")
	second := createHotelImageFixture(t, service, hotelID, "https://example.com/second.jpg")

	if _, err := service.SetPrimaryHotelImage(context.Background(), hotelID, first.ID); err != nil {
		t.Fatalf("set first primary: %v", err)
	}
	updated, err := service.SetPrimaryHotelImage(context.Background(), hotelID, second.ID)
	if err != nil {
		t.Fatalf("set second primary: %v", err)
	}
	if !updated.IsPrimary || updated.ID != second.ID {
		t.Errorf("updated image = %+v, want second image as primary", updated)
	}

	listed, err := service.ListHotelImages(context.Background(), hotelID)
	if err != nil {
		t.Fatalf("ListHotelImages() error = %v", err)
	}
	if len(listed) != 2 || listed[0].ID != second.ID || !listed[0].IsPrimary {
		t.Fatalf("ordered images = %+v, want second image first and primary", listed)
	}
	if listed[1].ID != first.ID || listed[1].IsPrimary {
		t.Errorf("old primary image = %+v, want first image non-primary", listed[1])
	}
}

func TestSetPrimaryHotelImageRollsBackWhenTargetIsMissing(t *testing.T) {
	pool, service := newHotelImageIntegrationService(t)
	hotelID := insertHotelImageHotel(t, pool, "Rollback Hotel")
	first := createHotelImageFixture(t, service, hotelID, "https://example.com/first.jpg")

	if _, err := service.SetPrimaryHotelImage(context.Background(), hotelID, first.ID); err != nil {
		t.Fatalf("set initial primary: %v", err)
	}
	_, err := service.SetPrimaryHotelImage(context.Background(), hotelID, 999999)
	if !errors.Is(err, ErrImageNotFound) {
		t.Fatalf("SetPrimaryHotelImage() error = %v, want ErrImageNotFound", err)
	}

	var stillPrimary bool
	if err := pool.QueryRow(
		context.Background(),
		"SELECT is_primary FROM hotel_images WHERE id = $1",
		first.ID,
	).Scan(&stillPrimary); err != nil {
		t.Fatalf("read original primary image: %v", err)
	}
	if !stillPrimary {
		t.Error("original image lost primary status after rolled-back operation")
	}
}

func TestHotelImageMutationsAreScopedToHotel(t *testing.T) {
	pool, service := newHotelImageIntegrationService(t)
	firstHotelID := insertHotelImageHotel(t, pool, "First Hotel")
	secondHotelID := insertHotelImageHotel(t, pool, "Second Hotel")
	secondHotelImage := createHotelImageFixture(
		t,
		service,
		secondHotelID,
		"https://example.com/second-hotel.jpg",
	)

	_, err := service.SetPrimaryHotelImage(context.Background(), firstHotelID, secondHotelImage.ID)
	if !errors.Is(err, ErrImageNotFound) {
		t.Fatalf("cross-hotel SetPrimaryHotelImage() error = %v, want ErrImageNotFound", err)
	}
	if err := service.DeleteHotelImage(context.Background(), firstHotelID, secondHotelImage.ID); !errors.Is(err, ErrImageNotFound) {
		t.Fatalf("cross-hotel DeleteHotelImage() error = %v, want ErrImageNotFound", err)
	}

	var exists bool
	if err := pool.QueryRow(
		context.Background(),
		"SELECT EXISTS (SELECT 1 FROM hotel_images WHERE id = $1)",
		secondHotelImage.ID,
	).Scan(&exists); err != nil {
		t.Fatalf("check second hotel's image: %v", err)
	}
	if !exists {
		t.Error("cross-hotel mutation removed the image")
	}
}

func TestHotelImageServiceMapsMissingResources(t *testing.T) {
	pool, service := newHotelImageIntegrationService(t)

	_, err := service.CreateHotelImage(
		context.Background(),
		999999,
		"https://example.com/missing-hotel.jpg",
	)
	if !errors.Is(err, ErrHotelNotFound) {
		t.Fatalf("CreateHotelImage() error = %v, want ErrHotelNotFound", err)
	}
	_, err = service.SetPrimaryHotelImage(context.Background(), 999999, 999999)
	if !errors.Is(err, ErrHotelNotFound) {
		t.Fatalf("SetPrimaryHotelImage() error = %v, want ErrHotelNotFound", err)
	}

	hotelID := insertHotelImageHotel(t, pool, "Delete Hotel")
	image := createHotelImageFixture(t, service, hotelID, "https://example.com/delete.jpg")
	if err := service.DeleteHotelImage(context.Background(), hotelID, image.ID); err != nil {
		t.Fatalf("first DeleteHotelImage() error = %v", err)
	}
	if err := service.DeleteHotelImage(context.Background(), hotelID, image.ID); !errors.Is(err, ErrImageNotFound) {
		t.Fatalf("second DeleteHotelImage() error = %v, want ErrImageNotFound", err)
	}
}

func TestConcurrentPrimaryImageChangesAreSerialized(t *testing.T) {
	pool, service := newHotelImageIntegrationService(t)
	hotelID := insertHotelImageHotel(t, pool, "Concurrent Hotel")
	first := createHotelImageFixture(t, service, hotelID, "https://example.com/first.jpg")
	second := createHotelImageFixture(t, service, hotelID, "https://example.com/second.jpg")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, imageID := range []int64{first.ID, second.ID} {
		imageID := imageID
		go func() {
			<-start
			_, err := service.SetPrimaryHotelImage(ctx, hotelID, imageID)
			results <- err
		}()
	}
	close(start)

	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent SetPrimaryHotelImage() error = %v", err)
		}
	}

	var primaryCount int64
	var primaryID int64
	if err := pool.QueryRow(
		context.Background(),
		`SELECT count(*), max(id) FILTER (WHERE is_primary)
		 FROM hotel_images
		 WHERE hotel_id = $1 AND is_primary = true`,
		hotelID,
	).Scan(&primaryCount, &primaryID); err != nil {
		t.Fatalf("read final primary image: %v", err)
	}
	if primaryCount != 1 {
		t.Fatalf("primary image count = %d, want 1", primaryCount)
	}
	if primaryID != first.ID && primaryID != second.ID {
		t.Errorf("primary image ID = %d, want %d or %d", primaryID, first.ID, second.ID)
	}
}

func newHotelImageIntegrationService(t *testing.T) (*pgxpool.Pool, *Service) {
	t.Helper()

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL hotel-image integration tests")
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

	schemaName := randomHotelImageSchemaName(t)
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

	applyHotelImageIntegrationMigrations(t, pool)
	queries := sqlc.New(pool)
	return pool, NewService(pool, queries)
}

func applyHotelImageIntegrationMigrations(t *testing.T, pool *pgxpool.Pool) {
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

func insertHotelImageHotel(t *testing.T, pool *pgxpool.Pool, name string) int64 {
	t.Helper()

	var hotelID int64
	err := pool.QueryRow(
		context.Background(),
		`INSERT INTO hotels (name, address, city)
		 VALUES ($1, '1 Image Street', 'Image City')
		 RETURNING id`,
		name,
	).Scan(&hotelID)
	if err != nil {
		t.Fatalf("insert hotel: %v", err)
	}
	return hotelID
}

func createHotelImageFixture(t *testing.T, service *Service, hotelID int64, imageURL string) sqlc.HotelImage {
	t.Helper()

	image, err := service.CreateHotelImage(context.Background(), hotelID, imageURL)
	if err != nil {
		t.Fatalf("CreateHotelImage() fixture error = %v", err)
	}
	return image
}

func randomHotelImageSchemaName(t *testing.T) string {
	t.Helper()

	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		t.Fatalf("generate random schema name: %v", err)
	}
	return fmt.Sprintf("ryoko_hotel_image_test_%s", hex.EncodeToString(randomBytes[:]))
}
