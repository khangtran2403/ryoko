package notification

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

func TestServicePostgresIntegration(t *testing.T) {
	pool := newNotificationIntegrationPool(t)
	queries := sqlc.New(pool)

	t.Run("delivers a pending email", func(t *testing.T) {
		emailID := insertNotificationFixture(t, pool, queries, "sent@example.com")
		sender := &integrationEmailSender{}
		service := newIntegrationNotificationService(queries, sender, 5)

		result, err := service.ProcessPendingEmails(context.Background())
		if err != nil {
			t.Fatalf("process pending emails: %v", err)
		}
		if result != (BatchResult{Claimed: 1, Sent: 1}) {
			t.Fatalf("result = %+v, want one claimed and sent", result)
		}

		message := getIntegrationOutboxMessage(t, queries, emailID)
		if message.Status != "sent" || message.Attempts != 1 {
			t.Errorf("message status/attempts = %q/%d, want sent/1", message.Status, message.Attempts)
		}
		if !message.SentAt.Valid || message.LockedAt.Valid || message.LastError.Valid {
			t.Errorf("unexpected sent state: %+v", message)
		}

		emails := sender.sentEmails()
		if len(emails) != 1 {
			t.Fatalf("sent email count = %d, want 1", len(emails))
		}
		if emails[0].To != "sent@example.com" ||
			emails[0].EventType != BookingConfirmedEvent ||
			emails[0].Payload.BookingID != message.BookingID {
			t.Errorf("sent email = %+v, want fixture recipient, event, and booking", emails[0])
		}
	})

	t.Run("schedules a retry and does not reclaim it early", func(t *testing.T) {
		emailID := insertNotificationFixture(t, pool, queries, "retry@example.com")
		sendErr := errors.New("SMTP temporarily unavailable")
		sender := &integrationEmailSender{err: sendErr}
		service := newIntegrationNotificationService(queries, sender, 5)

		startedAt := time.Now()
		result, err := service.ProcessPendingEmails(context.Background())
		if !errors.Is(err, sendErr) {
			t.Fatalf("process error = %v, want wrapped sender error", err)
		}
		if result != (BatchResult{Claimed: 1, Retried: 1}) {
			t.Fatalf("result = %+v, want one claimed and retried", result)
		}

		message := getIntegrationOutboxMessage(t, queries, emailID)
		if message.Status != "pending" || message.Attempts != 1 {
			t.Errorf("message status/attempts = %q/%d, want pending/1", message.Status, message.Attempts)
		}
		if message.LockedAt.Valid || !message.LastError.Valid || message.LastError.String == "" {
			t.Errorf("unexpected retry state: %+v", message)
		}
		if !message.AvailableAt.Valid || message.AvailableAt.Time.Before(startedAt.Add(59*time.Second)) {
			t.Errorf("available_at = %v, want about one minute in the future", message.AvailableAt)
		}

		secondResult, secondErr := service.ProcessPendingEmails(context.Background())
		if secondErr != nil {
			t.Fatalf("process before retry time: %v", secondErr)
		}
		if secondResult != (BatchResult{}) {
			t.Errorf("result before retry time = %+v, want empty", secondResult)
		}
		if sender.callCount() != 1 {
			t.Errorf("sender calls = %d, want 1", sender.callCount())
		}
	})

	t.Run("reclaims a stale processing message", func(t *testing.T) {
		emailID := insertNotificationFixture(t, pool, queries, "stale@example.com")
		claimed, err := queries.ClaimEmailOutboxMessages(
			context.Background(),
			sqlc.ClaimEmailOutboxMessagesParams{LeaseSeconds: 60, BatchSize: 1},
		)
		if err != nil || len(claimed) != 1 || claimed[0].ID != emailID {
			t.Fatalf("initial claim = %+v, error = %v", claimed, err)
		}
		if _, err := pool.Exec(
			context.Background(),
			`UPDATE email_outbox SET locked_at = now() - interval '2 seconds' WHERE id = $1`,
			emailID,
		); err != nil {
			t.Fatalf("age outbox lease: %v", err)
		}

		sender := &integrationEmailSender{}
		service := NewService(queries, sender, time.Second, 20, 5, time.Minute)
		result, err := service.ProcessPendingEmails(context.Background())
		if err != nil {
			t.Fatalf("process stale message: %v", err)
		}
		if result != (BatchResult{Claimed: 1, Sent: 1}) {
			t.Fatalf("result = %+v, want one reclaimed and sent", result)
		}

		message := getIntegrationOutboxMessage(t, queries, emailID)
		if message.Status != "sent" || message.Attempts != 2 {
			t.Errorf("message status/attempts = %q/%d, want sent/2", message.Status, message.Attempts)
		}
	})

	t.Run("concurrent processors deliver a message once", func(t *testing.T) {
		emailID := insertNotificationFixture(t, pool, queries, "concurrent@example.com")
		sender := &integrationEmailSender{}
		first := newIntegrationNotificationService(queries, sender, 5)
		second := newIntegrationNotificationService(queries, sender, 5)

		start := make(chan struct{})
		results := make(chan integrationProcessResult, 2)
		var ready sync.WaitGroup
		ready.Add(2)
		process := func(service *Service) {
			ready.Done()
			<-start
			result, err := service.ProcessPendingEmails(context.Background())
			results <- integrationProcessResult{result: result, err: err}
		}
		go process(first)
		go process(second)
		ready.Wait()
		close(start)

		var total BatchResult
		for range 2 {
			outcome := <-results
			if outcome.err != nil {
				t.Fatalf("concurrent processing: %v", outcome.err)
			}
			total.Claimed += outcome.result.Claimed
			total.Sent += outcome.result.Sent
			total.Retried += outcome.result.Retried
			total.Failed += outcome.result.Failed
		}
		if total != (BatchResult{Claimed: 1, Sent: 1}) {
			t.Errorf("combined result = %+v, want one claimed and sent", total)
		}
		if sender.callCount() != 1 {
			t.Errorf("sender calls = %d, want 1", sender.callCount())
		}
		if message := getIntegrationOutboxMessage(t, queries, emailID); message.Status != "sent" {
			t.Errorf("message status = %q, want sent", message.Status)
		}
	})

	t.Run("marks a message failed at the maximum attempt", func(t *testing.T) {
		emailID := insertNotificationFixture(t, pool, queries, "failed@example.com")
		if _, err := pool.Exec(
			context.Background(),
			`UPDATE email_outbox SET attempts = 2 WHERE id = $1`,
			emailID,
		); err != nil {
			t.Fatalf("set previous attempts: %v", err)
		}
		sendErr := errors.New("permanent SMTP failure")
		service := newIntegrationNotificationService(
			queries,
			&integrationEmailSender{err: sendErr},
			3,
		)

		result, err := service.ProcessPendingEmails(context.Background())
		if !errors.Is(err, sendErr) {
			t.Fatalf("process error = %v, want wrapped sender error", err)
		}
		if result != (BatchResult{Claimed: 1, Failed: 1}) {
			t.Fatalf("result = %+v, want one claimed and failed", result)
		}

		message := getIntegrationOutboxMessage(t, queries, emailID)
		if message.Status != "failed" || message.Attempts != 3 {
			t.Errorf("message status/attempts = %q/%d, want failed/3", message.Status, message.Attempts)
		}
		if message.LockedAt.Valid || !message.LastError.Valid {
			t.Errorf("unexpected failed state: %+v", message)
		}
	})

	t.Run("deletes sent messages older than the retention cutoff", func(t *testing.T) {
		emailID := insertNotificationFixture(t, pool, queries, "cleanup@example.com")
		service := newIntegrationNotificationService(queries, &integrationEmailSender{}, 5)
		if _, err := service.ProcessPendingEmails(context.Background()); err != nil {
			t.Fatalf("send cleanup fixture: %v", err)
		}
		if _, err := pool.Exec(
			context.Background(),
			`UPDATE email_outbox SET sent_at = now() - interval '48 hours' WHERE id = $1`,
			emailID,
		); err != nil {
			t.Fatalf("age sent message: %v", err)
		}

		deleted, err := queries.DeleteSentEmailOutboxBefore(
			context.Background(),
			pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true},
		)
		if err != nil {
			t.Fatalf("delete old sent messages: %v", err)
		}
		if deleted != 1 {
			t.Errorf("deleted rows = %d, want 1", deleted)
		}

		var count int
		if err := pool.QueryRow(
			context.Background(),
			`SELECT count(*) FROM email_outbox WHERE id = $1`,
			emailID,
		).Scan(&count); err != nil {
			t.Fatalf("count deleted message: %v", err)
		}
		if count != 0 {
			t.Errorf("remaining message count = %d, want 0", count)
		}
	})
}

