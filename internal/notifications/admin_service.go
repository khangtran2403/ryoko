package notification

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

const (
	DefaultAdminEmailPage     int32 = 1
	DefaultAdminEmailPageSize int32 = 20
	MaxAdminEmailPageSize     int32 = 100
)

var (
	ErrInvalidAdminEmailStatus   = errors.New("status must be pending, processing, sent, or failed")
	ErrInvalidAdminEmailPage     = errors.New("page must be a positive integer")
	ErrInvalidAdminEmailPageSize = errors.New("page_size must be between 1 and 100")
	ErrAdminEmailNotFound        = errors.New("email outbox message not found")
	ErrAdminEmailNotFailed       = errors.New("only failed email outbox messages can be retried")
)

type adminEmailQueries interface {
	ListEmailOutboxForAdmin(context.Context, sqlc.ListEmailOutboxForAdminParams) ([]sqlc.ListEmailOutboxForAdminRow, error)
	RetryFailedEmailOutbox(context.Context, int64) (sqlc.RetryFailedEmailOutboxRow, error)
	GetEmailOutboxByID(context.Context, int64) (sqlc.EmailOutbox, error)
}

type AdminEmail struct {
	ID             int64
	BookingID      int64
	EventType      string
	RecipientEmail string
	Status         string
	Attempts       int32
	AvailableAt    pgtype.Timestamptz
	LockedAt       pgtype.Timestamptz
	SentAt         pgtype.Timestamptz
	LastError      pgtype.Text
	CreatedAt      pgtype.Timestamptz
	UpdatedAt      pgtype.Timestamptz
}

type ListAdminEmailsInput struct {
	Status   *string
	Page     int32
	PageSize int32
}

type AdminEmailPagination struct {
	Page     int32 `json:"page"`
	PageSize int32 `json:"page_size"`
	HasMore  bool  `json:"has_more"`
}

type ListAdminEmailsResult struct {
	Emails     []AdminEmail
	Pagination AdminEmailPagination
}

type AdminService struct {
	queries adminEmailQueries
}

func NewAdminService(queries adminEmailQueries) *AdminService {
	if queries == nil {
		panic("notification admin queries must not be nil")
	}
	return &AdminService{queries: queries}
}

func (s *AdminService) ListEmails(
	ctx context.Context,
	input ListAdminEmailsInput,
) (ListAdminEmailsResult, error) {
	if input.Page <= 0 {
		return ListAdminEmailsResult{}, ErrInvalidAdminEmailPage
	}
	if input.PageSize <= 0 || input.PageSize > MaxAdminEmailPageSize {
		return ListAdminEmailsResult{}, ErrInvalidAdminEmailPageSize
	}

	var status pgtype.Text
	if input.Status != nil {
		if !supportedAdminEmailStatus(*input.Status) {
			return ListAdminEmailsResult{}, ErrInvalidAdminEmailStatus
		}
		status = pgtype.Text{String: *input.Status, Valid: true}
	}

	offset := int64(input.Page-1) * int64(input.PageSize)
	rows, err := s.queries.ListEmailOutboxForAdmin(
		ctx,
		sqlc.ListEmailOutboxForAdminParams{
			Status:       status,
			ResultLimit:  input.PageSize + 1,
			ResultOffset: offset,
		},
	)
	if err != nil {
		return ListAdminEmailsResult{}, fmt.Errorf("list email outbox for admin: %w", err)
	}

	hasMore := len(rows) > int(input.PageSize)
	if hasMore {
		rows = rows[:input.PageSize]
	}
	emails := make([]AdminEmail, len(rows))
	for index, row := range rows {
		emails[index] = adminEmailFromListRow(row)
	}

	return ListAdminEmailsResult{
		Emails: emails,
		Pagination: AdminEmailPagination{
			Page: input.Page, PageSize: input.PageSize, HasMore: hasMore,
		},
	}, nil
}

func (s *AdminService) RetryFailedEmail(ctx context.Context, emailID int64) (AdminEmail, error) {
	if emailID <= 0 {
		return AdminEmail{}, ErrAdminEmailNotFound
	}

	row, err := s.queries.RetryFailedEmailOutbox(ctx, emailID)
	if err == nil {
		return adminEmailFromRetryRow(row), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AdminEmail{}, fmt.Errorf("retry failed email outbox message: %w", err)
	}

	_, lookupErr := s.queries.GetEmailOutboxByID(ctx, emailID)
	if errors.Is(lookupErr, pgx.ErrNoRows) {
		return AdminEmail{}, ErrAdminEmailNotFound
	}
	if lookupErr != nil {
		return AdminEmail{}, fmt.Errorf("get email outbox message after retry conflict: %w", lookupErr)
	}
	return AdminEmail{}, ErrAdminEmailNotFailed
}

func supportedAdminEmailStatus(status string) bool {
	switch status {
	case "pending", "processing", "sent", "failed":
		return true
	default:
		return false
	}
}

func adminEmailFromListRow(row sqlc.ListEmailOutboxForAdminRow) AdminEmail {
	return AdminEmail{
		ID: row.ID, BookingID: row.BookingID, EventType: row.EventType,
		RecipientEmail: row.RecipientEmail, Status: row.Status, Attempts: row.Attempts,
		AvailableAt: row.AvailableAt, LockedAt: row.LockedAt, SentAt: row.SentAt,
		LastError: row.LastError, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func adminEmailFromRetryRow(row sqlc.RetryFailedEmailOutboxRow) AdminEmail {
	return AdminEmail{
		ID: row.ID, BookingID: row.BookingID, EventType: row.EventType,
		RecipientEmail: row.RecipientEmail, Status: row.Status, Attempts: row.Attempts,
		AvailableAt: row.AvailableAt, LockedAt: row.LockedAt, SentAt: row.SentAt,
		LastError: row.LastError, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
