package inventory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

const (
	MaxInventoryRangeDays = 366
	MaxBlockReasonLength  = 500
)

var (
	ErrInvalidRoomTypeID   = errors.New("room type ID must be positive")
	ErrRoomTypeNotFound    = errors.New("room type not found")
	ErrInvalidDateRange    = errors.New("date_to must be after date_from")
	ErrInventoryDateInPast = errors.New("inventory cannot be changed in the past")
	ErrInventoryRangeLarge = errors.New("inventory range cannot exceed 366 days")
	ErrInvalidBlockedRooms = errors.New("rooms_blocked must not be negative")
	ErrInvalidBlockReason  = errors.New("block reason must be between 1 and 500 characters")
	ErrInventoryConflict   = errors.New("blocked rooms would exceed available inventory")
)

type SetBlockedInventoryInput struct {
	RoomTypeID   int64
	DateFrom     time.Time
	DateTo       time.Time
	RoomsBlocked int32
	BlockReason  string
}

type ListInventoryInput struct {
	RoomTypeID int64
	DateFrom   time.Time
	DateTo     time.Time
}
type Service struct {
	pool    *pgxpool.Pool
	queries *sqlc.Queries
	now     func() time.Time
}

func NewService(pool *pgxpool.Pool, queries *sqlc.Queries) *Service {
	return &Service{
		pool:    pool,
		queries: queries,
		now:     time.Now,
	}
}
func (s *Service) SetBlockedInventory(ctx context.Context, input SetBlockedInventoryInput) ([]sqlc.ListRoomTypeInventoryRow, error) {
	var blockReasons string
	blockReasons = strings.TrimSpace(input.BlockReason)
	validateDateFrom, validateDateTo, err := s.validateStayDates(input.DateFrom, input.DateTo)
	if err != nil {
		return nil, err
	}
	if input.RoomTypeID <= 0 {
		return nil, ErrInvalidRoomTypeID
	}
	DateFrom := pgtype.Date{
		Time:  validateDateFrom,
		Valid: true,
	}

	DateTo := pgtype.Date{
		Time:  validateDateTo,
		Valid: true,
	}
	days := int(validateDateTo.Sub(validateDateFrom) / (24 * time.Hour))
	if days > MaxInventoryRangeDays {
		return nil, ErrInventoryRangeLarge
	}
	if input.RoomsBlocked < 0 {
		return nil, ErrInvalidBlockedRooms
	}
	if input.RoomsBlocked == 0 {
		blockReasons = ""
	} else if blockReasons == "" || utf8.RuneCountInString(blockReasons) > MaxBlockReasonLength {
		return nil, ErrInvalidBlockReason
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.ReadCommitted,
	})
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)
	roomType, err := qtx.GetRoomTypeForBooking(ctx, input.RoomTypeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRoomTypeNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get room type: %w", err)
	}
	err = qtx.EnsureAvailabilityRows(ctx, sqlc.EnsureAvailabilityRowsParams{
		CheckIn:    DateFrom,
		CheckOut:   DateTo,
		RoomTypeID: input.RoomTypeID,
	})
	if err != nil {
		return nil, fmt.Errorf("availability rows: %w", err)
	}
	rows, err := qtx.LockAvailabilityRows(ctx, sqlc.LockAvailabilityRowsParams{
		RoomTypeID: input.RoomTypeID,
		CheckIn:    DateFrom,
		CheckOut:   DateTo,
	})
	if err != nil {
		return nil, fmt.Errorf(
			"lock availability rows: %w",
			err,
		)
	}
	if len(rows) != days {
		return nil, fmt.Errorf(
			"availability invariant violated: got %d rows for %d days",
			len(rows),
			days,
		)
	}
	for _, row := range rows {
		if row.RoomsBooked+input.RoomsBlocked > roomType.TotalRooms {
			return nil, ErrInventoryConflict
		}
	}
	affected, err := qtx.SetBlockedInventory(
		ctx,
		sqlc.SetBlockedInventoryParams{
			RoomTypeID:   input.RoomTypeID,
			DateFrom:     DateFrom,
			DateTo:       DateTo,
			RoomsBlocked: input.RoomsBlocked,
			BlockReason:  blockReasons,
			TotalRooms:   roomType.TotalRooms,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("set blocked inventory: %w", err)
	}
	if affected != int64(days) {
		return nil, fmt.Errorf("inventory invariant violated: updated %d rows for %d days", affected, days)
	}
	list, err := qtx.ListRoomTypeInventory(ctx, sqlc.ListRoomTypeInventoryParams{
		RoomTypeID: input.RoomTypeID,
		DateFrom:   DateFrom,
		DateTo:     DateTo,
	})
	if err != nil {
		return nil, fmt.Errorf("list room type inventory : %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit inventory transaction: %w", err)
	}
	return list, nil
}
func (s *Service) ListRoomTypeInventory(ctx context.Context, input ListInventoryInput) ([]sqlc.ListRoomTypeInventoryRow, error) {
	validateDateFrom, validateDateTo, err := s.generalValidateStayDates(input.DateFrom, input.DateTo)
	if err != nil {
		return nil, err
	}
	if input.RoomTypeID <= 0 {
		return nil, ErrInvalidRoomTypeID
	}
	DateFrom := pgtype.Date{
		Time:  validateDateFrom,
		Valid: true,
	}

	DateTo := pgtype.Date{
		Time:  validateDateTo,
		Valid: true,
	}
	getRoomtype, err := s.queries.GetRoomTypeByID(ctx, input.RoomTypeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRoomTypeNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get room type : %w", err)
	}
	list, err := s.queries.ListRoomTypeInventory(ctx, sqlc.ListRoomTypeInventoryParams{
		RoomTypeID: getRoomtype.ID,
		DateFrom:   DateFrom,
		DateTo:     DateTo,
	})
	if err != nil {
		return nil, fmt.Errorf("faile to list room type inventory : %w", err)
	}
	if list == nil {
		list = []sqlc.ListRoomTypeInventoryRow{}
	}
	return list, nil
}
func dateOnlyUTC(value time.Time) time.Time {
	return time.Date(
		value.Year(),
		value.Month(),
		value.Day(),
		0, 0, 0, 0,
		time.UTC,
	)
}
func (s *Service) generalValidateStayDates(
	dateFrom time.Time,
	dateTo time.Time,
) (time.Time, time.Time, error) {
	from := dateOnlyUTC(dateFrom)
	to := dateOnlyUTC(dateTo)

	if !to.After(from) {
		return time.Time{}, time.Time{}, ErrInvalidDateRange
	}

	days := int(to.Sub(from) / (24 * time.Hour))
	if days > MaxInventoryRangeDays {
		return time.Time{}, time.Time{}, ErrInventoryRangeLarge
	}

	return from, to, nil
}

func (s *Service) validateStayDates(
	dateFrom time.Time,
	dateTo time.Time,
) (time.Time, time.Time, error) {
	from, to, err := s.generalValidateStayDates(dateFrom, dateTo)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	if from.Before(dateOnlyUTC(s.now())) {
		return time.Time{}, time.Time{}, ErrInventoryDateInPast
	}

	return from, to, nil
}
