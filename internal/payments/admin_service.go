package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

const (
	DefaultAdminPaymentPage     int32 = 1
	DefaultAdminPaymentPageSize int32 = 20
	MaxAdminPaymentPageSize     int32 = 100
)

var (
	ErrInvalidAdminPaymentID       = errors.New("payment ID must be positive")
	ErrInvalidAdminPaymentStatus   = errors.New("status must be pending, succeeded, expired, refund_pending, or refunded")
	ErrInvalidAdminPaymentProvider = errors.New("provider must not be blank or longer than 100 characters")
	ErrInvalidAdminPaymentPage     = errors.New("page must be a positive integer")
	ErrInvalidAdminPaymentPageSize = errors.New("page_size must be between 1 and 100")
	ErrAdminRefundNotRetryable     = errors.New("payment refund is not retryable")
	ErrAdminRefundInProgress       = errors.New("payment refund is currently being processed")
	ErrAdminRefundStateChanged     = errors.New("payment refund state changed concurrently")
)

type adminPaymentQueries interface {
	ListPaymentsForAdmin(context.Context, sqlc.ListPaymentsForAdminParams) ([]sqlc.ListPaymentsForAdminRow, error)
	ListPaymentRefundAttemptsForAdmin(context.Context, sqlc.ListPaymentRefundAttemptsForAdminParams) ([]sqlc.PaymentRefundAttempt, error)
	GetPaymentForAdmin(context.Context, int64) (sqlc.GetPaymentForAdminRow, error)
	RetryFailedPaymentRefundForAdmin(context.Context, int64) (sqlc.RetryFailedPaymentRefundForAdminRow, error)
}

type AdminPayment struct {
	ID                int64
	BookingID         int64
	BookingStatus     string
	CustomerID        int64
	CustomerName      string
	CustomerEmail     string
	HotelID           int64
	HotelName         string
	Provider          string
	ProviderPaymentID pgtype.Text
	AmountMinor       int64
	Currency          string
	Status            string
	FailureCode       pgtype.Text
	FailureMessage    pgtype.Text
	PaidAt            pgtype.Timestamptz
	RefundedAt        pgtype.Timestamptz
	RefundAttempts    int32
	RefundAvailableAt pgtype.Timestamptz
	RefundLockedAt    pgtype.Timestamptz
	CreatedAt         pgtype.Timestamptz
	UpdatedAt         pgtype.Timestamptz
}

type ListAdminPaymentsInput struct {
	Status    *string
	Provider  *string
	BookingID *int64
	Page      int32
	PageSize  int32
}

type AdminPaymentPagination struct {
	Page     int32 `json:"page"`
	PageSize int32 `json:"page_size"`
	HasMore  bool  `json:"has_more"`
}

type ListAdminPaymentsResult struct {
	Payments   []AdminPayment
	Pagination AdminPaymentPagination
}

type AdminRefundAttempt struct {
	ID             int64
	PaymentID      int64
	AttemptNumber  int32
	Status         string
	FailureCode    pgtype.Text
	FailureMessage pgtype.Text
	StartedAt      pgtype.Timestamptz
	CompletedAt    pgtype.Timestamptz
}

type ListAdminRefundAttemptsInput struct {
	PaymentID int64
	Page      int32
	PageSize  int32
}

type ListAdminRefundAttemptsResult struct {
	Attempts   []AdminRefundAttempt
	Pagination AdminPaymentPagination
}

type AdminService struct {
	queries adminPaymentQueries
}

func NewAdminService(queries adminPaymentQueries) *AdminService {
	if queries == nil {
		panic("admin payment queries must not be nil")
	}
	return &AdminService{queries: queries}
}

