package review

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

type reviewFixtures struct {
	ownerID           int64
	otherUserID       int64
	hotelID           int64
	completedBooking  int64
	completedBooking2 int64
	confirmedBooking  int64
}

func TestReviewServiceIntegration(t *testing.T) {
	pool, service := newReviewIntegrationService(t)
	fixtures := insertReviewFixtures(t, pool)
	comment := "  Wonderful service  "

	created, err := service.CreateReview(context.Background(), CreateReview{
		Rating:    5,
		Comment:   &comment,
		BookingID: fixtures.completedBooking,
		UserID:    fixtures.ownerID,
	})
	if err != nil {
		t.Fatalf("CreateReview() error = %v", err)
	}
	if created.BookingID != fixtures.completedBooking || created.Rating != 5 {
		t.Errorf("created review = %+v", created)
	}
	if !created.Comment.Valid || created.Comment.String != "Wonderful service" {
		t.Errorf("created comment = %#v, want trimmed text", created.Comment)
	}

	got, err := service.GetReviewByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetReviewByID() error = %v", err)
	}
	if got.ID != created.ID || got.HotelID != fixtures.hotelID {
		t.Errorf("GetReviewByID() = %+v", got)
	}
	if got.ReviewerName != "Review Owner" || got.RoomTypeName != "Standard" {
		t.Errorf(
			"joined fields = {reviewer:%q room:%q}",
			got.ReviewerName,
			got.RoomTypeName,
		)
	}

	listed, err := service.ListReviewByHotel(context.Background(), fixtures.hotelID)
	if err != nil {
		t.Fatalf("ListReviewByHotel() error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Errorf("ListReviewByHotel() = %+v, want created review", listed)
	}
}

func TestCreateReviewRequiresOwnedCompletedBooking(t *testing.T) {
	pool, service := newReviewIntegrationService(t)
	fixtures := insertReviewFixtures(t, pool)

	tests := []struct {
		name      string
		bookingID int64
		userID    int64
	}{
		{
			name:      "confirmed booking",
			bookingID: fixtures.confirmedBooking,
			userID:    fixtures.ownerID,
		},
		{
			name:      "another users booking",
			bookingID: fixtures.completedBooking,
			userID:    fixtures.otherUserID,
		},
		{
			name:      "missing booking",
			bookingID: 999999,
			userID:    fixtures.ownerID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.CreateReview(context.Background(), CreateReview{
				Rating:    4,
				BookingID: tt.bookingID,
				UserID:    tt.userID,
			})
			if !errors.Is(err, ErrBookingNotReviewable) {
				t.Fatalf("CreateReview() error = %v, want ErrBookingNotReviewable", err)
			}
		})
	}

	var reviewCount int64
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM reviews").Scan(&reviewCount); err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if reviewCount != 0 {
		t.Errorf("review count = %d, want 0", reviewCount)
	}
}

func TestCreateReviewAllowsOnlyOneReviewPerBooking(t *testing.T) {
	pool, service := newReviewIntegrationService(t)
	fixtures := insertReviewFixtures(t, pool)

	_, err := service.CreateReview(context.Background(), CreateReview{
		Rating:    5,
		BookingID: fixtures.completedBooking,
		UserID:    fixtures.ownerID,
	})
	if err != nil {
		t.Fatalf("first CreateReview() error = %v", err)
	}

	_, err = service.CreateReview(context.Background(), CreateReview{
		Rating:    3,
		BookingID: fixtures.completedBooking,
		UserID:    fixtures.ownerID,
	})
	if !errors.Is(err, ErrReviewAlreadyExists) {
		t.Fatalf("second CreateReview() error = %v, want ErrReviewAlreadyExists", err)
	}
}

func TestCreateReviewStoresOmittedCommentAsNull(t *testing.T) {
	pool, service := newReviewIntegrationService(t)
	fixtures := insertReviewFixtures(t, pool)

	created, err := service.CreateReview(context.Background(), CreateReview{
		Rating:    4,
		Comment:   nil,
		BookingID: fixtures.completedBooking2,
		UserID:    fixtures.ownerID,
	})
	if err != nil {
		t.Fatalf("CreateReview() error = %v", err)
	}
	if created.Comment.Valid {
		t.Errorf("created comment = %#v, want SQL NULL", created.Comment)
	}
}

