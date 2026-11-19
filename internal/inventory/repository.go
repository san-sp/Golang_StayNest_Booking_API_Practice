package inventory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{
		pool: pool,
	}
}

// SetDailyCapacity creates or updates inventory while preserving
// the number of rooms already booked.
func (r *Repository) SetDailyCapacity(
	ctx context.Context,
	roomTypeID string,
	stayDate time.Time,
	totalRooms int,
) (*DailyInventory, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin inventory transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	// Lock the room type to ensure it exists and prevent its deletion
	// while inventory is being created or updated.
	var existingRoomTypeID string

	err = tx.QueryRow(
		ctx,
		`SELECT id FROM room_types WHERE id = $1 FOR KEY SHARE`,
		roomTypeID,
	).Scan(&existingRoomTypeID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRoomTypeNotFound
		}

		return nil, fmt.Errorf("check room type: %w", err)
	}

	query := `
		INSERT INTO room_inventory (
			room_type_id,
			stay_date,
			total_rooms,
			available_rooms
		)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (room_type_id, stay_date)
		DO UPDATE SET
			total_rooms = EXCLUDED.total_rooms,
			available_rooms =
				room_inventory.available_rooms
				+ (EXCLUDED.total_rooms - room_inventory.total_rooms),
			updated_at = NOW()
		WHERE
			room_inventory.available_rooms
			+ (EXCLUDED.total_rooms - room_inventory.total_rooms) >= 0
		RETURNING
			room_type_id,
			stay_date,
			total_rooms,
			available_rooms,
			updated_at
	`

	inventory := &DailyInventory{}

	err = tx.QueryRow(
		ctx,
		query,
		roomTypeID,
		stayDate,
		totalRooms,
	).Scan(
		&inventory.RoomTypeID,
		&inventory.StayDate,
		&inventory.TotalRooms,
		&inventory.AvailableRooms,
		&inventory.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCapacityBelowBooked
		}

		return nil, fmt.Errorf("upsert daily inventory: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit inventory transaction: %w", err)
	}

	return inventory, nil
}

// ListByRoomTypeAndDateRange retrieves configured inventory records.
// The start date is inclusive and the end date is exclusive.
func (r *Repository) ListByRoomTypeAndDateRange(
	ctx context.Context,
	roomTypeID string,
	startDate time.Time,
	endDate time.Time,
) ([]DailyInventory, error) {
	var roomTypeExists bool

	err := r.pool.QueryRow(
		ctx,
		`SELECT EXISTS (
			SELECT 1 FROM room_types WHERE id = $1
		)`,
		roomTypeID,
	).Scan(&roomTypeExists)

	if err != nil {
		return nil, fmt.Errorf("check room type existence: %w", err)
	}

	if !roomTypeExists {
		return nil, ErrRoomTypeNotFound
	}

	query := `
		SELECT
			room_type_id,
			stay_date,
			total_rooms,
			available_rooms,
			updated_at
		FROM room_inventory
		WHERE room_type_id = $1
			AND stay_date >= $2
			AND stay_date < $3
		ORDER BY stay_date ASC
	`

	rows, err := r.pool.Query(
		ctx,
		query,
		roomTypeID,
		startDate,
		endDate,
	)
	if err != nil {
		return nil, fmt.Errorf("list daily inventory: %w", err)
	}
	defer rows.Close()

	inventory := make([]DailyInventory, 0)

	for rows.Next() {
		var daily DailyInventory

		if err := rows.Scan(
			&daily.RoomTypeID,
			&daily.StayDate,
			&daily.TotalRooms,
			&daily.AvailableRooms,
			&daily.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan daily inventory: %w", err)
		}

		inventory = append(inventory, daily)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate daily inventory: %w", err)
	}

	return inventory, nil
}
