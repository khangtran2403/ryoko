package admin_booking

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

func TestListBookingsForAdminFiltersAndPaginates(t *testing.T) {
	pool, service := newAdminBookingIntegrationService(t)
	fixtures := insertAdminBookingFixtures(t, pool)

	firstPage, err := service.ListBookingsForAdmin(context.Background(), ListBookingsForAdminInput{
		Page:     1,
		PageSize: 2,
	})
	if err != nil {
		t.Fatalf("first page error = %v", err)
	}
	assertAdminBookingIDs(t, firstPage.Bookings, fixtures.newestBookingID, fixtures.middleBookingID)
	if !firstPage.Pagination.HasMore {
		t.Error("first page has_more = false, want true")
	}

	secondPage, err := service.ListBookingsForAdmin(context.Background(), ListBookingsForAdminInput{
		Page:     2,
		PageSize: 2,
	})
	if err != nil {
		t.Fatalf("second page error = %v", err)
	}
	assertAdminBookingIDs(t, secondPage.Bookings, fixtures.oldestBookingID)
	if secondPage.Pagination.HasMore {
		t.Error("second page has_more = true, want false")
	}

	tests := []struct {
		name    string
		input   ListBookingsForAdminInput
		wantIDs []int64
	}{
		{
			name: "hotel",
			input: ListBookingsForAdminInput{
				HotelID:  &fixtures.firstHotelID,
				Page:     1,
				PageSize: 10,
			},
			wantIDs: []int64{fixtures.middleBookingID, fixtures.oldestBookingID},
		},
		{
			name: "status",
			input: ListBookingsForAdminInput{
				Status:   stringPointer("confirmed"),
				Page:     1,
				PageSize: 10,
			},
			wantIDs: []int64{fixtures.oldestBookingID},
		},
		{
			name: "check-in from only",
			input: ListBookingsForAdminInput{
				CheckInFrom: timePointer(time.Date(2030, time.January, 15, 0, 0, 0, 0, time.UTC)),
				Page:        1,
				PageSize:    10,
			},
			wantIDs: []int64{fixtures.newestBookingID, fixtures.middleBookingID},
		},
		{
			name: "check-in to only",
			input: ListBookingsForAdminInput{
				CheckInTo: timePointer(time.Date(2030, time.January, 15, 0, 0, 0, 0, time.UTC)),
				Page:      1,
				PageSize:  10,
			},
			wantIDs: []int64{fixtures.middleBookingID, fixtures.oldestBookingID},
		},
		{
			name: "combined filters",
			input: ListBookingsForAdminInput{
				HotelID:     &fixtures.firstHotelID,
				Status:      stringPointer("cancelled"),
				CheckInFrom: timePointer(time.Date(2030, time.January, 15, 0, 0, 0, 0, time.UTC)),
				CheckInTo:   timePointer(time.Date(2030, time.January, 15, 0, 0, 0, 0, time.UTC)),
				Page:        1,
				PageSize:    10,
			},
			wantIDs: []int64{fixtures.middleBookingID},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := service.ListBookingsForAdmin(context.Background(), tt.input)
			if err != nil {
				t.Fatalf("ListBookingsForAdmin() error = %v", err)
			}
			assertAdminBookingIDs(t, result.Bookings, tt.wantIDs...)
			if result.Pagination.HasMore {
				t.Error("has_more = true, want false")
			}
		})
	}
}

type adminBookingFixtures struct {
	firstHotelID    int64
	oldestBookingID int64
	middleBookingID int64
	newestBookingID int64
}

