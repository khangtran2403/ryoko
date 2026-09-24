package inventory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSetBlockedInventoryValidatesBeforeStartingTransaction(t *testing.T) {
	today := time.Date(2030, time.January, 10, 0, 0, 0, 0, time.UTC)
	service := NewService(nil, nil)
	service.now = func() time.Time { return today }

	valid := SetBlockedInventoryInput{
		RoomTypeID:   1,
		DateFrom:     today,
		DateTo:       today.AddDate(0, 0, 3),
		RoomsBlocked: 1,
		BlockReason:  "Maintenance",
	}

	tests := []struct {
		name    string
		mutate  func(*SetBlockedInventoryInput)
		wantErr error
	}{
		{
			name: "invalid room type ID",
			mutate: func(input *SetBlockedInventoryInput) {
				input.RoomTypeID = 0
			},
			wantErr: ErrInvalidRoomTypeID,
		},
		{
			name: "date range is empty",
			mutate: func(input *SetBlockedInventoryInput) {
				input.DateTo = input.DateFrom
			},
			wantErr: ErrInvalidDateRange,
		},
		{
			name: "date range is reversed",
			mutate: func(input *SetBlockedInventoryInput) {
				input.DateTo = input.DateFrom.AddDate(0, 0, -1)
			},
			wantErr: ErrInvalidDateRange,
		},
		{
			name: "date starts in past",
			mutate: func(input *SetBlockedInventoryInput) {
				input.DateFrom = today.AddDate(0, 0, -1)
			},
			wantErr: ErrInventoryDateInPast,
		},
		{
			name: "range exceeds maximum",
			mutate: func(input *SetBlockedInventoryInput) {
				input.DateTo = input.DateFrom.AddDate(0, 0, MaxInventoryRangeDays+1)
			},
			wantErr: ErrInventoryRangeLarge,
		},
		{
			name: "negative blocked rooms",
			mutate: func(input *SetBlockedInventoryInput) {
				input.RoomsBlocked = -1
			},
			wantErr: ErrInvalidBlockedRooms,
		},
		{
			name: "blank reason",
			mutate: func(input *SetBlockedInventoryInput) {
				input.BlockReason = "   "
			},
			wantErr: ErrInvalidBlockReason,
		},
		{
			name: "reason exceeds Unicode character maximum",
			mutate: func(input *SetBlockedInventoryInput) {
				input.BlockReason = strings.Repeat("đ", MaxBlockReasonLength+1)
			},
			wantErr: ErrInvalidBlockReason,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := valid
			tt.mutate(&input)

			_, err := service.SetBlockedInventory(context.Background(), input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("SetBlockedInventory() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestListRoomTypeInventoryValidatesBeforeQuerying(t *testing.T) {
	service := NewService(nil, nil)
	from := time.Date(2020, time.January, 10, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		input   ListInventoryInput
		wantErr error
	}{
		{
			name: "invalid room type ID",
			input: ListInventoryInput{
				RoomTypeID: 0,
				DateFrom:   from,
				DateTo:     from.AddDate(0, 0, 1),
			},
			wantErr: ErrInvalidRoomTypeID,
		},
		{
			name: "invalid date range",
			input: ListInventoryInput{
				RoomTypeID: 1,
				DateFrom:   from,
				DateTo:     from,
			},
			wantErr: ErrInvalidDateRange,
		},
		{
			name: "range exceeds maximum",
			input: ListInventoryInput{
				RoomTypeID: 1,
				DateFrom:   from,
				DateTo:     from.AddDate(0, 0, MaxInventoryRangeDays+1),
			},
			wantErr: ErrInventoryRangeLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.ListRoomTypeInventory(context.Background(), tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ListRoomTypeInventory() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestDateOnlyUTCPreservesSuppliedCalendarDate(t *testing.T) {
	location := time.FixedZone("UTC+7", 7*60*60)
	input := time.Date(2030, time.March, 15, 23, 30, 0, 0, location)

	got := dateOnlyUTC(input)

	if got.Location() != time.UTC {
		t.Errorf("location = %v, want UTC", got.Location())
	}
	if got.Format(time.DateOnly) != "2030-03-15" {
		t.Errorf("date = %s, want 2030-03-15", got.Format(time.DateOnly))
	}
}