type integrationProcessResult struct {
	result BatchResult
	err    error
}

type integrationEmailSender struct {
	mu     sync.Mutex
	emails []BookingEmail
	err    error
}

func (s *integrationEmailSender) SendBookingEmail(_ context.Context, email BookingEmail) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emails = append(s.emails, email)
	return s.err
}

func (s *integrationEmailSender) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.emails)
}

func (s *integrationEmailSender) sentEmails() []BookingEmail {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]BookingEmail(nil), s.emails...)
}

func newIntegrationNotificationService(
	queries *sqlc.Queries,
	sender EmailSender,
	maxAttempts int32,
) *Service {
	return NewService(queries, sender, time.Minute, 20, maxAttempts, time.Minute)
}

func insertNotificationFixture(
	t *testing.T,
	pool *pgxpool.Pool,
	queries *sqlc.Queries,
	email string,
) int64 {
	t.Helper()
	ctx := context.Background()

	var userID int64
	if err := pool.QueryRow(
		ctx,
		`INSERT INTO users (email, full_name) VALUES ($1, 'Outbox Guest') RETURNING id`,
		email,
	).Scan(&userID); err != nil {
		t.Fatalf("insert outbox user: %v", err)
	}

	var hotelID int64
	if err := pool.QueryRow(
		ctx,
		`INSERT INTO hotels (name, address, city)
		 VALUES ('Outbox Hotel', '1 Test Street', 'Test City')
		 RETURNING id`,
	).Scan(&hotelID); err != nil {
		t.Fatalf("insert outbox hotel: %v", err)
	}

	var roomTypeID int64
	if err := pool.QueryRow(
		ctx,
		`INSERT INTO room_types (
			hotel_id, name, price_per_night, capacity, total_rooms, currency
		 ) VALUES ($1, 'Deluxe', 1200000, 2, 5, 'VND')
		 RETURNING id`,
		hotelID,
	).Scan(&roomTypeID); err != nil {
		t.Fatalf("insert outbox room type: %v", err)
	}

	var bookingID int64
	if err := pool.QueryRow(
		ctx,
		`INSERT INTO bookings (
			user_id, room_type_id, check_in, check_out, rooms_count,
			guest_count, price_per_night, total_price, status, currency
		 ) VALUES ($1, $2, DATE '2030-01-10', DATE '2030-01-12', 1,
			2, 1200000, 2400000, 'confirmed', 'VND')
		 RETURNING id`,
		userID,
		roomTypeID,
	).Scan(&bookingID); err != nil {
		t.Fatalf("insert outbox booking: %v", err)
	}

	rows, err := queries.EnqueueBookingEmail(
		ctx,
		sqlc.EnqueueBookingEmailParams{
			EventType: BookingConfirmedEvent,
			BookingID: bookingID,
		},
	)
	if err != nil {
		t.Fatalf("enqueue booking email: %v", err)
	}
	if rows != 1 {
		t.Fatalf("enqueued rows = %d, want 1", rows)
	}

	var emailID int64
	if err := pool.QueryRow(
		ctx,
		`SELECT id FROM email_outbox WHERE booking_id = $1 AND event_type = $2`,
		bookingID,
		BookingConfirmedEvent,
	).Scan(&emailID); err != nil {
		t.Fatalf("read enqueued email ID: %v", err)
	}
	return emailID
}

