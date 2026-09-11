package hotel

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

var (
	ErrInvalidHotelID = errors.New("hotel ID must be positive")
	ErrHotelNotFound  = errors.New("hotel not found")
)

type HotelResponse struct {
	ID            int64                         `json:"id"`
	Name          string                        `json:"name"`
	Address       string                        `json:"address"`
	City          string                        `json:"city"`
	Description   pgtype.Text                   `json:"description"`
	Images        []sqlc.HotelImage             `json:"images"`
	Amenities     []sqlc.Amenity                `json:"amenities"`
	RoomTypes     []sqlc.RoomType               `json:"room_types"`
	ReviewSummary sqlc.GetHotelReviewSummaryRow `json:"review_summary"`
}

type Service struct {
	queries *sqlc.Queries
}

func NewService(queries *sqlc.Queries) *Service {
	return &Service{
		queries: queries,
	}
}

func (s *Service) GetHotelDetailsByID(ctx context.Context, hotelID int64) (HotelResponse, error) {
	if hotelID <= 0 {
		return HotelResponse{}, ErrInvalidHotelID
	}
	hotel, err := s.queries.GetHotelByID(ctx, hotelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return HotelResponse{}, ErrHotelNotFound
	}
	if err != nil {
		return HotelResponse{}, fmt.Errorf("load hotel : %w", err)
	}
	listImages, err := s.queries.ListHotelImages(ctx, hotelID)
	if err != nil {
		return HotelResponse{}, fmt.Errorf("load hotel images : %w", err)
	}
	if listImages == nil {
		listImages = []sqlc.HotelImage{}
	}
	listAmenities, err := s.queries.ListAmenitiesByHotel(ctx, hotelID)
	if err != nil {
		return HotelResponse{}, fmt.Errorf("load hotel amenities : %w", err)
	}
	if listAmenities == nil {
		listAmenities = []sqlc.Amenity{}
	}
	listRoomtypes, err := s.queries.ListRoomTypesByHotel(ctx, hotelID)
	if err != nil {
		return HotelResponse{}, fmt.Errorf("load hotel room types : %w", err)
	}
	if listRoomtypes == nil {
		listRoomtypes = []sqlc.RoomType{}
	}
	getReviewSummary, err := s.queries.GetHotelReviewSummary(ctx, hotelID)
	if err != nil {
		return HotelResponse{}, fmt.Errorf("load hotel review summary : %w", err)
	}
	return HotelResponse{
		ID:            hotel.ID,
		Name:          hotel.Name,
		Address:       hotel.Address,
		City:          hotel.City,
		Description:   hotel.Description,
		Images:        listImages,
		Amenities:     listAmenities,
		RoomTypes:     listRoomtypes,
		ReviewSummary: getReviewSummary,
	}, nil
}
