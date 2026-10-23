package notification

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

type recordingOutboxQueries struct {
	messages    []sqlc.EmailOutbox
	claimErr    error
	claimParams sqlc.ClaimEmailOutboxMessagesParams

	sentIDs     []int64
	sentRows    map[int64]int64
	sentErrors  map[int64]error
	retryCalls  []sqlc.ScheduleEmailOutboxRetryParams
	retryRows   map[int64]int64
	retryErrors map[int64]error
	failedCalls []sqlc.MarkEmailOutboxFailedParams
	failedRows  map[int64]int64
	failedErrs  map[int64]error
}

func (q *recordingOutboxQueries) ClaimEmailOutboxMessages(
	_ context.Context,
	params sqlc.ClaimEmailOutboxMessagesParams,
) ([]sqlc.EmailOutbox, error) {
	q.claimParams = params
	return q.messages, q.claimErr
}

func (q *recordingOutboxQueries) MarkEmailOutboxSent(
	_ context.Context,
	emailID int64,
) (int64, error) {
	q.sentIDs = append(q.sentIDs, emailID)
	return configuredRows(q.sentRows, emailID), q.sentErrors[emailID]
}

func (q *recordingOutboxQueries) ScheduleEmailOutboxRetry(
	_ context.Context,
	params sqlc.ScheduleEmailOutboxRetryParams,
) (int64, error) {
	q.retryCalls = append(q.retryCalls, params)
	return configuredRows(q.retryRows, params.EmailID), q.retryErrors[params.EmailID]
}

func (q *recordingOutboxQueries) MarkEmailOutboxFailed(
	_ context.Context,
	params sqlc.MarkEmailOutboxFailedParams,
) (int64, error) {
	q.failedCalls = append(q.failedCalls, params)
	return configuredRows(q.failedRows, params.EmailID), q.failedErrs[params.EmailID]
}

func configuredRows(configured map[int64]int64, id int64) int64 {
	if rows, ok := configured[id]; ok {
		return rows
	}
	return 1
}

type recordingEmailSender struct {
	calls  []BookingEmail
	errors map[string]error
}

func (s *recordingEmailSender) SendBookingEmail(
	_ context.Context,
	email BookingEmail,
) error {
	s.calls = append(s.calls, email)
	return s.errors[email.To]
}

func TestServiceProcessesMixedBatch(t *testing.T) {
	providerErr := errors.New("SMTP server unavailable")
	queries := &recordingOutboxQueries{
		messages: []sqlc.EmailOutbox{
			validOutboxMessage(t, 1, "sent@example.com", 1),
			validOutboxMessage(t, 2, "retry@example.com", 1),
			validOutboxMessage(t, 3, "failed@example.com", 3),
			{
				ID:             4,
				BookingID:      104,
				EventType:      BookingConfirmedEvent,
				RecipientEmail: "malformed@example.com",
				Payload:        []byte(`{"booking_id":`),
				Attempts:       1,
			},
		},
	}
	sender := &recordingEmailSender{errors: map[string]error{
		"retry@example.com":  providerErr,
		"failed@example.com": providerErr,
	}}
	service := NewService(queries, sender, 2*time.Minute, 10, 3, 30*time.Second)

	result, err := service.ProcessPendingEmails(context.Background())
	if !errors.Is(err, providerErr) || !errors.Is(err, ErrInvalidEmailMessage) {
		t.Fatalf("ProcessPendingEmails() error = %v", err)
	}
	wantResult := BatchResult{Claimed: 4, Sent: 1, Retried: 1, Failed: 2}
	if result != wantResult {
		t.Fatalf("ProcessPendingEmails() result = %+v, want %+v", result, wantResult)
	}
	if queries.claimParams.LeaseSeconds != 120 || queries.claimParams.BatchSize != 10 {
		t.Errorf("claim params = %+v", queries.claimParams)
	}
	if len(sender.calls) != 3 {
		t.Fatalf("sender calls = %d, want 3", len(sender.calls))
	}
	if sender.calls[0].Payload.BookingID != 101 ||
		sender.calls[0].Payload.HotelName != "Test Hotel" ||
		sender.calls[0].EventType != BookingConfirmedEvent {
		t.Errorf("first sender call = %+v", sender.calls[0])
	}
	if len(queries.sentIDs) != 1 || queries.sentIDs[0] != 1 {
		t.Errorf("sent IDs = %v, want [1]", queries.sentIDs)
	}
	if len(queries.retryCalls) != 1 {
		t.Fatalf("retry calls = %+v, want one", queries.retryCalls)
	}
	retry := queries.retryCalls[0]
	if retry.EmailID != 2 || retry.RetryAfterSeconds != 30 ||
		!retry.LastError.Valid || !strings.Contains(retry.LastError.String, providerErr.Error()) {
		t.Errorf("retry call = %+v", retry)
	}
	if len(queries.failedCalls) != 2 ||
		queries.failedCalls[0].EmailID != 3 ||
		queries.failedCalls[1].EmailID != 4 {
		t.Errorf("failed calls = %+v, want IDs 3 and 4", queries.failedCalls)
	}
}

