package admin_booking

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestListBookingsForAdminValidatesInputBeforeQuery(t *testing.T) {
	valid := ListBookingsForAdminInput{
		Page:     DefaultBookingPage,
		PageSize: DefaultBookingPageSize,
	}

	tests := []struct {
		name    string
		mutate  func(*ListBookingsForAdminInput)
		wantErr error
	}{
		{
			name: "non-positive hotel ID",
			mutate: func(input *ListBookingsForAdminInput) {
				value := int64(0)
				input.HotelID = &value
			},
			wantErr: ErrInvalidHotelID,
		},
		{
			name: "invalid status",
			mutate: func(input *ListBookingsForAdminInput) {
				value := "pending"
				input.Status = &value
			},
			wantErr: ErrInvalidStatus,
		},
		{
			name: "reversed date range",
			mutate: func(input *ListBookingsForAdminInput) {
				from := time.Date(2030, time.January, 11, 0, 0, 0, 0, time.UTC)
				to := time.Date(2030, time.January, 10, 0, 0, 0, 0, time.UTC)
				input.CheckInFrom = &from
				input.CheckInTo = &to
			},
			wantErr: ErrInvalidDateRange,
		},
		{
			name: "zero page",
			mutate: func(input *ListBookingsForAdminInput) {
				input.Page = 0
			},
			wantErr: ErrInvalidPage,
		},
		{
			name: "zero page size",
			mutate: func(input *ListBookingsForAdminInput) {
				input.PageSize = 0
			},
			wantErr: ErrInvalidPageSize,
		},
		{
			name: "page size above maximum",
			mutate: func(input *ListBookingsForAdminInput) {
				input.PageSize = MaxBookingPageSize + 1
			},
			wantErr: ErrInvalidPageSize,
		},
	}

	service := &Service{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := valid
			tt.mutate(&input)

			_, err := service.ListBookingsForAdmin(context.Background(), input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ListBookingsForAdmin() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