func (s *AdminService) ListPayments(
	ctx context.Context,
	input ListAdminPaymentsInput,
) (ListAdminPaymentsResult, error) {
	if input.Page <= 0 {
		return ListAdminPaymentsResult{}, ErrInvalidAdminPaymentPage
	}
	if input.PageSize <= 0 || input.PageSize > MaxAdminPaymentPageSize {
		return ListAdminPaymentsResult{}, ErrInvalidAdminPaymentPageSize
	}

	var status pgtype.Text
	if input.Status != nil {
		if !supportedAdminPaymentStatus(*input.Status) {
			return ListAdminPaymentsResult{}, ErrInvalidAdminPaymentStatus
		}
		status = pgtype.Text{String: *input.Status, Valid: true}
	}

	var provider pgtype.Text
	if input.Provider != nil {
		normalized := strings.TrimSpace(*input.Provider)
		if normalized == "" || len(normalized) > 100 {
			return ListAdminPaymentsResult{}, ErrInvalidAdminPaymentProvider
		}
		provider = pgtype.Text{String: normalized, Valid: true}
	}

	var bookingID pgtype.Int8
	if input.BookingID != nil {
		if *input.BookingID <= 0 {
			return ListAdminPaymentsResult{}, ErrInvalidBookingID
		}
		bookingID = pgtype.Int8{Int64: *input.BookingID, Valid: true}
	}

	rows, err := s.queries.ListPaymentsForAdmin(ctx, sqlc.ListPaymentsForAdminParams{
		PaymentStatus: status,
		Provider:      provider,
		BookingID:     bookingID,
		ResultOffset:  int64(input.Page-1) * int64(input.PageSize),
		ResultLimit:   input.PageSize + 1,
	})
	if err != nil {
		return ListAdminPaymentsResult{}, fmt.Errorf("list payments for admin: %w", err)
	}

	hasMore := len(rows) > int(input.PageSize)
	if hasMore {
		rows = rows[:input.PageSize]
	}
	payments := make([]AdminPayment, len(rows))
	for index, row := range rows {
		payments[index] = adminPaymentFromListRow(row)
	}

	return ListAdminPaymentsResult{
		Payments: payments,
		Pagination: AdminPaymentPagination{
			Page: input.Page, PageSize: input.PageSize, HasMore: hasMore,
		},
	}, nil
}

func (s *AdminService) GetPayment(ctx context.Context, paymentID int64) (AdminPayment, error) {
	if paymentID <= 0 {
		return AdminPayment{}, ErrInvalidAdminPaymentID
	}
	row, err := s.queries.GetPaymentForAdmin(ctx, paymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminPayment{}, ErrPaymentNotFound
	}
	if err != nil {
		return AdminPayment{}, fmt.Errorf("get payment for admin: %w", err)
	}
	return adminPaymentFromGetRow(row), nil
}

func (s *AdminService) ListRefundAttempts(
	ctx context.Context,
	input ListAdminRefundAttemptsInput,
) (ListAdminRefundAttemptsResult, error) {
	if input.PaymentID <= 0 {
		return ListAdminRefundAttemptsResult{}, ErrInvalidAdminPaymentID
	}
	if input.Page <= 0 {
		return ListAdminRefundAttemptsResult{}, ErrInvalidAdminPaymentPage
	}
	if input.PageSize <= 0 || input.PageSize > MaxAdminPaymentPageSize {
		return ListAdminRefundAttemptsResult{}, ErrInvalidAdminPaymentPageSize
	}

	if _, err := s.queries.GetPaymentForAdmin(ctx, input.PaymentID); errors.Is(err, pgx.ErrNoRows) {
		return ListAdminRefundAttemptsResult{}, ErrPaymentNotFound
	} else if err != nil {
		return ListAdminRefundAttemptsResult{}, fmt.Errorf("get payment before listing refund attempts: %w", err)
	}

	rows, err := s.queries.ListPaymentRefundAttemptsForAdmin(
		ctx,
		sqlc.ListPaymentRefundAttemptsForAdminParams{
			PaymentID:    input.PaymentID,
			ResultOffset: int64(input.Page-1) * int64(input.PageSize),
			ResultLimit:  input.PageSize + 1,
		},
	)
	if err != nil {
		return ListAdminRefundAttemptsResult{}, fmt.Errorf("list payment refund attempts for admin: %w", err)
	}

	hasMore := len(rows) > int(input.PageSize)
	if hasMore {
		rows = rows[:input.PageSize]
	}
	attempts := make([]AdminRefundAttempt, len(rows))
	for index, row := range rows {
		attempts[index] = AdminRefundAttempt{
			ID: row.ID, PaymentID: row.PaymentID, AttemptNumber: row.AttemptNumber,
			Status: row.Status, FailureCode: row.FailureCode, FailureMessage: row.FailureMessage,
			StartedAt: row.StartedAt, CompletedAt: row.CompletedAt,
		}
	}

	return ListAdminRefundAttemptsResult{
		Attempts: attempts,
		Pagination: AdminPaymentPagination{
			Page: input.Page, PageSize: input.PageSize, HasMore: hasMore,
		},
	}, nil
}

