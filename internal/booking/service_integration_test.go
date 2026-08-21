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

func TestCancelBookingRestoresAvailabilityExactlyOnce(t *testing.T) {
	pool, service := newBookingIntegrationService(t)
	userID, roomTypeID := insertBookingFixtures(t, pool, 2)
	input := futureBookingInput(userID, roomTypeID, 1)

	created, err := service.CreateBooking(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateBooking() error = %v", err)
	}
	assertAvailabilityRange(t, pool, roomTypeID, 3, 1, 1)

	cancelled, err := service.CancelBooking(context.Background(), created.ID, userID)
	if err != nil {
		t.Fatalf("CancelBooking() error = %v", err)
	}
	if cancelled.Status != "cancelled" {
		t.Errorf("cancelled status = %q, want cancelled", cancelled.Status)
	}
	assertAvailabilityRange(t, pool, roomTypeID, 3, 0, 0)

	_, err = service.CancelBooking(context.Background(), created.ID, userID)
	if !errors.Is(err, ErrBookingNotCancellable) {
		t.Fatalf("second CancelBooking() error = %v, want ErrBookingNotCancellable", err)
	}
	assertAvailabilityRange(t, pool, roomTypeID, 3, 0, 0)
}

func TestCancelBookingPreventsConcurrentDoubleCancellation(t *testing.T) {
	pool, service := newBookingIntegrationService(t)
	userID, roomTypeID := insertBookingFixtures(t, pool, 2)
	created, err := service.CreateBooking(
		context.Background(),
		futureBookingInput(userID, roomTypeID, 1),
	)
	if err != nil {
		t.Fatalf("CreateBooking() error = %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for range 2 {
		go func() {
			<-start
			_, err := service.CancelBooking(ctx, created.ID, userID)
			results <- err
		}()
	}
	close(start)

	var successes int
	var notCancellable int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrBookingNotCancellable):
			notCancellable++
		default:
			t.Fatalf("unexpected concurrent cancellation error: %v", err)
		}
	}
	if successes != 1 || notCancellable != 1 {
		t.Fatalf(
			"concurrent results: successes=%d notCancellable=%d, want 1 and 1",
			successes,
			notCancellable,
		)
	}
	assertAvailabilityRange(t, pool, roomTypeID, 3, 0, 0)
}

func TestCancelBookingDoesNotExposeAnotherUsersBooking(t *testing.T) {
	pool, service := newBookingIntegrationService(t)
	ownerID, roomTypeID := insertBookingFixtures(t, pool, 2)
	created, err := service.CreateBooking(
		context.Background(),
		futureBookingInput(ownerID, roomTypeID, 1),
	)
	if err != nil {
		t.Fatalf("CreateBooking() error = %v", err)
	}

	var otherUserID int64
	err = pool.QueryRow(
		context.Background(),
		`INSERT INTO users (email, full_name)
		 VALUES ('other-booking-test@example.com', 'Other Booking Test')
		 RETURNING id`,
	).Scan(&otherUserID)
	if err != nil {
		t.Fatalf("insert other user: %v", err)
	}

	_, err = service.CancelBooking(context.Background(), created.ID, otherUserID)
	if !errors.Is(err, ErrBookingNotFound) {
		t.Fatalf("CancelBooking() error = %v, want ErrBookingNotFound", err)
	}
	assertBookingStatus(t, pool, created.ID, "confirmed")
	assertAvailabilityRange(t, pool, roomTypeID, 3, 1, 1)
}

