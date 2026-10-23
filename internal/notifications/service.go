package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

const (
	maxOutboxBatchSize  = 1000
	maxOutboxAttempts   = 100
	maxStoredErrorRunes = 2000
	maxOutboxRetryDelay = 24 * time.Hour
	maxRetryExponent    = 10
)

var (
	ErrInvalidEmailMessage = errors.New("invalid email outbox message")
	ErrOutboxStateChanged  = errors.New("email outbox state changed concurrently")
)

type outboxQueries interface {
	ClaimEmailOutboxMessages(context.Context, sqlc.ClaimEmailOutboxMessagesParams) ([]sqlc.EmailOutbox, error)
	MarkEmailOutboxSent(context.Context, int64) (int64, error)
	ScheduleEmailOutboxRetry(context.Context, sqlc.ScheduleEmailOutboxRetryParams) (int64, error)
	MarkEmailOutboxFailed(context.Context, sqlc.MarkEmailOutboxFailedParams) (int64, error)
}

type BatchResult struct {
	Claimed int
	Sent    int
	Retried int
	Failed  int
}

type Service struct {
	queries        outboxQueries
	sender         EmailSender
	leaseDuration  time.Duration
	batchSize      int32
	maxAttempts    int32
	baseRetryDelay time.Duration
}

func NewService(
	queries outboxQueries,
	sender EmailSender,
	leaseDuration time.Duration,
	batchSize int32,
	maxAttempts int32,
	baseRetryDelay time.Duration,
) *Service {
	if queries == nil {
		panic("notification outbox queries must not be nil")
	}
	if sender == nil {
		panic("notification email sender must not be nil")
	}
	if leaseDuration < time.Second || leaseDuration/time.Second > math.MaxInt32 {
		panic("notification lease duration must be from one second to 2147483647 seconds")
	}
	if batchSize < 1 || batchSize > maxOutboxBatchSize {
		panic("notification batch size must be between 1 and 1000")
	}
	if maxAttempts < 1 || maxAttempts > maxOutboxAttempts {
		panic("notification maximum attempts must be between 1 and 100")
	}
	if baseRetryDelay < time.Second || baseRetryDelay > maxOutboxRetryDelay {
		panic("notification base retry delay must be between one second and 24 hours")
	}

	return &Service{
		queries:        queries,
		sender:         sender,
		leaseDuration:  leaseDuration,
		batchSize:      batchSize,
		maxAttempts:    maxAttempts,
		baseRetryDelay: baseRetryDelay,
	}
}

func (s *Service) ProcessPendingEmails(ctx context.Context) (BatchResult, error) {
	messages, err := s.queries.ClaimEmailOutboxMessages(
		ctx,
		sqlc.ClaimEmailOutboxMessagesParams{
			LeaseSeconds: int32(s.leaseDuration / time.Second),
			BatchSize:    s.batchSize,
		},
	)
	if err != nil {
		return BatchResult{}, fmt.Errorf("claim email outbox messages: %w", err)
	}

	result := BatchResult{Claimed: len(messages)}
	var processingErrors []error
	for _, message := range messages {
		outcome, err := s.processMessage(ctx, message)
		switch outcome {
		case messageSent:
			result.Sent++
		case messageRetried:
			result.Retried++
		case messageFailed:
			result.Failed++
		}
		if err != nil {
			processingErrors = append(
				processingErrors,
				fmt.Errorf("process email outbox message %d: %w", message.ID, err),
			)
		}
	}

	return result, errors.Join(processingErrors...)
}

type messageOutcome uint8

const (
	messageUnchanged messageOutcome = iota
	messageSent
	messageRetried
	messageFailed
)

func (s *Service) processMessage(ctx context.Context, message sqlc.EmailOutbox) (messageOutcome, error) {
	email, err := bookingEmailFromOutbox(message)
	if err != nil {
		return s.failMessage(ctx, message.ID, err)
	}

	if err := s.sender.SendBookingEmail(ctx, email); err != nil {
		deliveryErr := fmt.Errorf("send booking email: %w", err)
		if message.Attempts >= s.maxAttempts {
			return s.failMessage(ctx, message.ID, deliveryErr)
		}
		return s.retryMessage(ctx, message, deliveryErr)
	}

	rows, err := s.queries.MarkEmailOutboxSent(ctx, message.ID)
	if err != nil {
		return messageUnchanged, fmt.Errorf("mark email sent: %w", err)
	}
	if rows != 1 {
		return messageUnchanged, outboxStateError(message.ID, rows)
	}
	return messageSent, nil
}

