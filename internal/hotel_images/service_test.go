package hotel_images

import (
	"context"
	"errors"
	"testing"
)

func TestServiceRejectsInvalidInput(t *testing.T) {
	service := &Service{}

	tests := []struct {
		name    string
		call    func() error
		wantErr error
	}{
		{
			name: "create with invalid hotel ID",
			call: func() error {
				_, err := service.CreateHotelImage(context.Background(), 0, "https://example.com/image.jpg")
				return err
			},
			wantErr: ErrInvalidHotelID,
		},
		{
			name: "create with blank URL",
			call: func() error {
				_, err := service.CreateHotelImage(context.Background(), 1, "  \t\n ")
				return err
			},
			wantErr: ErrInvalidImageURL,
		},
		{
			name: "list with invalid hotel ID",
			call: func() error {
				_, err := service.ListHotelImages(context.Background(), -1)
				return err
			},
			wantErr: ErrInvalidHotelID,
		},
		{
			name: "set primary with invalid hotel ID",
			call: func() error {
				_, err := service.SetPrimaryHotelImage(context.Background(), 0, 1)
				return err
			},
			wantErr: ErrInvalidHotelID,
		},
		{
			name: "set primary with invalid image ID",
			call: func() error {
				_, err := service.SetPrimaryHotelImage(context.Background(), 1, 0)
				return err
			},
			wantErr: ErrInvalidImageID,
		},
		{
			name: "delete with invalid hotel ID",
			call: func() error {
				return service.DeleteHotelImage(context.Background(), 0, 1)
			},
			wantErr: ErrInvalidHotelID,
		},
		{
			name: "delete with invalid image ID",
			call: func() error {
				return service.DeleteHotelImage(context.Background(), 1, 0)
			},
			wantErr: ErrInvalidImageID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
