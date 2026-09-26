package roomtype

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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

func TestUpdateRoomTypePersistsRequestedFields(t *testing.T) {
	pool, service := newRoomTypeIntegrationService(t)
	roomTypeID := insertRoomTypeFixture(t, pool, 10)
	price := numericFromString(t, "1750000.00")

	updated, err := service.UpdateRoomType(context.Background(), UpdateRoomTypeRequest{
		ID:            roomTypeID,
		Name:          "Deluxe King",
		Description:   pgtype.Text{String: "River view", Valid: true},
		PricePerNight: price,
		Capacity:      3,
		TotalRooms:    12,
	})
	if err != nil {
		t.Fatalf("UpdateRoomType() error = %v", err)
	}
	if updated.Name != "Deluxe King" || updated.Description.String != "River view" || updated.Capacity != 3 || updated.TotalRooms != 12 {
		t.Errorf("updated room type = %+v", updated)
	}
	assertNumericString(t, updated.PricePerNight, "1750000.00")

	stored, err := sqlc.New(pool).GetRoomTypeByID(context.Background(), roomTypeID)
	if err != nil {
		t.Fatalf("GetRoomTypeByID() error = %v", err)
	}
	if stored.Name != updated.Name || stored.Capacity != updated.Capacity || stored.TotalRooms != updated.TotalRooms {
		t.Errorf("stored room type = %+v, want values from update", stored)
	}
}

func TestUpdateRoomTypeRejectsInventoryConflictWithoutChangingRoomType(t *testing.T) {
	pool, service := newRoomTypeIntegrationService(t)
	roomTypeID := insertRoomTypeFixture(t, pool, 10)
	if _, err := pool.Exec(
		context.Background(),
		`INSERT INTO room_type_availability
			(room_type_id, date, rooms_booked, rooms_blocked, block_reason)
		 VALUES ($1, CURRENT_DATE + 1, 3, 2, 'Maintenance')`,
		roomTypeID,
	); err != nil {
		t.Fatalf("insert inventory fixture: %v", err)
	}

	_, err := service.UpdateRoomType(context.Background(), UpdateRoomTypeRequest{
		ID:            roomTypeID,
		Name:          "Must Not Persist",
		Description:   pgtype.Text{String: "Must not persist", Valid: true},
		PricePerNight: numericFromString(t, "2000000.00"),
		Capacity:      4,
		TotalRooms:    4,
	})
	if !errors.Is(err, ErrInventoryConflict) {
		t.Fatalf("UpdateRoomType() error = %v, want ErrInventoryConflict", err)
	}

	stored, err := sqlc.New(pool).GetRoomTypeByID(context.Background(), roomTypeID)
	if err != nil {
		t.Fatalf("GetRoomTypeByID() error = %v", err)
	}
	if stored.Name != "Standard" || stored.Capacity != 2 || stored.TotalRooms != 10 {
		t.Errorf("room type changed after conflict: %+v", stored)
	}
}

func TestUpdateRoomTypeAllowsTotalEqualToMaximumUsage(t *testing.T) {
	pool, service := newRoomTypeIntegrationService(t)
	roomTypeID := insertRoomTypeFixture(t, pool, 10)
	if _, err := pool.Exec(
		context.Background(),
		`INSERT INTO room_type_availability
			(room_type_id, date, rooms_booked, rooms_blocked, block_reason)
		 VALUES ($1, CURRENT_DATE + 1, 3, 2, 'Maintenance')`,
		roomTypeID,
	); err != nil {
		t.Fatalf("insert inventory fixture: %v", err)
	}

	updated, err := service.UpdateRoomType(context.Background(), UpdateRoomTypeRequest{
		ID:            roomTypeID,
		Name:          "Standard Updated",
		PricePerNight: numericFromString(t, "1200000.00"),
		Capacity:      2,
		TotalRooms:    5,
	})
	if err != nil {
		t.Fatalf("UpdateRoomType() error = %v", err)
	}
	if updated.TotalRooms != 5 {
		t.Errorf("total rooms = %d, want 5", updated.TotalRooms)
	}
}

func TestUpdateRoomTypeReturnsNotFound(t *testing.T) {
	_, service := newRoomTypeIntegrationService(t)

	_, err := service.UpdateRoomType(context.Background(), UpdateRoomTypeRequest{
		ID:            999999,
		Name:          "Missing",
		PricePerNight: numericFromString(t, "1000000.00"),
		Capacity:      2,
		TotalRooms:    5,
	})
	if !errors.Is(err, ErrRoomTypeNotFound) {
		t.Fatalf("UpdateRoomType() error = %v, want ErrRoomTypeNotFound", err)
	}
}

func newRoomTypeIntegrationService(t *testing.T) (*pgxpool.Pool, *Service) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL room-type integration tests")
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

	schemaName := randomRoomTypeSchemaName(t)
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

	applyRoomTypeMigrations(t, pool)
	return pool, NewService(pool, sqlc.New(pool))
}

func applyRoomTypeMigrations(t *testing.T, pool *pgxpool.Pool) {
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

func insertRoomTypeFixture(t *testing.T, pool *pgxpool.Pool, totalRooms int32) int64 {
	t.Helper()
	var hotelID int64
	if err := pool.QueryRow(
		context.Background(),
		`INSERT INTO hotels (name, address, city)
		 VALUES ('Room Type Hotel', '1 Test Street', 'Test City')
		 RETURNING id`,
	).Scan(&hotelID); err != nil {
		t.Fatalf("insert hotel fixture: %v", err)
	}

	var roomTypeID int64
	if err := pool.QueryRow(
		context.Background(),
		`INSERT INTO room_types
			(hotel_id, name, description, price_per_night, capacity, total_rooms)
		 VALUES ($1, 'Standard', 'Original description', 1000000.00, 2, $2)
		 RETURNING id`,
		hotelID,
		totalRooms,
	).Scan(&roomTypeID); err != nil {
		t.Fatalf("insert room type fixture: %v", err)
	}
	return roomTypeID
}

func numericFromString(t *testing.T, value string) pgtype.Numeric {
	t.Helper()
	var numeric pgtype.Numeric
	if err := numeric.Scan(value); err != nil {
		t.Fatalf("parse numeric %q: %v", value, err)
	}
	return numeric
}

func assertNumericString(t *testing.T, got pgtype.Numeric, want string) {
	t.Helper()
	value, err := got.Value()
	if err != nil {
		t.Fatalf("read numeric value: %v", err)
	}
	if fmt.Sprint(value) != want {
		t.Errorf("numeric value = %v, want %s", value, want)
	}
}

func randomRoomTypeSchemaName(t *testing.T) string {
	t.Helper()
	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		t.Fatalf("generate random schema name: %v", err)
	}
	return fmt.Sprintf("ryoko_room_type_test_%s", hex.EncodeToString(randomBytes[:]))
}