func getIntegrationOutboxMessage(
	t *testing.T,
	queries *sqlc.Queries,
	emailID int64,
) sqlc.EmailOutbox {
	t.Helper()
	message, err := queries.GetEmailOutboxByID(context.Background(), emailID)
	if err != nil {
		t.Fatalf("get email outbox message %d: %v", emailID, err)
	}
	return message
}

func newNotificationIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL, explicitlyConfigured := os.LookupEnv("TEST_DATABASE_URL")
	if !explicitlyConfigured {
		databaseURL = "postgres://test:test@localhost:5433/testdb?sslmode=disable"
	}
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL notification integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	adminPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create notification integration pool: %v", err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		if !explicitlyConfigured {
			t.Skipf("local PostgreSQL test database is unavailable: %v", err)
		}
		t.Fatalf("ping notification integration database: %v", err)
	}

	schemaName := fmt.Sprintf("notification_test_%d", time.Now().UnixNano())
	schemaIdentifier := pgx.Identifier{schemaName}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schemaIdentifier); err != nil {
		adminPool.Close()
		t.Fatalf("create notification integration schema: %v", err)
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		adminPool.Close()
		t.Fatalf("parse notification integration database URL: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		adminPool.Close()
		t.Fatalf("create isolated notification integration pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := adminPool.Exec(
			cleanupCtx,
			"DROP SCHEMA "+schemaIdentifier+" CASCADE",
		); err != nil {
			t.Errorf("drop notification integration schema: %v", err)
		}
		adminPool.Close()
	})

	paths, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("find notification integration migrations: %v", err)
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
	return pool
}