func TestReviewServiceReadNotFoundAndEmptyList(t *testing.T) {
	_, service := newReviewIntegrationService(t)

	_, err := service.GetReviewByID(context.Background(), 999999)
	if !errors.Is(err, ErrReviewNotFound) {
		t.Fatalf("GetReviewByID() error = %v, want ErrReviewNotFound", err)
	}

	list, err := service.ListReviewByHotel(context.Background(), 999999)
	if err != nil {
		t.Fatalf("ListReviewByHotel() error = %v", err)
	}
	if list == nil {
		t.Fatal("ListReviewByHotel() returned nil, want initialized empty slice")
	}
	if len(list) != 0 {
		t.Errorf("ListReviewByHotel() length = %d, want 0", len(list))
	}
}

func newReviewIntegrationService(t *testing.T) (*pgxpool.Pool, *Service) {
	t.Helper()

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL review integration tests")
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

	schemaName := randomReviewSchemaName(t)
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

	applyReviewIntegrationMigrations(t, pool)
	queries := sqlc.New(pool)
	return pool, NewService(queries)
}

func applyReviewIntegrationMigrations(t *testing.T, pool *pgxpool.Pool) {
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

func insertReviewFixtures(t *testing.T, pool *pgxpool.Pool) reviewFixtures {
	t.Helper()

	ctx := context.Background()
	var fixtures reviewFixtures
	err := pool.QueryRow(
		ctx,
		`INSERT INTO users (email, full_name)
		 VALUES ('review-owner@example.com', 'Review Owner')
		 RETURNING id`,
	).Scan(&fixtures.ownerID)
	if err != nil {
		t.Fatalf("insert review owner: %v", err)
	}
	err = pool.QueryRow(
		ctx,
		`INSERT INTO users (email, full_name)
		 VALUES ('other-reviewer@example.com', 'Other Reviewer')
		 RETURNING id`,
	).Scan(&fixtures.otherUserID)
	if err != nil {
		t.Fatalf("insert other reviewer: %v", err)
	}
	err = pool.QueryRow(
		ctx,
		`INSERT INTO hotels (name, address, city)
		 VALUES ('Review Hotel', '1 Review Street', 'Review City')
		 RETURNING id`,
	).Scan(&fixtures.hotelID)
	if err != nil {
		t.Fatalf("insert hotel: %v", err)
	}

	var roomTypeID int64
	err = pool.QueryRow(
		ctx,
		`INSERT INTO room_types (
		     hotel_id, name, price_per_night, capacity, total_rooms
		 )
		 VALUES ($1, 'Standard', 100.00, 2, 3)
		 RETURNING id`,
		fixtures.hotelID,
	).Scan(&roomTypeID)
	if err != nil {
		t.Fatalf("insert room type: %v", err)
	}

	fixtures.completedBooking = insertReviewBooking(t, pool, fixtures.ownerID, roomTypeID, "completed", 1)
	fixtures.completedBooking2 = insertReviewBooking(t, pool, fixtures.ownerID, roomTypeID, "completed", 2)
	fixtures.confirmedBooking = insertReviewBooking(t, pool, fixtures.ownerID, roomTypeID, "confirmed", 3)
	return fixtures
}

func insertReviewBooking(
	t *testing.T,
	pool *pgxpool.Pool,
	userID int64,
	roomTypeID int64,
	status string,
	dayOffset int,
) int64 {
	t.Helper()

	checkIn := time.Date(2030, time.January, dayOffset, 0, 0, 0, 0, time.UTC)
	checkOut := checkIn.AddDate(0, 0, 2)
	var bookingID int64
	err := pool.QueryRow(
		context.Background(),
		`INSERT INTO bookings (
		     user_id,
		     room_type_id,
		     check_in,
		     check_out,
		     rooms_count,
		     guest_count,
		     price_per_night,
		     total_price,
		     status
		 )
		 VALUES ($1, $2, $3, $4, 1, 1, 100.00, 200.00, $5)
		 RETURNING id`,
		userID,
		roomTypeID,
		checkIn,
		checkOut,
		status,
	).Scan(&bookingID)
	if err != nil {
		t.Fatalf("insert %s booking: %v", status, err)
	}
	return bookingID
}

func randomReviewSchemaName(t *testing.T) string {
	t.Helper()

	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		t.Fatalf("generate random schema name: %v", err)
	}
	return fmt.Sprintf("ryoko_review_test_%s", hex.EncodeToString(randomBytes[:]))
}
