package inventory

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
	"github.com/khangtran2403/ryoko/internal/booking"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

func TestSetBlockedInventoryPersistsAndCanBeCleared(t *testing.T) {
	pool, service := newInventoryIntegrationService(t)
	_, roomTypeID := insertInventoryFixtures(t, pool, 5)
	from := inventoryTomorrow()
	to := from.AddDate(0, 0, 3)

	rows, err := service.SetBlockedInventory(context.Background(), SetBlockedInventoryInput{
		RoomTypeID:   roomTypeID,
		DateFrom:     from,
		DateTo:       to,
		RoomsBlocked: 2,
		BlockReason:  "  Bathroom renovation  ",
	})
	if err != nil {
		t.Fatalf("SetBlockedInventory() error = %v", err)
	}
	assertInventoryRows(t, rows, 3, 5, 0, 2, 3, "Bathroom renovation")

	listed, err := service.ListRoomTypeInventory(context.Background(), ListInventoryInput{
		RoomTypeID: roomTypeID,
		DateFrom:   from,
		DateTo:     to.AddDate(0, 0, 1),
	})
	if err != nil {
		t.Fatalf("ListRoomTypeInventory() error = %v", err)
	}
	if len(listed) != 4 {
		t.Fatalf("listed row count = %d, want 4", len(listed))
	}
	assertInventoryRows(t, listed[:3], 3, 5, 0, 2, 3, "Bathroom renovation")
	if listed[3].RoomsBooked != 0 || listed[3].RoomsBlocked != 0 || listed[3].RoomsAvailable != 5 || listed[3].BlockReason.Valid {
		t.Errorf("uninitialized calendar day = %+v, want zero usage and five available", listed[3])
	}

	cleared, err := service.SetBlockedInventory(context.Background(), SetBlockedInventoryInput{
		RoomTypeID:   roomTypeID,
		DateFrom:     from,
		DateTo:       to,
		RoomsBlocked: 0,
		BlockReason:  "this value must be ignored",
	})
	if err != nil {
		t.Fatalf("clear SetBlockedInventory() error = %v", err)
	}
	assertInventoryRows(t, cleared, 3, 5, 0, 0, 5, "")
}

func TestSetBlockedInventoryRejectsExistingBookingConflictWithoutPartialUpdate(t *testing.T) {
	pool, service := newInventoryIntegrationService(t)
	_, roomTypeID := insertInventoryFixtures(t, pool, 5)
	from := inventoryTomorrow()
	to := from.AddDate(0, 0, 3)

	if _, err := pool.Exec(
		context.Background(),
		`INSERT INTO room_type_availability (room_type_id, date, rooms_booked)
		 VALUES ($1, $2, 4)`,
		roomTypeID,
		from.AddDate(0, 0, 1),
	); err != nil {
		t.Fatalf("insert booked inventory: %v", err)
	}

	_, err := service.SetBlockedInventory(context.Background(), SetBlockedInventoryInput{
		RoomTypeID:   roomTypeID,
		DateFrom:     from,
		DateTo:       to,
		RoomsBlocked: 2,
		BlockReason:  "Maintenance",
	})
	if !errors.Is(err, ErrInventoryConflict) {
		t.Fatalf("SetBlockedInventory() error = %v, want ErrInventoryConflict", err)
	}

	var rowCount int64
	var blockedTotal int64
	if err := pool.QueryRow(
		context.Background(),
		`SELECT count(*), COALESCE(sum(rooms_blocked), 0)
		 FROM room_type_availability
		 WHERE room_type_id = $1`,
		roomTypeID,
	).Scan(&rowCount, &blockedTotal); err != nil {
		t.Fatalf("query inventory after conflict: %v", err)
	}
	if rowCount != 1 || blockedTotal != 0 {
		t.Errorf("inventory after rollback = {rows:%d blocked:%d}, want {rows:1 blocked:0}", rowCount, blockedTotal)
	}
}