func TestCancelBookingRollsBackPartialAvailabilityUpdate(t *testing.T) {
	pool, service := newBookingIntegrationService(t)
	userID, roomTypeID := insertBookingFixtures(t, pool, 2)
	created, err := service.CreateBooking(
		context.Background(),
		futureBookingInput(userID, roomTypeID, 1),
	)
	if err != nil {
		t.Fatalf("CreateBooking() error = %v", err)
	}

	_, err = pool.Exec(
		context.Background(),
		`UPDATE room_type_availability
		 SET rooms_booked = 0
		 WHERE room_type_id = $1
		   AND date = (
		       SELECT min(date)
		       FROM room_type_availability
		       WHERE room_type_id = $1
		   )`,
		roomTypeID,
	)
	if err != nil {
		t.Fatalf("corrupt one availability row: %v", err)
	}

	_, err = service.CancelBooking(context.Background(), created.ID, userID)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("CancelBooking() error = %v, want ErrUnavailable", err)
	}
	assertBookingStatus(t, pool, created.ID, "confirmed")

	var zeroRows int64
	var oneRows int64
	err = pool.QueryRow(
		context.Background(),
		`SELECT
		     count(*) FILTER (WHERE rooms_booked = 0),
		     count(*) FILTER (WHERE rooms_booked = 1)
		 FROM room_type_availability
		 WHERE room_type_id = $1`,
		roomTypeID,
	).Scan(&zeroRows, &oneRows)
	if err != nil {
		t.Fatalf("query availability after rollback: %v", err)
	}
	if zeroRows != 1 || oneRows != 2 {
		t.Errorf("availability rows: zero=%d one=%d, want zero=1 one=2", zeroRows, oneRows)
	}
}

func TestCancelBookingRejectsStartedStay(t *testing.T) {
	pool, service := newBookingIntegrationService(t)
	userID, roomTypeID := insertBookingFixtures(t, pool, 2)
	today := utcDate(time.Now())
	created, err := service.CreateBooking(context.Background(), CreateInput{
		UserID:     userID,
		RoomTypeID: roomTypeID,
		CheckIn:    today.AddDate(0, 0, -1),
		CheckOut:   today.AddDate(0, 0, 1),
		RoomsCount: 1,
		GuestCount: 1,
	})
	if err != nil {
		t.Fatalf("CreateBooking() error = %v", err)
	}

	_, err = service.CancelBooking(context.Background(), created.ID, userID)
	if !errors.Is(err, ErrBookingNotCancellable) {
		t.Fatalf("CancelBooking() error = %v, want ErrBookingNotCancellable", err)
	}
	assertBookingStatus(t, pool, created.ID, "confirmed")
	assertAvailabilityRange(t, pool, roomTypeID, 2, 1, 1)
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

func futureBookingInput(userID, roomTypeID int64, roomsCount int32) CreateInput {
	checkIn := utcDate(time.Now()).AddDate(1, 0, 0)
	return CreateInput{
		UserID:     userID,
		RoomTypeID: roomTypeID,
		CheckIn:    checkIn,
		CheckOut:   checkIn.AddDate(0, 0, 3),
		RoomsCount: roomsCount,
		GuestCount: roomsCount,
	}
}

func utcDate(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func assertAvailabilityRange(
	t *testing.T,
	pool *pgxpool.Pool,
	roomTypeID int64,
	wantRows int64,
	wantMinimum int32,
	wantMaximum int32,
) {
	t.Helper()

	var rows int64
	var minimum int32
	var maximum int32
	err := pool.QueryRow(
		context.Background(),
		`SELECT count(*), min(rooms_booked), max(rooms_booked)
		 FROM room_type_availability
		 WHERE room_type_id = $1`,
		roomTypeID,
	).Scan(&rows, &minimum, &maximum)
	if err != nil {
		t.Fatalf("query availability range: %v", err)
	}
	if rows != wantRows || minimum != wantMinimum || maximum != wantMaximum {
		t.Errorf(
			"availability = {rows:%d min:%d max:%d}, want {rows:%d min:%d max:%d}",
			rows,
			minimum,
			maximum,
			wantRows,
			wantMinimum,
			wantMaximum,
		)
	}
}

func assertBookingStatus(t *testing.T, pool *pgxpool.Pool, bookingID int64, want string) {
	t.Helper()

	var status string
	if err := pool.QueryRow(
		context.Background(),
		"SELECT status FROM bookings WHERE id = $1",
		bookingID,
	).Scan(&status); err != nil {
		t.Fatalf("query booking status: %v", err)
	}
	if status != want {
		t.Errorf("booking status = %q, want %q", status, want)
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