func insertAdminBookingFixtures(t *testing.T, pool *pgxpool.Pool) adminBookingFixtures {
	t.Helper()
	ctx := context.Background()

	insertUser := func(email, name string) int64 {
		t.Helper()
		var id int64
		if err := pool.QueryRow(
			ctx,
			"INSERT INTO users (email, full_name) VALUES ($1, $2) RETURNING id",
			email,
			name,
		).Scan(&id); err != nil {
			t.Fatalf("insert user: %v", err)
		}
		return id
	}
	insertHotelAndRoomType := func(name string) (int64, int64) {
		t.Helper()
		var hotelID int64
		if err := pool.QueryRow(
			ctx,
			"INSERT INTO hotels (name, address, city) VALUES ($1, $2, 'Test City') RETURNING id",
			name,
			name+" Address",
		).Scan(&hotelID); err != nil {
			t.Fatalf("insert hotel: %v", err)
		}
		var roomTypeID int64
		if err := pool.QueryRow(
			ctx,
			"INSERT INTO room_types (hotel_id, name, price_per_night, capacity, total_rooms) "+
				"VALUES ($1, 'Standard', 100.00, 2, 5) RETURNING id",
			hotelID,
		).Scan(&roomTypeID); err != nil {
			t.Fatalf("insert room type: %v", err)
		}
		return hotelID, roomTypeID
	}
	insertBooking := func(userID, roomTypeID int64, checkIn, status string, createdAt time.Time) int64 {
		t.Helper()
		checkInDate, err := time.Parse(time.DateOnly, checkIn)
		if err != nil {
			t.Fatalf("parse fixture check-in: %v", err)
		}
		var id int64
		if err := pool.QueryRow(
			ctx,
			"INSERT INTO bookings "+
				"(user_id, room_type_id, check_in, check_out, rooms_count, guest_count, "+
				"price_per_night, total_price, status, created_at) "+
				"VALUES ($1, $2, $3, $4, 1, 2, 100.00, 300.00, $5, $6) RETURNING id",
			userID,
			roomTypeID,
			checkInDate,
			checkInDate.AddDate(0, 0, 3),
			status,
			createdAt,
		).Scan(&id); err != nil {
			t.Fatalf("insert booking: %v", err)
		}
		return id
	}

	firstUserID := insertUser("admin-list-one@example.com", "Customer One")
	secondUserID := insertUser("admin-list-two@example.com", "Customer Two")
	firstHotelID, firstRoomTypeID := insertHotelAndRoomType("First Hotel")
	_, secondRoomTypeID := insertHotelAndRoomType("Second Hotel")

	oldestID := insertBooking(
		firstUserID,
		firstRoomTypeID,
		"2030-01-10",
		"confirmed",
		time.Date(2029, time.December, 1, 8, 0, 0, 0, time.UTC),
	)
	middleID := insertBooking(
		secondUserID,
		firstRoomTypeID,
		"2030-01-15",
		"cancelled",
		time.Date(2029, time.December, 2, 8, 0, 0, 0, time.UTC),
	)
	newestID := insertBooking(
		firstUserID,
		secondRoomTypeID,
		"2030-01-20",
		"completed",
		time.Date(2029, time.December, 3, 8, 0, 0, 0, time.UTC),
	)

	return adminBookingFixtures{
		firstHotelID:    firstHotelID,
		oldestBookingID: oldestID,
		middleBookingID: middleID,
		newestBookingID: newestID,
	}
}

func assertAdminBookingIDs(t *testing.T, rows []sqlc.ListBookingsForAdminRow, want ...int64) {
	t.Helper()
	if len(rows) != len(want) {
		t.Fatalf("booking count = %d, want %d; rows = %+v", len(rows), len(want), rows)
	}
	for i, id := range want {
		if rows[i].BookingID != id {
			t.Errorf("booking[%d].ID = %d, want %d", i, rows[i].BookingID, id)
		}
	}
}

func stringPointer(value string) *string {
	return &value
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func newAdminBookingIntegrationService(t *testing.T) (*pgxpool.Pool, *Service) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL admin booking integration tests")
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

	schemaName := randomAdminBookingSchemaName(t)
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

	applyAdminBookingMigrations(t, pool)
	return pool, NewService(sqlc.New(pool))
}

func applyAdminBookingMigrations(t *testing.T, pool *pgxpool.Pool) {
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

func randomAdminBookingSchemaName(t *testing.T) string {
	t.Helper()
	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		t.Fatalf("generate random schema name: %v", err)
	}
	return fmt.Sprintf("ryoko_admin_booking_test_%s", hex.EncodeToString(randomBytes[:]))
}