func TestBookingCreationHonorsBlockedInventory(t *testing.T) {
	pool, inventoryService := newInventoryIntegrationService(t)
	userID, roomTypeID := insertInventoryFixtures(t, pool, 5)
	bookingService := booking.NewService(pool, sqlc.New(pool))
	from := inventoryTomorrow()
	to := from.AddDate(0, 0, 3)

	if _, err := inventoryService.SetBlockedInventory(context.Background(), SetBlockedInventoryInput{
		RoomTypeID:   roomTypeID,
		DateFrom:     from,
		DateTo:       to,
		RoomsBlocked: 2,
		BlockReason:  "Maintenance",
	}); err != nil {
		t.Fatalf("SetBlockedInventory() error = %v", err)
	}

	_, err := bookingService.CreateBooking(context.Background(), booking.CreateInput{
		UserID:         userID,
		RoomTypeID:     roomTypeID,
		CheckIn:        from,
		CheckOut:       to,
		RoomsCount:     4,
		GuestCount:     4,
		IdempotencyKey: "blocked-inventory-rejected",
	})
	if !errors.Is(err, booking.ErrUnavailable) {
		t.Fatalf("CreateBooking() error = %v, want ErrUnavailable", err)
	}

	created, err := bookingService.CreateBooking(context.Background(), booking.CreateInput{
		UserID:         userID,
		RoomTypeID:     roomTypeID,
		CheckIn:        from,
		CheckOut:       to,
		RoomsCount:     3,
		GuestCount:     3,
		IdempotencyKey: "blocked-inventory-accepted",
	})
	if err != nil {
		t.Fatalf("CreateBooking() within remaining inventory error = %v", err)
	}
	if created.RoomsCount != 3 {
		t.Errorf("created rooms = %d, want 3", created.RoomsCount)
	}
}

func TestListRoomTypeInventoryAllowsHistoricalRange(t *testing.T) {
	pool, service := newInventoryIntegrationService(t)
	_, roomTypeID := insertInventoryFixtures(t, pool, 5)
	from := time.Date(2020, time.January, 10, 0, 0, 0, 0, time.UTC)

	rows, err := service.ListRoomTypeInventory(context.Background(), ListInventoryInput{
		RoomTypeID: roomTypeID,
		DateFrom:   from,
		DateTo:     from.AddDate(0, 0, 2),
	})
	if err != nil {
		t.Fatalf("ListRoomTypeInventory() historical error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("historical row count = %d, want 2", len(rows))
	}
}

func assertInventoryRows(
	t *testing.T,
	rows []sqlc.ListRoomTypeInventoryRow,
	wantLength int,
	wantTotal int32,
	wantBooked int32,
	wantBlocked int32,
	wantAvailable int32,
	wantReason string,
) {
	t.Helper()
	if len(rows) != wantLength {
		t.Fatalf("inventory row count = %d, want %d", len(rows), wantLength)
	}
	for _, row := range rows {
		if row.TotalRooms != wantTotal || row.RoomsBooked != wantBooked || row.RoomsBlocked != wantBlocked || row.RoomsAvailable != wantAvailable {
			t.Errorf("inventory row = %+v, want total=%d booked=%d blocked=%d available=%d", row, wantTotal, wantBooked, wantBlocked, wantAvailable)
		}
		if wantReason == "" {
			if row.BlockReason.Valid {
				t.Errorf("block reason = %q, want NULL", row.BlockReason.String)
			}
		} else if !row.BlockReason.Valid || row.BlockReason.String != wantReason {
			t.Errorf("block reason = %+v, want %q", row.BlockReason, wantReason)
		}
	}
}

func newInventoryIntegrationService(t *testing.T) (*pgxpool.Pool, *Service) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL inventory integration tests")
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

	schemaName := inventoryRandomSchemaName(t)
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

	applyInventoryMigrations(t, pool)
	queries := sqlc.New(pool)
	return pool, NewService(pool, queries)
}

func applyInventoryMigrations(t *testing.T, pool *pgxpool.Pool) {
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

func insertInventoryFixtures(t *testing.T, pool *pgxpool.Pool, totalRooms int32) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	var userID int64
	if err := pool.QueryRow(
		ctx,
		`INSERT INTO users (email, full_name)
		 VALUES ('inventory-test@example.com', 'Inventory Test')
		 RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	var hotelID int64
	if err := pool.QueryRow(
		ctx,
		`INSERT INTO hotels (name, address, city)
		 VALUES ('Inventory Hotel', '1 Test Street', 'Test City')
		 RETURNING id`,
	).Scan(&hotelID); err != nil {
		t.Fatalf("insert hotel: %v", err)
	}

	var roomTypeID int64
	if err := pool.QueryRow(
		ctx,
		`INSERT INTO room_types (hotel_id, name, price_per_night, capacity, total_rooms)
		 VALUES ($1, 'Standard', 100.00, 2, $2)
		 RETURNING id`,
		hotelID,
		totalRooms,
	).Scan(&roomTypeID); err != nil {
		t.Fatalf("insert room type: %v", err)
	}
	return userID, roomTypeID
}

func inventoryTomorrow() time.Time {
	today := time.Now().UTC()
	return time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
}

func inventoryRandomSchemaName(t *testing.T) string {
	t.Helper()
	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		t.Fatalf("generate random schema name: %v", err)
	}
	return fmt.Sprintf("ryoko_inventory_test_%s", hex.EncodeToString(randomBytes[:]))
}