func (s *AdminService) RetryFailedRefund(ctx context.Context, paymentID int64) (AdminPayment, error) {
	if paymentID <= 0 {
		return AdminPayment{}, ErrInvalidAdminPaymentID
	}

	row, err := s.queries.RetryFailedPaymentRefundForAdmin(ctx, paymentID)
	if err == nil {
		return adminPaymentFromRetryRow(row), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AdminPayment{}, fmt.Errorf("retry failed payment refund for admin: %w", err)
	}

	payment, lookupErr := s.queries.GetPaymentForAdmin(ctx, paymentID)
	if errors.Is(lookupErr, pgx.ErrNoRows) {
		return AdminPayment{}, ErrPaymentNotFound
	}
	if lookupErr != nil {
		return AdminPayment{}, fmt.Errorf("get payment after refund retry conflict: %w", lookupErr)
	}
	if payment.Status != "refund_pending" || !payment.FailureCode.Valid {
		return AdminPayment{}, ErrAdminRefundNotRetryable
	}
	if payment.RefundLockedAt.Valid {
		return AdminPayment{}, ErrAdminRefundInProgress
	}
	return AdminPayment{}, ErrAdminRefundStateChanged
}

func supportedAdminPaymentStatus(status string) bool {
	switch status {
	case "pending", "succeeded", "expired", "refund_pending", "refunded":
		return true
	default:
		return false
	}
}

func adminPaymentFromListRow(row sqlc.ListPaymentsForAdminRow) AdminPayment {
	return AdminPayment{
		ID: row.ID, BookingID: row.BookingID, BookingStatus: row.BookingStatus,
		CustomerID: row.CustomerID, CustomerName: row.CustomerName, CustomerEmail: row.CustomerEmail,
		HotelID: row.HotelID, HotelName: row.HotelName, Provider: row.Provider,
		ProviderPaymentID: row.ProviderPaymentID, AmountMinor: row.AmountMinor, Currency: row.Currency,
		Status: row.Status, FailureCode: row.FailureCode, FailureMessage: row.FailureMessage,
		PaidAt: row.PaidAt, RefundedAt: row.RefundedAt,
		RefundAttempts: row.RefundAttempts, RefundAvailableAt: row.RefundAvailableAt, RefundLockedAt: row.RefundLockedAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func adminPaymentFromGetRow(row sqlc.GetPaymentForAdminRow) AdminPayment {
	return AdminPayment{
		ID: row.ID, BookingID: row.BookingID, BookingStatus: row.BookingStatus,
		CustomerID: row.CustomerID, CustomerName: row.CustomerName, CustomerEmail: row.CustomerEmail,
		HotelID: row.HotelID, HotelName: row.HotelName, Provider: row.Provider,
		ProviderPaymentID: row.ProviderPaymentID, AmountMinor: row.AmountMinor, Currency: row.Currency,
		Status: row.Status, FailureCode: row.FailureCode, FailureMessage: row.FailureMessage,
		PaidAt: row.PaidAt, RefundedAt: row.RefundedAt,
		RefundAttempts: row.RefundAttempts, RefundAvailableAt: row.RefundAvailableAt, RefundLockedAt: row.RefundLockedAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func adminPaymentFromRetryRow(row sqlc.RetryFailedPaymentRefundForAdminRow) AdminPayment {
	return AdminPayment{
		ID: row.ID, BookingID: row.BookingID, BookingStatus: row.BookingStatus,
		CustomerID: row.CustomerID, CustomerName: row.CustomerName, CustomerEmail: row.CustomerEmail,
		HotelID: row.HotelID, HotelName: row.HotelName, Provider: row.Provider,
		ProviderPaymentID: row.ProviderPaymentID, AmountMinor: row.AmountMinor, Currency: row.Currency,
		Status: row.Status, FailureCode: row.FailureCode, FailureMessage: row.FailureMessage,
		PaidAt: row.PaidAt, RefundedAt: row.RefundedAt,
		RefundAttempts: row.RefundAttempts, RefundAvailableAt: row.RefundAvailableAt, RefundLockedAt: row.RefundLockedAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
