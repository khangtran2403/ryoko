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
)

type CreateReview struct {
	Rating    int16   `json:"rating"`
	Comment   *string `json:"comment"`
	BookingID int64   `json:"booking_id"`
	UserID    int64   `json:"user_id"`
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
func (s *Service) ListReviewByHotel(ctx context.Context, hotelID int64) ([]sqlc.ListReviewsByHotelRow, error) {
	if hotelID <= 0 {
		return nil, ErrReviewNotFound
	}
	listReview, err := s.queries.ListReviewsByHotel(ctx, hotelID)
	if err != nil {
		return nil, fmt.Errorf("list review: %w", err)

	}
	if listReview == nil {
		listReview = []sqlc.ListReviewsByHotelRow{}
	}
	return listReview, nil
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
