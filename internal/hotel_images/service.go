package hotel_images

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

var (
	ErrInvalidHotelID  = errors.New("hotel ID must be positive")
	ErrInvalidImageID  = errors.New("image ID must be positive")
	ErrInvalidImageURL = errors.New("image URL must not be empty")
	ErrHotelNotFound   = errors.New("hotel not found")
	ErrImageNotFound   = errors.New("hotel image not found")
)

type Service struct {
	pool    *pgxpool.Pool
	queries *sqlc.Queries
}

func NewService(pool *pgxpool.Pool, queries *sqlc.Queries) *Service {
	return &Service{
		pool:    pool,
		queries: queries,
	}
}

func (s *Service) CreateHotelImage(ctx context.Context, hotelID int64, imageURL string) (sqlc.HotelImage, error) {
	if hotelID <= 0 {
		return sqlc.HotelImage{}, ErrInvalidHotelID
	}
	imageURL = strings.TrimSpace(imageURL)
	if imageURL == "" {
		return sqlc.HotelImage{}, ErrInvalidImageURL
	}
	create, err := s.queries.CreateHotelImage(ctx, sqlc.CreateHotelImageParams{
		HotelID:  hotelID,
		ImageUrl: imageURL,
	})
	var pgErr *pgconn.PgError

	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return sqlc.HotelImage{}, ErrHotelNotFound
	}
	if err != nil {
		return sqlc.HotelImage{}, fmt.Errorf("create image for hotel:%w", err)
	}
	return create, nil
}
func (s *Service) ListHotelImages(ctx context.Context, hotelID int64) ([]sqlc.HotelImage, error) {
	if hotelID <= 0 {
		return nil, ErrInvalidHotelID
	}
	listImages, err := s.queries.ListHotelImages(ctx, hotelID)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)

	}
	if listImages == nil {
		listImages = []sqlc.HotelImage{}
	}
	return listImages, nil
}
func (s *Service) SetPrimaryHotelImage(ctx context.Context, hotelID, imageID int64) (sqlc.HotelImage, error) {
	if hotelID <= 0 {
		return sqlc.HotelImage{}, ErrInvalidHotelID
	}
	if imageID <= 0 {
		return sqlc.HotelImage{}, ErrInvalidImageID
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.ReadCommitted,
	})
	if err != nil {
		return sqlc.HotelImage{}, fmt.Errorf("begin transaction %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)

	_, err = qtx.LockHotelForImageUpdate(ctx, hotelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.HotelImage{}, ErrHotelNotFound
	}
	if err != nil {
		return sqlc.HotelImage{}, fmt.Errorf("lock hotel row %w", err)
	}
	err = qtx.ClearPrimaryHotelImage(ctx, hotelID)
	if err != nil {
		return sqlc.HotelImage{}, fmt.Errorf("clear image %w", err)
	}
	setImage, err := qtx.SetPrimaryHotelImage(ctx, sqlc.SetPrimaryHotelImageParams{
		ImageID: imageID,
		HotelID: hotelID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.HotelImage{}, ErrImageNotFound
	}
	if err != nil {
		return sqlc.HotelImage{}, fmt.Errorf("set primary hotel image %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sqlc.HotelImage{}, fmt.Errorf(
			"commit transaction: %w",
			err,
		)
	}
	return setImage, nil
}

func (s *Service) DeleteHotelImage(ctx context.Context, hotelID, imageID int64) error {
	if hotelID <= 0 {
		return ErrInvalidHotelID
	}
	if imageID <= 0 {
		return ErrInvalidImageID
	}
	_, err := s.queries.DeleteHotelImage(ctx, sqlc.DeleteHotelImageParams{
		ImageID: imageID,
		HotelID: hotelID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrImageNotFound
	}
	if err != nil {
		return fmt.Errorf("delete image:%w", err)
	}
	return nil
}
