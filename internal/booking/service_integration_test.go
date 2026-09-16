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
		UserID:         userID,
		RoomTypeID:     roomTypeID,
		CheckIn:        time.Date(2030, time.January, 1, 14, 0, 0, 0, time.UTC),
		CheckOut:       time.Date(2030, time.January, 4, 9, 0, 0, 0, time.UTC),
		RoomsCount:     1,
		GuestCount:     2,
		IdempotencyKey: "create-booking-integration",
	}

	_, err := service.CreateBooking(context.Background(), CreateInput{
		UserID:         userID,
		RoomTypeID:     roomTypeID,
		CheckIn:        input.CheckIn,
		CheckOut:       input.CheckOut,
		RoomsCount:     1,
		GuestCount:     3,
		IdempotencyKey: input.IdempotencyKey,
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

func TestBookingKeepsPriceSnapshotAfterRoomTypePriceChanges(t *testing.T) {
	pool, service := newBookingIntegrationService(t)
	userID, roomTypeID := insertBookingFixtures(t, pool, 2)
	input := futureBookingInput(userID, roomTypeID, 1)
	input.IdempotencyKey = "price-snapshot"

	created, err := service.CreateBooking(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateBooking() error = %v", err)
	}

	if _, err := pool.Exec(
		context.Background(),
		"UPDATE room_types SET price_per_night = 250.00 WHERE id = $1",
		roomTypeID,
	); err != nil {
		t.Fatalf("update room type price: %v", err)
	}

	stored, err := service.GetBookingByUserID(context.Background(), created.ID, userID)
	if err != nil {
		t.Fatalf("GetBookingByUserID() error = %v", err)
	}
	pricePerNight, err := stored.PricePerNight.Value()
	if err != nil {
		t.Fatalf("format stored price per night: %v", err)
	}
	totalPrice, err := stored.TotalPrice.Value()
	if err != nil {
		t.Fatalf("format stored total price: %v", err)
	}
	if pricePerNight != "100.00" {
		t.Errorf("stored price per night = %v, want 100.00", pricePerNight)
	}
	if totalPrice != "300.00" {
		t.Errorf("stored total price = %v, want 300.00", totalPrice)
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

	for i := range 2 {
		go func(idempotencyKey string) {
			<-start
			request := input
			request.IdempotencyKey = idempotencyKey
			_, err := service.CreateBooking(ctx, request)
			results <- result{err: err}
		}(fmt.Sprintf("overbooking-request-%d", i))
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

func TestCreateBookingReplaysSameIdempotencyKey(t *testing.T) {
	pool, service := newBookingIntegrationService(t)
	userID, roomTypeID := insertBookingFixtures(t, pool, 2)
	input := futureBookingInput(userID, roomTypeID, 1)
	input.IdempotencyKey = "replay-booking"

	first, err := service.CreateBooking(context.Background(), input)
	if err != nil {
		t.Fatalf("first CreateBooking() error = %v", err)
	}
	second, err := service.CreateBooking(context.Background(), input)
	if err != nil {
		t.Fatalf("replayed CreateBooking() error = %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("replayed booking ID = %d, want %d", second.ID, first.ID)
	}

	assertBookingCounts(t, pool, 1, 3)
	assertAvailabilityRange(t, pool, roomTypeID, 3, 1, 1)
}

func TestCreateBookingRejectsReusedKeyWithDifferentPayload(t *testing.T) {
	pool, service := newBookingIntegrationService(t)
	userID, roomTypeID := insertBookingFixtures(t, pool, 2)
	input := futureBookingInput(userID, roomTypeID, 1)
	input.IdempotencyKey = "conflicting-booking"

	created, err := service.CreateBooking(context.Background(), input)
	if err != nil {
		t.Fatalf("first CreateBooking() error = %v", err)
	}
	conflicting := input
	conflicting.GuestCount = 2

	_, err = service.CreateBooking(context.Background(), conflicting)
	if !errors.Is(err, ErrIdempotencyKeyConflict) {
		t.Fatalf("conflicting CreateBooking() error = %v, want ErrIdempotencyKeyConflict", err)
	}

	assertBookingCounts(t, pool, 1, 3)
	assertBookingStatus(t, pool, created.ID, "confirmed")
	assertAvailabilityRange(t, pool, roomTypeID, 3, 1, 1)
}

func TestCreateBookingCoalescesConcurrentIdenticalRequests(t *testing.T) {
	pool, service := newBookingIntegrationService(t)
	userID, roomTypeID := insertBookingFixtures(t, pool, 2)
	input := futureBookingInput(userID, roomTypeID, 1)
	input.IdempotencyKey = "concurrent-replay"

	start := make(chan struct{})
	results := make(chan sqlc.Booking, 2)
	errorsChannel := make(chan error, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for range 2 {
		go func() {
			<-start
			created, err := service.CreateBooking(ctx, input)
			results <- created
			errorsChannel <- err
		}()
	}
	close(start)

	first := <-results
	second := <-results
	for range 2 {
		if err := <-errorsChannel; err != nil {
			t.Fatalf("concurrent CreateBooking() error = %v", err)
		}
	}
	if first.ID == 0 || second.ID != first.ID {
		t.Errorf("concurrent booking IDs = [%d, %d], want the same non-zero ID", first.ID, second.ID)
	}

	assertBookingCounts(t, pool, 1, 3)
	assertAvailabilityRange(t, pool, roomTypeID, 3, 1, 1)
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
	currentTime := time.Date(2030, time.January, 1, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time {
		return currentTime
	}
	created, err := service.CreateBooking(context.Background(), CreateInput{
		UserID:         userID,
		RoomTypeID:     roomTypeID,
		CheckIn:        time.Date(2030, time.January, 2, 0, 0, 0, 0, time.UTC),
		CheckOut:       time.Date(2030, time.January, 4, 0, 0, 0, 0, time.UTC),
		RoomsCount:     1,
		GuestCount:     1,
		IdempotencyKey: "started-stay",
	})
	if err != nil {
		t.Fatalf("CreateBooking() error = %v", err)
	}

	currentTime = time.Date(2030, time.January, 2, 12, 0, 0, 0, time.UTC)
	_, err = service.CancelBooking(context.Background(), created.ID, userID)
	if !errors.Is(err, ErrBookingNotCancellable) {
		t.Fatalf("CancelBooking() error = %v, want ErrBookingNotCancellable", err)
	}
	assertBookingStatus(t, pool, created.ID, "confirmed")
	assertAvailabilityRange(t, pool, roomTypeID, 2, 1, 1)
}

func TestListAvailableRoomTypesUsesBottleneckNightAndCapacity(t *testing.T) {
	pool, service := newBookingIntegrationService(t)
	service.now = func() time.Time {
		return time.Date(2029, time.January, 1, 12, 0, 0, 0, time.UTC)
	}
	_, standardID := insertBookingFixtures(t, pool, 3)

	var hotelID int64
	if err := pool.QueryRow(
		context.Background(),
		"SELECT hotel_id FROM room_types WHERE id = $1",
		standardID,
	).Scan(&hotelID); err != nil {
		t.Fatalf("read hotel ID: %v", err)
	}

	var suiteID int64
	if err := pool.QueryRow(
		context.Background(),
		`INSERT INTO room_types (
		     hotel_id, name, price_per_night, capacity, total_rooms
		 )
		 VALUES ($1, 'Suite', 200.00, 4, 2)
		 RETURNING id`,
		hotelID,
	).Scan(&suiteID); err != nil {
		t.Fatalf("insert suite room type: %v", err)
	}
	if _, err := pool.Exec(
		context.Background(),
		`INSERT INTO room_type_availability (room_type_id, date, rooms_booked)
		 VALUES
		     ($1, '2030-01-10', 1),
		     ($1, '2030-01-11', 2),
		     ($1, '2030-01-12', 1)`,
		standardID,
	); err != nil {
		t.Fatalf("insert nightly availability: %v", err)
	}

	available, err := service.ListAvailableRoomTypes(context.Background(), AvailabilityInput{
		HotelID:    hotelID,
		CheckIn:    time.Date(2030, time.January, 10, 15, 0, 0, 0, time.UTC),
		CheckOut:   time.Date(2030, time.January, 13, 9, 0, 0, 0, time.UTC),
		RoomsCount: 1,
		GuestCount: 2,
	})
	if err != nil {
		t.Fatalf("ListAvailableRoomTypes() error = %v", err)
	}
	if len(available) != 2 {
		t.Fatalf("available room types = %+v, want 2 rows", available)
	}
	if available[0].ID != standardID || available[0].RoomsAvailable != 1 {
		t.Errorf("standard availability = %+v, want bottleneck availability 1", available[0])
	}
	if available[1].ID != suiteID || available[1].RoomsAvailable != 2 {
		t.Errorf("suite availability = %+v, want availability 2", available[1])
	}

	capacityFiltered, err := service.ListAvailableRoomTypes(context.Background(), AvailabilityInput{
		HotelID:    hotelID,
		CheckIn:    time.Date(2030, time.January, 10, 0, 0, 0, 0, time.UTC),
		CheckOut:   time.Date(2030, time.January, 13, 0, 0, 0, 0, time.UTC),
		RoomsCount: 1,
		GuestCount: 3,
	})
	if err != nil {
		t.Fatalf("capacity-filtered ListAvailableRoomTypes() error = %v", err)
	}
	if len(capacityFiltered) != 1 || capacityFiltered[0].ID != suiteID {
		t.Errorf("capacity-filtered result = %+v, want suite only", capacityFiltered)
	}

	twoRooms, err := service.ListAvailableRoomTypes(context.Background(), AvailabilityInput{
		HotelID:    hotelID,
		CheckIn:    time.Date(2030, time.January, 10, 0, 0, 0, 0, time.UTC),
		CheckOut:   time.Date(2030, time.January, 13, 0, 0, 0, 0, time.UTC),
		RoomsCount: 2,
		GuestCount: 2,
	})
	if err != nil {
		t.Fatalf("two-room ListAvailableRoomTypes() error = %v", err)
	}
	if len(twoRooms) != 1 || twoRooms[0].ID != suiteID {
		t.Errorf("two-room result = %+v, want suite only", twoRooms)
	}
}

func TestAvailabilitySearchReflectsBookingAndCancellation(t *testing.T) {
	pool, service := newBookingIntegrationService(t)
	userID, roomTypeID := insertBookingFixtures(t, pool, 2)
	input := futureBookingInput(userID, roomTypeID, 1)

	var hotelID int64
	if err := pool.QueryRow(
		context.Background(),
		"SELECT hotel_id FROM room_types WHERE id = $1",
		roomTypeID,
	).Scan(&hotelID); err != nil {
		t.Fatalf("read hotel ID: %v", err)
	}
	search := func(rooms int32) []sqlc.ListAvailableRoomTypesRow {
		t.Helper()
		available, err := service.ListAvailableRoomTypes(context.Background(), AvailabilityInput{
			HotelID:    hotelID,
			CheckIn:    input.CheckIn,
			CheckOut:   input.CheckOut,
			RoomsCount: rooms,
			GuestCount: 1,
		})
		if err != nil {
			t.Fatalf("ListAvailableRoomTypes(%d) error = %v", rooms, err)
		}
		return available
	}

	before := search(2)
	if len(before) != 1 || before[0].RoomsAvailable != 2 {
		t.Fatalf("availability before booking = %+v, want 2 rooms", before)
	}
	created, err := service.CreateBooking(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateBooking() error = %v", err)
	}
	afterBooking := search(1)
	if len(afterBooking) != 1 || afterBooking[0].RoomsAvailable != 1 {
		t.Errorf("availability after booking = %+v, want 1 room", afterBooking)
	}
	if availableForTwo := search(2); len(availableForTwo) != 0 {
		t.Errorf("two-room search after booking = %+v, want empty", availableForTwo)
	}

	if _, err := service.CancelBooking(context.Background(), created.ID, userID); err != nil {
		t.Fatalf("CancelBooking() error = %v", err)
	}
	afterCancellation := search(2)
	if len(afterCancellation) != 1 || afterCancellation[0].RoomsAvailable != 2 {
		t.Errorf("availability after cancellation = %+v, want 2 rooms", afterCancellation)
	}
}

func TestListAvailableRoomTypesReturnsInitializedEmptySlice(t *testing.T) {
	_, service := newBookingIntegrationService(t)

	available, err := service.ListAvailableRoomTypes(context.Background(), AvailabilityInput{
		HotelID:    999999,
		CheckIn:    time.Date(2030, time.January, 10, 0, 0, 0, 0, time.UTC),
		CheckOut:   time.Date(2030, time.January, 11, 0, 0, 0, 0, time.UTC),
		RoomsCount: 1,
		GuestCount: 1,
	})
	if err != nil {
		t.Fatalf("ListAvailableRoomTypes() error = %v", err)
	}
	if available == nil {
		t.Fatal("ListAvailableRoomTypes() returned nil, want initialized empty slice")
	}
	if len(available) != 0 {
		t.Errorf("ListAvailableRoomTypes() = %+v, want empty", available)
	}
}

func TestListBookingsByUserPaginatesNewestFirst(t *testing.T) {
	pool, service := newBookingIntegrationService(t)
	userID, roomTypeID := insertBookingFixtures(t, pool, 3)

	insertBooking := func(createdAt time.Time) int64 {
		t.Helper()

		var bookingID int64
		if err := pool.QueryRow(
			context.Background(),
			"INSERT INTO bookings "+
				"(user_id, room_type_id, check_in, check_out, rooms_count, guest_count, "+
				"price_per_night, total_price, created_at) "+
				"VALUES ($1, $2, '2030-01-10', '2030-01-11', 1, 1, 100.00, 100.00, $3) "+
				"RETURNING id",
			userID,
			roomTypeID,
			createdAt,
		).Scan(&bookingID); err != nil {
			t.Fatalf("insert booking: %v", err)
		}
		return bookingID
	}

	oldestID := insertBooking(time.Date(2029, time.January, 1, 8, 0, 0, 0, time.UTC))
	middleID := insertBooking(time.Date(2029, time.January, 2, 8, 0, 0, 0, time.UTC))
	newestID := insertBooking(time.Date(2029, time.January, 3, 8, 0, 0, 0, time.UTC))

	var otherUserID int64
	if err := pool.QueryRow(
		context.Background(),
		"INSERT INTO users (email, full_name) "+
			"VALUES ('pagination-other@example.com', 'Pagination Other') RETURNING id",
	).Scan(&otherUserID); err != nil {
		t.Fatalf("insert other user: %v", err)
	}
	if _, err := pool.Exec(
		context.Background(),
		"INSERT INTO bookings "+
			"(user_id, room_type_id, check_in, check_out, rooms_count, guest_count, "+
			"price_per_night, total_price, created_at) "+
			"VALUES ($1, $2, '2030-01-10', '2030-01-11', 1, 1, 100.00, 100.00, $3)",
		otherUserID,
		roomTypeID,
		time.Date(2029, time.January, 4, 8, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatalf("insert other user's booking: %v", err)
	}

	firstPage, err := service.ListBookingsByUser(context.Background(), ListBookingsInput{
		UserID:   userID,
		Page:     1,
		PageSize: 2,
	})
	if err != nil {
		t.Fatalf("ListBookingsByUser() first page error = %v", err)
	}
	if len(firstPage.Bookings) != 2 ||
		firstPage.Bookings[0].ID != newestID ||
		firstPage.Bookings[1].ID != middleID {
		t.Errorf("first page = %+v, want booking IDs [%d, %d]", firstPage.Bookings, newestID, middleID)
	}
	if !firstPage.Pagination.HasMore {
		t.Error("first page has_more = false, want true")
	}

	secondPage, err := service.ListBookingsByUser(context.Background(), ListBookingsInput{
		UserID:   userID,
		Page:     2,
		PageSize: 2,
	})
	if err != nil {
		t.Fatalf("ListBookingsByUser() second page error = %v", err)
	}
	if len(secondPage.Bookings) != 1 || secondPage.Bookings[0].ID != oldestID {
		t.Errorf("second page = %+v, want booking ID [%d]", secondPage.Bookings, oldestID)
	}
	if secondPage.Pagination.HasMore {
		t.Error("second page has_more = true, want false")
	}
}

func TestSearchAvailableHotelsPaginatesAndSorts(t *testing.T) {
	pool, service := newBookingIntegrationService(t)
	service.now = func() time.Time {
		return time.Date(2029, time.January, 1, 12, 0, 0, 0, time.UTC)
	}

	insertHotel := func(name, city, price string) int64 {
		t.Helper()

		var hotelID int64
		if err := pool.QueryRow(
			context.Background(),
			`INSERT INTO hotels (name, address, city)
			 VALUES ($1, $2, $3)
			 RETURNING id`,
			name,
			name+" address",
			city,
		).Scan(&hotelID); err != nil {
			t.Fatalf("insert hotel %q: %v", name, err)
		}
		if _, err := pool.Exec(
			context.Background(),
			`INSERT INTO room_types (
			     hotel_id, name, price_per_night, capacity, total_rooms
			 )
			 VALUES ($1, 'Standard', $2, 2, 5)`,
			hotelID,
			price,
		); err != nil {
			t.Fatalf("insert room type for %q: %v", name, err)
		}
		return hotelID
	}

	cheapestID := insertHotel("Budget Stay", "Da Nang", "100.00")
	mostExpensiveID := insertHotel("Luxury Stay", "DA NANG", "300.00")
	middleID := insertHotel("Comfort Stay", "da nang", "200.00")
	insertHotel("Other City Stay", "Hue", "50.00")

	search := func(page, pageSize int32, sortOrder string) HotelSearchResult {
		t.Helper()

		result, err := service.SearchAvailableHotels(
			context.Background(),
			HotelSearchInput{
				City:       "  Da Nang  ",
				CheckIn:    time.Date(2030, time.January, 10, 0, 0, 0, 0, time.UTC),
				CheckOut:   time.Date(2030, time.January, 13, 0, 0, 0, 0, time.UTC),
				RoomsCount: 1,
				GuestCount: 2,
				Page:       page,
				PageSize:   pageSize,
				Sort:       sortOrder,
			},
		)
		if err != nil {
			t.Fatalf("SearchAvailableHotels() error = %v", err)
		}
		return result
	}

	firstPage := search(1, 2, HotelSearchSortPriceAsc)
	if len(firstPage.Hotels) != 2 ||
		firstPage.Hotels[0].ID != cheapestID ||
		firstPage.Hotels[1].ID != middleID {
		t.Errorf("ascending first page = %+v, want hotel IDs [%d, %d]", firstPage.Hotels, cheapestID, middleID)
	}
	if !firstPage.Pagination.HasMore {
		t.Error("ascending first page has_more = false, want true")
	}

	secondPage := search(2, 2, HotelSearchSortPriceAsc)
	if len(secondPage.Hotels) != 1 || secondPage.Hotels[0].ID != mostExpensiveID {
		t.Errorf("ascending second page = %+v, want hotel ID [%d]", secondPage.Hotels, mostExpensiveID)
	}
	if secondPage.Pagination.HasMore {
		t.Error("ascending second page has_more = true, want false")
	}

	descending := search(1, 2, HotelSearchSortPriceDesc)
	if len(descending.Hotels) != 2 ||
		descending.Hotels[0].ID != mostExpensiveID ||
		descending.Hotels[1].ID != middleID {
		t.Errorf("descending first page = %+v, want hotel IDs [%d, %d]", descending.Hotels, mostExpensiveID, middleID)
	}
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
		UserID:         userID,
		RoomTypeID:     roomTypeID,
		CheckIn:        checkIn,
		CheckOut:       checkIn.AddDate(0, 0, 3),
		RoomsCount:     roomsCount,
		GuestCount:     roomsCount,
		IdempotencyKey: "future-booking",
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
