package roomtype

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

var (
	ErrInvalidID         = errors.New("ID must be positive")
	ErrRoomTypeNotFound  = errors.New("room type not found")
	ErrInvalidTotalRooms = errors.New("total rooms must be positive")
	ErrInventoryConflict = errors.New("total rooms cannot be less than booked and blocked rooms")
)

type UpdateRoomTypeRequest struct {
	ID            int64          `json:"id"`
	Name          string         `json:"name"`
	Description   pgtype.Text    `json:"description"`
	PricePerNight pgtype.Numeric `json:"price_per_night"`
	Capacity      int32          `json:"capacity"`
	TotalRooms    int32          `json:"total_rooms"`
}

type Service struct {
	pool    *pgxpool.Pool
	queries *sqlc.Queries
}

func NewService(pool *pgxpool.Pool, queries *sqlc.Queries) *Service {
	return &Service{
		pool:    pool,
		queries: queries,
	}
}

func (s *Service) UpdateRoomType(ctx context.Context, input UpdateRoomTypeRequest) (sqlc.RoomType, error) {
	if input.ID <= 0 {
		return sqlc.RoomType{}, ErrInvalidID
	}
	if input.TotalRooms <= 0 {
		return sqlc.RoomType{}, ErrInvalidTotalRooms
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.ReadCommitted,
	})
	if err != nil {
		return sqlc.RoomType{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)

	_, err = qtx.GetRoomTypeForUpdate(ctx, input.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.RoomType{}, ErrRoomTypeNotFound
	}
	if err != nil {
		return sqlc.RoomType{}, fmt.Errorf("failed to lock row:%w", err)
	}
	getMax, err := qtx.GetMaxRoomTypeInventoryUsage(ctx, input.ID)
	if err != nil {
		return sqlc.RoomType{}, fmt.Errorf("failed to get max inventory usage:%w", err)
	}
	if input.TotalRooms < getMax {
		return sqlc.RoomType{}, ErrInventoryConflict
	}
	update, err := qtx.UpdateRoomType(ctx, sqlc.UpdateRoomTypeParams{
		ID:            input.ID,
		Name:          input.Name,
		Description:   input.Description,
		PricePerNight: input.PricePerNight,
		Capacity:      input.Capacity,
		TotalRooms:    input.TotalRooms,
	})
	if err != nil {
		return sqlc.RoomType{}, fmt.Errorf("update room type:%w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sqlc.RoomType{}, fmt.Errorf(
			"commit transaction: %w",
			err,
		)
	}
	return update, nil
}
