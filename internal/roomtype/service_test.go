package roomtype

import (
	"context"
	"errors"
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
