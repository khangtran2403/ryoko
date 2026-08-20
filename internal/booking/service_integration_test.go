package booking

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

func TestCreateBookingIntegration(t *testing.T) {
	pool, service := newBookingIntegrationService(t)
	userID, roomTypeID := insertBookingFixtures(t, pool, 1)

	input := CreateInput{
		UserID:     userID,
		RoomTypeID: roomTypeID,
		CheckIn:    time.Date(2030, time.January, 1, 14, 0, 0, 0, time.UTC),
		CheckOut:   time.Date(2030, time.January, 4, 9, 0, 0, 0, time.UTC),
		RoomsCount: 1,
		GuestCount: 2,
	}

	_, err := service.CreateBooking(context.Background(), CreateInput{
		UserID:     userID,
		RoomTypeID: roomTypeID,
		CheckIn:    input.CheckIn,
		CheckOut:   input.CheckOut,
		RoomsCount: 1,
		GuestCount: 3,
	})
	if !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("capacity check error = %v, want ErrCapacityExceeded", err)
	}
	assertBookingCounts(t, pool, 0, 0)

	created, err := service.CreateBooking(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateBooking() error = %v", err)
	}
	if created.UserID != userID {
		t.Errorf("created UserID = %d, want %d", created.UserID, userID)
	}
	if created.RoomTypeID != roomTypeID {
		t.Errorf("created RoomTypeID = %d, want %d", created.RoomTypeID, roomTypeID)
	}
	if created.Status != "confirmed" {
		t.Errorf("created Status = %q, want confirmed", created.Status)
	}

	var totalPrice string
	err = pool.QueryRow(
		context.Background(),
		"SELECT total_price::text FROM bookings WHERE id = $1",
		created.ID,
	).Scan(&totalPrice)
	if err != nil {
		t.Fatalf("query total price: %v", err)
	}
	if totalPrice != "300.00" {
		t.Errorf("total price = %q, want 300.00", totalPrice)
	}

	var availabilityRows int64
	var minimumBooked int32
	var maximumBooked int32
	err = pool.QueryRow(
		context.Background(),
		`SELECT count(*), min(rooms_booked), max(rooms_booked)
		 FROM room_type_availability
		 WHERE room_type_id = $1`,
		roomTypeID,
	).Scan(&availabilityRows, &minimumBooked, &maximumBooked)
	if err != nil {
		t.Fatalf("query availability: %v", err)
	}
	if availabilityRows != 3 {
		t.Errorf("availability row count = %d, want 3", availabilityRows)
	}
	if minimumBooked != 1 || maximumBooked != 1 {
		t.Errorf(
			"rooms_booked range = %d..%d, want 1..1",
			minimumBooked,
			maximumBooked,
		)
	}
}

func TestCreateBookingPreventsConcurrentOverbooking(t *testing.T) {
	pool, service := newBookingIntegrationService(t)
	userID, roomTypeID := insertBookingFixtures(t, pool, 1)

	input := CreateInput{
		UserID:     userID,
		RoomTypeID: roomTypeID,
		CheckIn:    time.Date(2030, time.February, 1, 0, 0, 0, 0, time.UTC),
		CheckOut:   time.Date(2030, time.February, 4, 0, 0, 0, 0, time.UTC),
		RoomsCount: 1,
		GuestCount: 1,
	}

	type result struct {
		err error
	}

	start := make(chan struct{})
	results := make(chan result, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for range 2 {
		go func() {
			<-start
			_, err := service.CreateBooking(ctx, input)
			results <- result{err: err}
		}()
	}
	close(start)

	var successes int
	var unavailable int
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			successes++
		case errors.Is(result.err, ErrUnavailable):
			unavailable++
		default:
			t.Fatalf("unexpected concurrent booking error: %v", result.err)
		}
	}

	if successes != 1 || unavailable != 1 {
		t.Fatalf(
			"concurrent results: successes=%d unavailable=%d, want 1 and 1",
			successes,
			unavailable,
		)
	}
	assertBookingCounts(t, pool, 1, 3)
}

func newBookingIntegrationService(t *testing.T) (*pgxpool.Pool, *Service) {
	t.Helper()

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL booking integration tests")
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

	schemaName := randomSchemaName(t)
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

	applyIntegrationMigrations(t, pool)
	queries := sqlc.New(pool)
	return pool, NewService(pool, queries)
}

func applyIntegrationMigrations(t *testing.T, pool *pgxpool.Pool) {
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

func insertBookingFixtures(
	t *testing.T,
	pool *pgxpool.Pool,
	totalRooms int32,
) (int64, int64) {
	t.Helper()

	ctx := context.Background()
	var userID int64
	err := pool.QueryRow(
		ctx,
		`INSERT INTO users (email, full_name)
		 VALUES ('booking-test@example.com', 'Booking Test')
		 RETURNING id`,
	).Scan(&userID)
	if err != nil {
		t.Fatalf("insert test user: %v", err)
	}

	var hotelID int64
	err = pool.QueryRow(
		ctx,
		`INSERT INTO hotels (name, address, city)
		 VALUES ('Test Hotel', '1 Test Street', 'Test City')
		 RETURNING id`,
	).Scan(&hotelID)
	if err != nil {
		t.Fatalf("insert test hotel: %v", err)
	}

	var roomTypeID int64
	err = pool.QueryRow(
		ctx,
		`INSERT INTO room_types (
		     hotel_id, name, price_per_night, capacity, total_rooms
		 )
		 VALUES ($1, 'Standard', 100.00, 2, $2)
		 RETURNING id`,
		hotelID,
		totalRooms,
	).Scan(&roomTypeID)
	if err != nil {
		t.Fatalf("insert test room type: %v", err)
	}

	return userID, roomTypeID
}

func assertBookingCounts(
	t *testing.T,
	pool *pgxpool.Pool,
	wantBookings int64,
	wantAvailabilityRows int64,
) {
	t.Helper()

	var bookings int64
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM bookings",
	).Scan(&bookings); err != nil {
		t.Fatalf("count bookings: %v", err)
	}
	if bookings != wantBookings {
		t.Errorf("booking count = %d, want %d", bookings, wantBookings)
	}

	var availabilityRows int64
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM room_type_availability",
	).Scan(&availabilityRows); err != nil {
		t.Fatalf("count availability rows: %v", err)
	}
	if availabilityRows != wantAvailabilityRows {
		t.Errorf(
			"availability row count = %d, want %d",
			availabilityRows,
			wantAvailabilityRows,
		)
	}
}

func randomSchemaName(t *testing.T) string {
	t.Helper()

	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		t.Fatalf("generate random schema name: %v", err)
	}
	return fmt.Sprintf("ryoko_test_%s", hex.EncodeToString(randomBytes[:]))
}
