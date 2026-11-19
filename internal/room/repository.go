package room

import (
	"context"
	"errors"
	"fmt"

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

// Create inserts a room type and returns the created record.
func (r *Repository) Create(
	ctx context.Context,
	roomType RoomType,
) (*RoomType, error) {
	query := `
		INSERT INTO room_types (
			hotel_id,
			name,
			description,
			max_occupancy,
			price_per_night_paise
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING
			id,
			hotel_id,
			name,
			description,
			max_occupancy,
			price_per_night_paise,
			created_at,
			updated_at
	`

	created := &RoomType{}

	err := r.pool.QueryRow(
		ctx,
		query,
		roomType.HotelID,
		roomType.Name,
		roomType.Description,
		roomType.MaxOccupancy,
		roomType.PricePerNightPaise,
	).Scan(
		&created.ID,
		&created.HotelID,
		&created.Name,
		&created.Description,
		&created.MaxOccupancy,
		&created.PricePerNightPaise,
		&created.CreatedAt,
		&created.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("create room type: %w", err)
	}

	return created, nil
}

// GetByID retrieves a room type by its ID.
func (r *Repository) GetByID(
	ctx context.Context,
	id string,
) (*RoomType, error) {
	query := `
		SELECT
			id,
			hotel_id,
			name,
			description,
			max_occupancy,
			price_per_night_paise,
			created_at,
			updated_at
		FROM room_types
		WHERE id = $1
	`

	found := &RoomType{}

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&found.ID,
		&found.HotelID,
		&found.Name,
		&found.Description,
		&found.MaxOccupancy,
		&found.PricePerNightPaise,
		&found.CreatedAt,
		&found.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
		}

		return nil, fmt.Errorf("get room type by ID: %w", err)
	}

	return found, nil
}

// ListByHotelID retrieves all room types belonging to a hotel.
func (r *Repository) ListByHotelID(
	ctx context.Context,
	hotelID string,
) ([]RoomType, error) {
	var hotelExists bool

	err := r.pool.QueryRow(
		ctx,
		`SELECT EXISTS (
        SELECT 1 FROM hotels WHERE id = $1
    )`,
		hotelID,
	).Scan(&hotelExists)

	if err != nil {
		return nil, fmt.Errorf("check hotel existence: %w", err)
	}

	if !hotelExists {
		return nil, ErrHotelNotFound
	}

	query := `
		SELECT
			id,
			hotel_id,
			name,
			description,
			max_occupancy,
			price_per_night_paise,
			created_at,
			updated_at
		FROM room_types
		WHERE hotel_id = $1
		ORDER BY created_at DESC, id DESC
	`

	rows, err := r.pool.Query(ctx, query, hotelID)
	if err != nil {
		return nil, fmt.Errorf("list room types by hotel ID: %w", err)
	}
	defer rows.Close()

	roomTypes := make([]RoomType, 0)

	for rows.Next() {
		var roomType RoomType

		if err := rows.Scan(
			&roomType.ID,
			&roomType.HotelID,
			&roomType.Name,
			&roomType.Description,
			&roomType.MaxOccupancy,
			&roomType.PricePerNightPaise,
			&roomType.CreatedAt,
			&roomType.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan room type: %w", err)
		}

		roomTypes = append(roomTypes, roomType)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate room types: %w", err)
	}

	return roomTypes, nil
}

// Update replaces a room type's editable details.
func (r *Repository) Update(
	ctx context.Context,
	id string,
	roomType RoomType,
) (*RoomType, error) {
	query := `
		UPDATE room_types
		SET
			name = $1,
			description = $2,
			max_occupancy = $3,
			price_per_night_paise = $4,
			updated_at = NOW()
		WHERE id = $5
		RETURNING
			id,
			hotel_id,
			name,
			description,
			max_occupancy,
			price_per_night_paise,
			created_at,
			updated_at
	`

	updated := &RoomType{}

	err := r.pool.QueryRow(
		ctx,
		query,
		roomType.Name,
		roomType.Description,
		roomType.MaxOccupancy,
		roomType.PricePerNightPaise,
		id,
	).Scan(
		&updated.ID,
		&updated.HotelID,
		&updated.Name,
		&updated.Description,
		&updated.MaxOccupancy,
		&updated.PricePerNightPaise,
		&updated.CreatedAt,
		&updated.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
		}

		return nil, fmt.Errorf("update room type: %w", err)
	}

	return updated, nil
}
