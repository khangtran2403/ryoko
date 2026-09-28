package roomtype

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestUpdateRoomTypeValidatesBeforeStartingTransaction(t *testing.T) {
	tests := []struct {
		name    string
		input   UpdateRoomTypeRequest
		wantErr error
	}{
		{
			name: "invalid room type ID",
			input: UpdateRoomTypeRequest{
				ID:         0,
				TotalRooms: 5,
			},
			wantErr: ErrInvalidID,
		},
		{
			name: "zero total rooms",
			input: UpdateRoomTypeRequest{
				ID:         1,
				TotalRooms: 0,
			},
			wantErr: ErrInvalidTotalRooms,
		},
		{
			name: "negative total rooms",
			input: UpdateRoomTypeRequest{
				ID:         1,
				TotalRooms: -1,
			},
			wantErr: ErrInvalidTotalRooms,
		},
	}

	service := NewService(nil, nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.UpdateRoomType(context.Background(), tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("UpdateRoomType() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestDeactivateRoomTypeRejectsInvalidIDBeforeQuerying(t *testing.T) {
	service := NewService(nil, nil)

	for _, roomTypeID := range []int64{0, -1} {
		roomTypeID := roomTypeID
		t.Run(fmt.Sprintf("ID_%d", roomTypeID), func(t *testing.T) {
			err := service.DeactivateRoomType(context.Background(), roomTypeID)
			if !errors.Is(err, ErrInvalidID) {
				t.Fatalf("DeactivateRoomType() error = %v, want ErrInvalidID", err)
			}
		})
	}
}
