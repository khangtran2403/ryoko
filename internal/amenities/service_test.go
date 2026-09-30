package amenities

import (
	"context"
	"errors"
	"testing"

	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

func TestAmenityServiceValidation(t *testing.T) {
	service := NewService(sqlc.New(nil))

	tests := []struct {
		name string
		call func() error
		want error
	}{
		{
			name: "blank amenity name",
			call: func() error {
				_, err := service.CreateAmenity(context.Background(), "  ")
				return err
			},
			want: ErrInvalidAmenityName,
		},
		{
			name: "add with invalid hotel ID",
			call: func() error {
				_, err := service.AddAmenityToHotel(context.Background(), 0, 1)
				return err
			},
			want: ErrInvalidHotelID,
		},
		{
			name: "update with invalid amenity ID",
			call: func() error {
				_, err := service.UpdateAmenity(context.Background(), 0, "Pool")
				return err
			},
			want: ErrInvalidAmenityID,
		},
		{
			name: "update with blank name",
			call: func() error {
				_, err := service.UpdateAmenity(context.Background(), 1, "  ")
				return err
			},
			want: ErrInvalidAmenityName,
		},
		{
			name: "delete with invalid amenity ID",
			call: func() error {
				return service.DeleteAmenity(context.Background(), 0)
			},
			want: ErrInvalidAmenityID,
		},
		{
			name: "add with invalid amenity ID",
			call: func() error {
				_, err := service.AddAmenityToHotel(context.Background(), 1, 0)
				return err
			},
			want: ErrInvalidAmenityID,
		},
		{
			name: "remove with invalid hotel ID",
			call: func() error {
				return service.RemoveAmenityFromHotel(context.Background(), -1, 1)
			},
			want: ErrInvalidHotelID,
		},
		{
			name: "remove with invalid amenity ID",
			call: func() error {
				return service.RemoveAmenityFromHotel(context.Background(), 1, -1)
			},
			want: ErrInvalidAmenityID,
		},
		{
			name: "list-by-hotel with invalid hotel ID",
			call: func() error {
				_, err := service.ListAmenitiesByHotel(context.Background(), 0)
				return err
			},
			want: ErrInvalidHotelID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}