func (s *Service) retryMessage(ctx context.Context, message sqlc.EmailOutbox, cause error) (messageOutcome, error) {
	delay := s.retryDelay(message.Attempts)
	rows, err := s.queries.ScheduleEmailOutboxRetry(
		ctx,
		sqlc.ScheduleEmailOutboxRetryParams{
			RetryAfterSeconds: int32(delay / time.Second),
			LastError:         outboxErrorText(cause),
			EmailID:           message.ID,
		},
	)
	if err != nil {
		return messageUnchanged, errors.Join(cause, fmt.Errorf("schedule email retry: %w", err))
	}
	if rows != 1 {
		return messageUnchanged, errors.Join(cause, outboxStateError(message.ID, rows))
	}
	return messageRetried, cause
}

func (s *Service) failMessage(ctx context.Context, emailID int64, cause error) (messageOutcome, error) {
	rows, err := s.queries.MarkEmailOutboxFailed(
		ctx,
		sqlc.MarkEmailOutboxFailedParams{
			LastError: outboxErrorText(cause),
			EmailID:   emailID,
		},
	)
	if err != nil {
		return messageUnchanged, errors.Join(cause, fmt.Errorf("mark email failed: %w", err))
	}
	if rows != 1 {
		return messageUnchanged, errors.Join(cause, outboxStateError(emailID, rows))
	}
	return messageFailed, cause
}

func (s *Service) retryDelay(attempts int32) time.Duration {
	exponent := attempts - 1
	if exponent < 0 {
		exponent = 0
	}
	if exponent > maxRetryExponent {
		exponent = maxRetryExponent
	}
	multiplier := time.Duration(1) << exponent
	if s.baseRetryDelay > maxOutboxRetryDelay/multiplier {
		return maxOutboxRetryDelay
	}
	delay := s.baseRetryDelay * multiplier
	if delay > maxOutboxRetryDelay {
		return maxOutboxRetryDelay
	}
	return delay
}

func bookingEmailFromOutbox(message sqlc.EmailOutbox) (BookingEmail, error) {
	recipient := strings.TrimSpace(message.RecipientEmail)
	if recipient == "" {
		return BookingEmail{}, fmt.Errorf("%w: recipient is required", ErrInvalidEmailMessage)
	}
	if !supportedEventType(message.EventType) {
		return BookingEmail{}, fmt.Errorf(
			"%w: unsupported event type %q",
			ErrInvalidEmailMessage,
			message.EventType,
		)
	}

	var payload BookingEmailPayload
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		return BookingEmail{}, fmt.Errorf("%w: decode payload: %v", ErrInvalidEmailMessage, err)
	}
	if payload.BookingID <= 0 ||
		strings.TrimSpace(payload.GuestName) == "" ||
		strings.TrimSpace(payload.HotelName) == "" ||
		strings.TrimSpace(payload.RoomTypeName) == "" ||
		strings.TrimSpace(payload.CheckIn) == "" ||
		strings.TrimSpace(payload.CheckOut) == "" ||
		payload.RoomsCount <= 0 ||
		payload.GuestCount <= 0 ||
		strings.TrimSpace(payload.PricePerNight) == "" ||
		strings.TrimSpace(payload.TotalPrice) == "" ||
		strings.TrimSpace(payload.Currency) == "" {
		return BookingEmail{}, fmt.Errorf("%w: payload fields are incomplete", ErrInvalidEmailMessage)
	}

	return BookingEmail{
		To:        recipient,
		EventType: message.EventType,
		Payload:   payload,
	}, nil
}

func supportedEventType(eventType string) bool {
	switch eventType {
	case BookingConfirmedEvent, BookingCancelledEvent, PaymentRefundedEvent:
		return true
	default:
		return false
	}
}

func outboxErrorText(err error) pgtype.Text {
	message := strings.TrimSpace(err.Error())
	if message == "" {
		message = "email delivery failed"
	}
	runes := []rune(message)
	if len(runes) > maxStoredErrorRunes {
		message = string(runes[:maxStoredErrorRunes])
	}
	return pgtype.Text{String: message, Valid: true}
}

func outboxStateError(emailID, rows int64) error {
	return fmt.Errorf(
		"%w: email %d update affected %d rows",
		ErrOutboxStateChanged,
		emailID,
		rows,
	)
}
