package amenities

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

var (
	ErrInvalidHotelID         = errors.New("hotel ID must be positive")
	ErrInvalidAmenityID       = errors.New("amenity ID must be positive")
	ErrInvalidAmenityName     = errors.New("amenity name must not be empty")
	ErrHotelNotFound          = errors.New("hotel not found")
	ErrHotelOrAmenityNotFound = errors.New("hotel or amenity not found")
	ErrAmenityNameConflict    = errors.New("amenity name already exists")
	ErrAmenityAlreadyAdded    = errors.New("amenity already added to hotel")
	ErrAmenityNotFound        = errors.New("amenity not found")
)

type Service struct {
	queries *sqlc.Queries
}

func NewService(queries *sqlc.Queries) *Service {
	return &Service{
		queries: queries,
	}
}

func (s *Service) CreateAmenity(ctx context.Context, name string) (sqlc.Amenity, error) {
	var pgErr *pgconn.PgError
	getname := strings.TrimSpace(name)
	if getname == "" {
		return sqlc.Amenity{}, ErrInvalidAmenityName
	}
	create, err := s.queries.CreateAmenity(ctx, getname)
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return sqlc.Amenity{}, ErrAmenityNameConflict
	}
	if err != nil {
		return sqlc.Amenity{}, fmt.Errorf("create amenity :%w", err)
	}
	return create, nil
}
func (s *Service) UpdateAmenity(ctx context.Context, amenityID int64, name string) (sqlc.Amenity, error) {
	var pgErr *pgconn.PgError
	getname := strings.TrimSpace(name)
	if getname == "" {
		return sqlc.Amenity{}, ErrInvalidAmenityName
	}
	if amenityID <= 0 {
		return sqlc.Amenity{}, ErrInvalidAmenityID
	}
	update, err := s.queries.UpdateAmenity(ctx, sqlc.UpdateAmenityParams{
		Name:      getname,
		AmenityID: amenityID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Amenity{}, ErrAmenityNotFound
	}
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return sqlc.Amenity{}, ErrAmenityNameConflict
	}
	if err != nil {
		return sqlc.Amenity{}, fmt.Errorf("update amenity:%w", err)
	}
	return update, nil
}
func (s *Service) DeleteAmenity(ctx context.Context, amenityID int64) error {
	if amenityID <= 0 {
		return ErrInvalidAmenityID
	}
	_, err := s.queries.DeleteAmenity(ctx, amenityID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAmenityNotFound
	}
	if err != nil {
		return fmt.Errorf("delete amenity:%w", err)
	}
	return nil
}
func (s *Service) ListAmenities(ctx context.Context) ([]sqlc.Amenity, error) {
	amenities, err := s.queries.ListAmenities(ctx)
	if err != nil {
		return nil, fmt.Errorf("list amenities: %w", err)
	}
	if amenities == nil {
		amenities = []sqlc.Amenity{}
	}
	return amenities, nil
}

func (s *Service) AddAmenityToHotel(ctx context.Context, hotelID int64, amenityID int64) (sqlc.HotelAmenity, error) {
	if hotelID <= 0 {
		return sqlc.HotelAmenity{}, ErrInvalidHotelID
	}
	if amenityID <= 0 {
		return sqlc.HotelAmenity{}, ErrInvalidAmenityID
	}

	hotelAmenity, err := s.queries.AddAmenityToHotel(ctx, sqlc.AddAmenityToHotelParams{
		HotelID:   hotelID,
		AmenityID: amenityID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.HotelAmenity{}, ErrHotelOrAmenityNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return sqlc.HotelAmenity{}, ErrAmenityAlreadyAdded
	}
	if err != nil {
		return sqlc.HotelAmenity{}, fmt.Errorf("add amenity to hotel: %w", err)
	}

	return hotelAmenity, nil
}

func (s *Service) RemoveAmenityFromHotel(ctx context.Context, hotelID int64, amenityID int64) error {
	if hotelID <= 0 {
		return ErrInvalidHotelID
	}
	if amenityID <= 0 {
		return ErrInvalidAmenityID
	}

	_, err := s.queries.RemoveAmenityFromHotel(ctx, sqlc.RemoveAmenityFromHotelParams{
		HotelID:   hotelID,
		AmenityID: amenityID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrHotelOrAmenityNotFound
	}
	if err != nil {
		return fmt.Errorf("remove amenity from hotel: %w", err)
	}

	return nil
}

func (s *Service) ListAmenitiesByHotel(ctx context.Context, hotelID int64) ([]sqlc.Amenity, error) {
	if hotelID <= 0 {
		return nil, ErrInvalidHotelID
	}

	hotel, err := s.queries.GetHotelByID(ctx, hotelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrHotelNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get hotel: %w", err)
	}

	amenities, err := s.queries.ListAmenitiesByHotel(ctx, hotel.ID)
	if err != nil {
		return nil, fmt.Errorf("list amenities by hotel: %w", err)
	}
	if amenities == nil {
		amenities = []sqlc.Amenity{}
	}

	return amenities, nil
}
