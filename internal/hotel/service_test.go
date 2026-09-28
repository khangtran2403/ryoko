package hotel

import (
	"context"
	"errors"
	"testing"
)

func TestGetHotelDetailsByIDRejectsInvalidIDBeforeQuery(t *testing.T) {
	service := &Service{}

	for _, hotelID := range []int64{0, -1} {
		_, err := service.GetHotelDetailsByID(context.Background(), hotelID)
		if !errors.Is(err, ErrInvalidHotelID) {
			t.Errorf("GetHotelDetailsByID(%d) error = %v, want ErrInvalidHotelID", hotelID, err)
		}
	}
}

func TestDeactivateHotelRejectsInvalidIDBeforeQuery(t *testing.T) {
	service := &Service{}

	for _, hotelID := range []int64{0, -1} {
		err := service.DeactivateHotel(context.Background(), hotelID)
		if !errors.Is(err, ErrInvalidHotelID) {
			t.Errorf("DeactivateHotel(%d) error = %v, want ErrInvalidHotelID", hotelID, err)
		}
	}
}
