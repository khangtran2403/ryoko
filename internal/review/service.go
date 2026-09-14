package review

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

var (
	ErrInvalidRating        = errors.New("rating must be between 1 and 5")
	ErrBlankComment         = errors.New("Comment must not be empty")
	ErrBookingNotReviewable = errors.New("booking cannot be reviewed")
	ErrReviewAlreadyExists  = errors.New("review already exists")
	ErrReviewNotFound       = errors.New("review not found")
	ErrReviewNotEditable    = errors.New("review cannot be edited")
	ErrInvalidPage          = errors.New("page must be a positive integer")
	ErrInvalidPageSize      = errors.New("page_size must be between 1 and 100")
)

const (
	DefaultReviewPage     int32 = 1
	DefaultReviewPageSize int32 = 20
	MaxReviewPageSize     int32 = 100
)

type CreateReview struct {
	Rating    int16   `json:"rating"`
	Comment   *string `json:"comment"`
	BookingID int64   `json:"booking_id"`
	UserID    int64   `json:"user_id"`
}
type UpdateReviewInput struct {
	ReviewID int64
	UserID   int64
	Rating   int16
	Comment  *string
}
type HotelReviewPagination struct {
	Page     int32 `json:"page"`
	PageSize int32 `json:"page_size"`
	HasMore  bool  `json:"has_more"`
}
type ListReviewsByHotelInput struct {
	HotelID  int64
	Page     int32
	PageSize int32
}
type HotelReviewResult struct {
	Reviews    []sqlc.ListReviewsByHotelRow `json:"reviews"`
	Pagination HotelReviewPagination        `json:"pagination"`
}
type Service struct {
	queries *sqlc.Queries
}

func NewService(queries *sqlc.Queries) *Service {
	return &Service{
		queries: queries,
	}
}
func (s *Service) CreateReview(ctx context.Context, input CreateReview) (sqlc.Review, error) {
	var pgErr *pgconn.PgError
	if input.UserID <= 0 || input.BookingID <= 0 {
		return sqlc.Review{}, ErrBookingNotReviewable
	}
	if input.Rating < 1 || input.Rating > 5 {
		return sqlc.Review{}, ErrInvalidRating
	}
	comment, err := nullableComment(input.Comment)
	if err != nil {
		return sqlc.Review{}, err
	}
	create, err := s.queries.CreateReviewForCompletedBooking(ctx, sqlc.CreateReviewForCompletedBookingParams{
		Rating:    input.Rating,
		Comment:   comment,
		BookingID: input.BookingID,
		UserID:    input.UserID,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return sqlc.Review{}, ErrBookingNotReviewable

	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		return sqlc.Review{}, ErrReviewAlreadyExists
	case err != nil:
		return sqlc.Review{}, fmt.Errorf("create review: %w", err)
	default:

		return create, nil
	}

}
func (s *Service) GetReviewByID(ctx context.Context, reviewID int64) (sqlc.GetReviewByIDRow, error) {
	if reviewID <= 0 {
		return sqlc.GetReviewByIDRow{}, ErrReviewNotFound
	}
	getReview, err := s.queries.GetReviewByID(ctx, reviewID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.GetReviewByIDRow{}, ErrReviewNotFound
	}
	if err != nil {
		return sqlc.GetReviewByIDRow{}, fmt.Errorf("get review: %w", err)

	}
	return getReview, nil
}
func (s *Service) ListReviewByHotel(ctx context.Context, input ListReviewsByHotelInput) (HotelReviewResult, error) {
	if input.Page <= 0 {
		return HotelReviewResult{}, ErrInvalidPage
	}
	if input.PageSize <= 0 || input.PageSize > MaxReviewPageSize {
		return HotelReviewResult{}, ErrInvalidPageSize
	}
	resultOffset := int64(input.Page-1) * int64(input.PageSize)

	listReview, err := s.queries.ListReviewsByHotel(ctx, sqlc.ListReviewsByHotelParams{
		HotelID:      input.HotelID,
		ResultLimit:  int64(input.PageSize) + 1,
		ResultOffset: resultOffset,
	})
	if err != nil {
		return HotelReviewResult{}, fmt.Errorf("list review: %w", err)

	}
	hasMore := len(listReview) > int(input.PageSize)
	if hasMore {
		listReview = listReview[:input.PageSize]
	}
	if listReview == nil {
		listReview = []sqlc.ListReviewsByHotelRow{}
	}
	return HotelReviewResult{
		Reviews: listReview,
		Pagination: HotelReviewPagination{
			Page:     input.Page,
			PageSize: input.PageSize,
			HasMore:  hasMore,
		},
	}, nil
}
func nullableComment(value *string) (pgtype.Text, error) {
	if value == nil {
		return pgtype.Text{}, nil
	}

	comment := strings.TrimSpace(*value)
	if comment == "" {
		return pgtype.Text{}, ErrBlankComment
	}

	return pgtype.Text{
		String: comment,
		Valid:  true,
	}, nil
}
func (s *Service) UpdateReviewByUser(ctx context.Context, input UpdateReviewInput) (sqlc.Review, error) {
	if input.UserID <= 0 || input.ReviewID <= 0 {
		return sqlc.Review{}, ErrBookingNotReviewable
	}
	if input.Rating < 1 || input.Rating > 5 {
		return sqlc.Review{}, ErrInvalidRating
	}
	comment, err := nullableComment(input.Comment)
	if err != nil {
		return sqlc.Review{}, err
	}
	update, err := s.queries.UpdateReviewByUser(ctx, sqlc.UpdateReviewByUserParams{
		Rating:   input.Rating,
		Comment:  comment,
		ReviewID: input.ReviewID,
		UserID:   input.UserID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Review{}, ErrReviewNotEditable
	}
	if err != nil {
		return sqlc.Review{}, fmt.Errorf("Update review : %w", err)
	}
	return update, nil
}
func (s *Service) DeleteReview(ctx context.Context, reviewID int64, userID int64) (int64, error) {
	if userID <= 0 || reviewID <= 0 {
		return 0, ErrBookingNotReviewable
	}
	delete, err := s.queries.DeleteReviewByUser(ctx, sqlc.DeleteReviewByUserParams{
		UserID:   userID,
		ReviewID: reviewID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrReviewNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("delete review : %w", err)
	}
	return delete, nil
}