func TestServiceContinuesAfterConcurrentStateChange(t *testing.T) {
	queries := &recordingOutboxQueries{
		messages: []sqlc.EmailOutbox{
			validOutboxMessage(t, 1, "first@example.com", 1),
			validOutboxMessage(t, 2, "second@example.com", 1),
		},
		sentRows: map[int64]int64{1: 0},
	}
	sender := &recordingEmailSender{}
	service := NewService(queries, sender, time.Minute, 10, 3, time.Minute)

	result, err := service.ProcessPendingEmails(context.Background())
	if !errors.Is(err, ErrOutboxStateChanged) {
		t.Fatalf("ProcessPendingEmails() error = %v, want ErrOutboxStateChanged", err)
	}
	if result != (BatchResult{Claimed: 2, Sent: 1}) {
		t.Errorf("result = %+v, want one sent after state conflict", result)
	}
	if len(sender.calls) != 2 || len(queries.sentIDs) != 2 {
		t.Errorf("sender calls = %d sent updates = %v", len(sender.calls), queries.sentIDs)
	}
}

func TestServiceReturnsClaimError(t *testing.T) {
	claimErr := errors.New("database unavailable")
	service := NewService(
		&recordingOutboxQueries{claimErr: claimErr},
		&recordingEmailSender{},
		time.Minute,
		10,
		3,
		time.Minute,
	)

	result, err := service.ProcessPendingEmails(context.Background())
	if !errors.Is(err, claimErr) {
		t.Fatalf("ProcessPendingEmails() error = %v, want claim error", err)
	}
	if result != (BatchResult{}) {
		t.Errorf("result = %+v, want zero result", result)
	}
}

func TestServiceRetryDelayIsExponentialAndCapped(t *testing.T) {
	service := NewService(
		&recordingOutboxQueries{},
		&recordingEmailSender{},
		time.Minute,
		10,
		20,
		time.Hour,
	)

	tests := []struct {
		attempts int32
		want     time.Duration
	}{
		{attempts: 0, want: time.Hour},
		{attempts: 1, want: time.Hour},
		{attempts: 2, want: 2 * time.Hour},
		{attempts: 5, want: 16 * time.Hour},
		{attempts: 6, want: 24 * time.Hour},
		{attempts: 100, want: 24 * time.Hour},
	}
	for _, test := range tests {
		if got := service.retryDelay(test.attempts); got != test.want {
			t.Errorf("retryDelay(%d) = %v, want %v", test.attempts, got, test.want)
		}
	}
}

func TestServiceTruncatesStoredErrorsWithoutBreakingUTF8(t *testing.T) {
	longError := errors.New(strings.Repeat("界", maxStoredErrorRunes+100))
	queries := &recordingOutboxQueries{
		messages: []sqlc.EmailOutbox{validOutboxMessage(t, 1, "failed@example.com", 1)},
	}
	sender := &recordingEmailSender{errors: map[string]error{"failed@example.com": longError}}
	service := NewService(queries, sender, time.Minute, 10, 1, time.Minute)

	result, err := service.ProcessPendingEmails(context.Background())
	if !errors.Is(err, longError) {
		t.Fatalf("ProcessPendingEmails() error = %v, want delivery error", err)
	}
	if result != (BatchResult{Claimed: 1, Failed: 1}) {
		t.Errorf("result = %+v", result)
	}
	if len(queries.failedCalls) != 1 {
		t.Fatalf("failed calls = %d, want 1", len(queries.failedCalls))
	}
	stored := queries.failedCalls[0].LastError.String
	if !utf8.ValidString(stored) || len([]rune(stored)) != maxStoredErrorRunes {
		t.Errorf("stored error has %d runes; valid UTF-8 = %t", len([]rune(stored)), utf8.ValidString(stored))
	}
}

func TestNewServiceRejectsInvalidConfiguration(t *testing.T) {
	queries := &recordingOutboxQueries{}
	sender := &recordingEmailSender{}
	tests := []struct {
		name  string
		build func()
	}{
		{name: "nil queries", build: func() { NewService(nil, sender, time.Minute, 10, 3, time.Minute) }},
		{name: "nil sender", build: func() { NewService(queries, nil, time.Minute, 10, 3, time.Minute) }},
		{name: "short lease", build: func() { NewService(queries, sender, time.Millisecond, 10, 3, time.Minute) }},
		{name: "zero batch", build: func() { NewService(queries, sender, time.Minute, 0, 3, time.Minute) }},
		{name: "large batch", build: func() { NewService(queries, sender, time.Minute, 1001, 3, time.Minute) }},
		{name: "zero attempts", build: func() { NewService(queries, sender, time.Minute, 10, 0, time.Minute) }},
		{name: "many attempts", build: func() { NewService(queries, sender, time.Minute, 10, 101, time.Minute) }},
		{name: "short retry", build: func() { NewService(queries, sender, time.Minute, 10, 3, time.Millisecond) }},
		{name: "long retry", build: func() { NewService(queries, sender, time.Minute, 10, 3, 25*time.Hour) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("NewService() did not panic")
				}
			}()
			test.build()
		})
	}
}

func validOutboxMessage(
	t *testing.T,
	id int64,
	recipient string,
	attempts int32,
) sqlc.EmailOutbox {
	t.Helper()
	payload, err := json.Marshal(BookingEmailPayload{
		BookingID:     id + 100,
		GuestName:     "Test Guest",
		HotelName:     "Test Hotel",
		RoomTypeName:  "Deluxe Room",
		CheckIn:       "2026-11-01",
		CheckOut:      "2026-11-03",
		RoomsCount:    1,
		GuestCount:    2,
		PricePerNight: "100.00",
		TotalPrice:    "200.00",
		Currency:      "VND",
	})
	if err != nil {
		t.Fatalf("marshal test payload: %v", err)
	}
	return sqlc.EmailOutbox{
		ID:             id,
		BookingID:      id + 100,
		EventType:      BookingConfirmedEvent,
		RecipientEmail: recipient,
		Payload:        payload,
		Status:         "processing",
		Attempts:       attempts,
	}
}
