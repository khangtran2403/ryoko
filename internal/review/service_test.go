package review

import (
	"context"
	"errors"
	"testing"
)

func TestCreateReviewValidatesInputBeforeQuery(t *testing.T) {
	validComment := "Great stay"
	valid := CreateReview{
		Rating:    5,
		Comment:   &validComment,
		BookingID: 1,
		UserID:    1,
	}

	tests := []struct {
		name    string
		mutate  func(*CreateReview)
		wantErr error
	}{
		{
			name: "invalid user",
			mutate: func(input *CreateReview) {
				input.UserID = 0
			},
			wantErr: ErrBookingNotReviewable,
		},
		{
			name: "invalid booking",
			mutate: func(input *CreateReview) {
				input.BookingID = 0
			},
			wantErr: ErrBookingNotReviewable,
		},
		{
			name: "rating below range",
			mutate: func(input *CreateReview) {
				input.Rating = 0
			},
			wantErr: ErrInvalidRating,
		},
		{
			name: "rating above range",
			mutate: func(input *CreateReview) {
				input.Rating = 6
			},
			wantErr: ErrInvalidRating,
		},
		{
			name: "blank comment",
			mutate: func(input *CreateReview) {
				comment := " \t\n "
				input.Comment = &comment
			},
			wantErr: ErrBlankComment,
		},
	}

	// A nil service is safe here only because all validation must finish
	// before CreateReview attempts to use its query dependency.
	service := &Service{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := valid
			tt.mutate(&input)

			_, err := service.CreateReview(context.Background(), input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CreateReview() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNullableComment(t *testing.T) {
	t.Run("omitted comment becomes SQL NULL", func(t *testing.T) {
		comment, err := nullableComment(nil)
		if err != nil {
			t.Fatalf("nullableComment() error = %v", err)
		}
		if comment.Valid {
			t.Errorf("comment.Valid = true, want false")
		}
	})

	t.Run("comment is trimmed", func(t *testing.T) {
		value := "  Excellent location  "
		comment, err := nullableComment(&value)
		if err != nil {
			t.Fatalf("nullableComment() error = %v", err)
		}
		if !comment.Valid || comment.String != "Excellent location" {
			t.Errorf("comment = %#v, want valid trimmed text", comment)
		}
	})
}

func TestGetReviewByIDRejectsInvalidIDBeforeQuery(t *testing.T) {
	service := &Service{}

	if _, err := service.GetReviewByID(context.Background(), 0); !errors.Is(err, ErrReviewNotFound) {
		t.Errorf("GetReviewByID() error = %v, want ErrReviewNotFound", err)
	}

}

func TestListReviewByHotelValidatesPaginationBeforeQuery(t *testing.T) {
	service := &Service{}
	tests := []struct {
		name     string
		page     int32
		pageSize int32
		wantErr  error
	}{
		{name: "zero page", page: 0, pageSize: DefaultReviewPageSize, wantErr: ErrInvalidPage},
		{name: "negative page", page: -1, pageSize: DefaultReviewPageSize, wantErr: ErrInvalidPage},
		{name: "zero page size", page: DefaultReviewPage, pageSize: 0, wantErr: ErrInvalidPageSize},
		{name: "negative page size", page: DefaultReviewPage, pageSize: -1, wantErr: ErrInvalidPageSize},
		{name: "page size above maximum", page: DefaultReviewPage, pageSize: MaxReviewPageSize + 1, wantErr: ErrInvalidPageSize},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.ListReviewByHotel(context.Background(), ListReviewsByHotelInput{
				HotelID:  12,
				Page:     tt.page,
				PageSize: tt.pageSize,
			})
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ListReviewByHotel() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
