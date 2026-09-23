package booking

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCancelBookingAsAdminValidatesInputBeforeStartingTransaction(t *testing.T) {
	service := NewService(nil, nil)

	tests := []struct {
		name    string
		input   AdminCancellationInput
		wantErr error
	}{
		{
			name:    "invalid booking ID",
			input:   AdminCancellationInput{BookingID: 0, AdminID: 1, Reason: "Maintenance"},
			wantErr: ErrBookingNotFound,
		},
		{
			name:    "invalid admin ID",
			input:   AdminCancellationInput{BookingID: 1, AdminID: 0, Reason: "Maintenance"},
			wantErr: ErrInvalidUser,
		},
		{
			name:    "blank reason",
			input:   AdminCancellationInput{BookingID: 1, AdminID: 1, Reason: "   "},
			wantErr: ErrInvalidCancellationReason,
		},
		{
			name:    "reason over 500 Unicode characters",
			input:   AdminCancellationInput{BookingID: 1, AdminID: 1, Reason: strings.Repeat("đ", MaxCancellationReasonLength+1)},
			wantErr: ErrInvalidCancellationReason,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.CancelBookingAsAdmin(context.Background(), tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CancelBookingAsAdmin() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCreateBookingValidatesInputBeforeStartingTransaction(t *testing.T) {
	valid := CreateInput{
		UserID:         1,
		RoomTypeID:     1,
		CheckIn:        time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC),
		CheckOut:       time.Date(2030, time.January, 2, 0, 0, 0, 0, time.UTC),
		RoomsCount:     1,
		GuestCount:     1,
		IdempotencyKey: "create-booking-test",
	}

	tests := []struct {
		name    string
		mutate  func(*CreateInput)
		wantErr error
	}{
		{
			name: "check-in is in the past",
			mutate: func(input *CreateInput) {
				input.CheckIn = time.Date(2028, time.December, 31, 0, 0, 0, 0, time.UTC)
			},
			wantErr: ErrCheckInInPast,
		},
		{
			name: "checkout equals checkin",
			mutate: func(input *CreateInput) {
				input.CheckOut = input.CheckIn
			},
			wantErr: ErrInvalidDates,
		},
		{
			name: "checkout precedes checkin",
			mutate: func(input *CreateInput) {
				input.CheckOut = input.CheckIn.AddDate(0, 0, -1)
			},
			wantErr: ErrInvalidDates,
		},
		{
			name: "times on the same calendar date",
			mutate: func(input *CreateInput) {
				input.CheckIn = time.Date(2030, time.January, 1, 1, 0, 0, 0, time.UTC)
				input.CheckOut = time.Date(2030, time.January, 1, 23, 0, 0, 0, time.UTC)
			},
			wantErr: ErrInvalidDates,
		},
		{
			name: "zero rooms",
			mutate: func(input *CreateInput) {
				input.RoomsCount = 0
			},
			wantErr: ErrInvalidRooms,
		},
		{
			name: "negative rooms",
			mutate: func(input *CreateInput) {
				input.RoomsCount = -1
			},
			wantErr: ErrInvalidRooms,
		},
		{
			name: "zero guests",
			mutate: func(input *CreateInput) {
				input.GuestCount = 0
			},
			wantErr: ErrInvalidGuests,
		},
		{
			name: "invalid user",
			mutate: func(input *CreateInput) {
				input.UserID = 0
			},
			wantErr: ErrInvalidUser,
		},
		{
			name: "invalid room type",
			mutate: func(input *CreateInput) {
				input.RoomTypeID = 0
			},
			wantErr: ErrRoomTypeNotFound,
		},
		{
			name: "missing idempotency key",
			mutate: func(input *CreateInput) {
				input.IdempotencyKey = ""
			},
			wantErr: ErrInvalidIdempotencyKey,
		},
		{
			name: "whitespace-only idempotency key",
			mutate: func(input *CreateInput) {
				input.IdempotencyKey = "   "
			},
			wantErr: ErrInvalidIdempotencyKey,
		},
		{
			name: "idempotency key above maximum length",
			mutate: func(input *CreateInput) {
				input.IdempotencyKey = string(make([]byte, 256))
			},
			wantErr: ErrInvalidIdempotencyKey,
		},
	}

	service := &Service{
		now: func() time.Time {
			return time.Date(2029, time.January, 1, 12, 0, 0, 0, time.UTC)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := valid
			tt.mutate(&input)

			_, err := service.CreateBooking(context.Background(), input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CreateBooking() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestListAvailableRoomTypesValidatesInputBeforeQuery(t *testing.T) {
	valid := AvailabilityInput{
		HotelID:    1,
		CheckIn:    time.Date(2030, time.January, 1, 14, 0, 0, 0, time.UTC),
		CheckOut:   time.Date(2030, time.January, 2, 9, 0, 0, 0, time.UTC),
		RoomsCount: 1,
		GuestCount: 1,
	}
	tests := []struct {
		name    string
		mutate  func(*AvailabilityInput)
		wantErr error
	}{
		{
			name: "check-in is in the past",
			mutate: func(input *AvailabilityInput) {
				input.CheckIn = time.Date(2028, time.December, 31, 0, 0, 0, 0, time.UTC)
			},
			wantErr: ErrCheckInInPast,
		},
		{
			name: "invalid hotel ID",
			mutate: func(input *AvailabilityInput) {
				input.HotelID = 0
			},
			wantErr: ErrInvalidHotelID,
		},
		{
			name: "checkout equals checkin date",
			mutate: func(input *AvailabilityInput) {
				input.CheckOut = input.CheckIn.Add(2 * time.Hour)
			},
			wantErr: ErrInvalidDates,
		},
		{
			name: "checkout precedes checkin",
			mutate: func(input *AvailabilityInput) {
				input.CheckOut = input.CheckIn.AddDate(0, 0, -1)
			},
			wantErr: ErrInvalidDates,
		},
		{
			name: "invalid room count",
			mutate: func(input *AvailabilityInput) {
				input.RoomsCount = 0
			},
			wantErr: ErrInvalidRooms,
		},
		{
			name: "invalid guest count",
			mutate: func(input *AvailabilityInput) {
				input.GuestCount = -1
			},
			wantErr: ErrInvalidGuests,
		},
	}

	service := &Service{
		now: func() time.Time {
			return time.Date(2029, time.January, 1, 12, 0, 0, 0, time.UTC)
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := valid
			tt.mutate(&input)

			_, err := service.ListAvailableRoomTypes(context.Background(), input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ListAvailableRoomTypes() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestSearchAvailableHotelsValidatesInputBeforeQuery(t *testing.T) {
	valid := HotelSearchInput{
		City:       "Da Nang",
		CheckIn:    time.Date(2030, time.January, 10, 14, 0, 0, 0, time.UTC),
		CheckOut:   time.Date(2030, time.January, 13, 9, 0, 0, 0, time.UTC),
		RoomsCount: 1,
		GuestCount: 2,
		Page:       DefaultHotelSearchPage,
		PageSize:   DefaultHotelSearchPageSize,
		Sort:       HotelSearchSortPriceAsc,
	}
	tests := []struct {
		name    string
		mutate  func(*HotelSearchInput)
		wantErr error
	}{
		{
			name: "empty city",
			mutate: func(input *HotelSearchInput) {
				input.City = ""
			},
			wantErr: ErrInvalidCity,
		},
		{
			name: "whitespace-only city",
			mutate: func(input *HotelSearchInput) {
				input.City = "   "
			},
			wantErr: ErrInvalidCity,
		},
		{
			name: "check-in is in the past",
			mutate: func(input *HotelSearchInput) {
				input.CheckIn = time.Date(2028, time.December, 31, 0, 0, 0, 0, time.UTC)
			},
			wantErr: ErrCheckInInPast,
		},
		{
			name: "checkout equals checkin date",
			mutate: func(input *HotelSearchInput) {
				input.CheckOut = input.CheckIn.Add(2 * time.Hour)
			},
			wantErr: ErrInvalidDates,
		},
		{
			name: "checkout precedes checkin",
			mutate: func(input *HotelSearchInput) {
				input.CheckOut = input.CheckIn.AddDate(0, 0, -1)
			},
			wantErr: ErrInvalidDates,
		},
		{
			name: "invalid room count",
			mutate: func(input *HotelSearchInput) {
				input.RoomsCount = 0
			},
			wantErr: ErrInvalidRooms,
		},
		{
			name: "invalid guest count",
			mutate: func(input *HotelSearchInput) {
				input.GuestCount = -1
			},
			wantErr: ErrInvalidGuests,
		},
		{
			name: "invalid page",
			mutate: func(input *HotelSearchInput) {
				input.Page = 0
			},
			wantErr: ErrInvalidPage,
		},
		{
			name: "invalid page size",
			mutate: func(input *HotelSearchInput) {
				input.PageSize = 0
			},
			wantErr: ErrInvalidPageSize,
		},
		{
			name: "page size above maximum",
			mutate: func(input *HotelSearchInput) {
				input.PageSize = MaxHotelSearchPageSize + 1
			},
			wantErr: ErrInvalidPageSize,
		},
		{
			name: "invalid sort",
			mutate: func(input *HotelSearchInput) {
				input.Sort = "newest"
			},
			wantErr: ErrInvalidSort,
		},
	}

	service := &Service{
		now: func() time.Time {
			return time.Date(2029, time.January, 1, 12, 0, 0, 0, time.UTC)
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := valid
			tt.mutate(&input)

			_, err := service.SearchAvailableHotels(context.Background(), input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("SearchAvailableHotels() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestListBookingsByUserValidatesInputBeforeQuery(t *testing.T) {
	valid := ListBookingsInput{
		UserID:   42,
		Page:     DefaultBookingPage,
		PageSize: DefaultBookingPageSize,
	}
	tests := []struct {
		name    string
		mutate  func(*ListBookingsInput)
		wantErr error
	}{
		{
			name: "invalid user",
			mutate: func(input *ListBookingsInput) {
				input.UserID = 0
			},
			wantErr: ErrInvalidUser,
		},
		{
			name: "zero page",
			mutate: func(input *ListBookingsInput) {
				input.Page = 0
			},
			wantErr: ErrInvalidPage,
		},
		{
			name: "negative page",
			mutate: func(input *ListBookingsInput) {
				input.Page = -1
			},
			wantErr: ErrInvalidPage,
		},
		{
			name: "zero page size",
			mutate: func(input *ListBookingsInput) {
				input.PageSize = 0
			},
			wantErr: ErrInvalidPageSize,
		},
		{
			name: "negative page size",
			mutate: func(input *ListBookingsInput) {
				input.PageSize = -1
			},
			wantErr: ErrInvalidPageSize,
		},
		{
			name: "page size above maximum",
			mutate: func(input *ListBookingsInput) {
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

			_, err := service.ListBookingsByUser(context.Background(), input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ListBookingsByUser() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestDateOnlyUTCPreservesCalendarDate(t *testing.T) {
	vietnamTime := time.Date(
		2030,
		time.January,
		10,
		0,
		30,
		0,
		0,
		time.FixedZone("UTC+7", 7*60*60),
	)

	got := dateOnlyUTC(vietnamTime)
	want := time.Date(2030, time.January, 10, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("dateOnlyUTC() = %s, want %s", got.Format(time.DateOnly), want.Format(time.DateOnly))
	}
}
