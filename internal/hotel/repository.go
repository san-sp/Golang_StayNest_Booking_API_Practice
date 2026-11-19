package hotel

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

// Create inserts a new hotel and returns the created record.
func (r *Repository) Create(
	ctx context.Context,
	hotel Hotel,
) (*Hotel, error) {
	query := `
		INSERT INTO hotels (
			name,
			description,
			address,
			city,
			country,
			timezone
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING
			id,
			name,
			description,
			address,
			city,
			country,
			timezone,
			created_at,
			updated_at
	`

	created := &Hotel{}

	err := r.pool.QueryRow(
		ctx,
		query,
		hotel.Name,
		hotel.Description,
		hotel.Address,
		hotel.City,
		hotel.Country,
		hotel.Timezone,
	).Scan(
		&created.ID,
		&created.Name,
		&created.Description,
		&created.Address,
		&created.City,
		&created.Country,
		&created.Timezone,
		&created.CreatedAt,
		&created.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("create hotel: %w", err)
	}

	return created, nil
}

// GetByID retrieves a hotel by its ID.
func (r *Repository) GetByID(
	ctx context.Context,
	id string,
) (*Hotel, error) {
	query := `
		SELECT
			id,
			name,
			description,
			address,
			city,
			country,
			timezone,
			created_at,
			updated_at
		FROM hotels
		WHERE id = $1
	`

	found := &Hotel{}

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&found.ID,
		&found.Name,
		&found.Description,
		&found.Address,
		&found.City,
		&found.Country,
		&found.Timezone,
		&found.CreatedAt,
		&found.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
		}

		return nil, fmt.Errorf("get hotel by ID: %w", err)
	}

	return found, nil
}

// Update replaces a hotel's editable details and returns the updated record.
func (r *Repository) Update(
	ctx context.Context,
	id string,
	hotel Hotel,
) (*Hotel, error) {
	query := `
		UPDATE hotels
		SET
			name = $1,
			description = $2,
			address = $3,
			city = $4,
			country = $5,
			timezone = $6,
			updated_at = NOW()
		WHERE id = $7
		RETURNING
			id,
			name,
			description,
			address,
			city,
			country,
			timezone,
			created_at,
			updated_at
	`

	updated := &Hotel{}

	err := r.pool.QueryRow(
		ctx,
		query,
		hotel.Name,
		hotel.Description,
		hotel.Address,
		hotel.City,
		hotel.Country,
		hotel.Timezone,
		id,
	).Scan(
		&updated.ID,
		&updated.Name,
		&updated.Description,
		&updated.Address,
		&updated.City,
		&updated.Country,
		&updated.Timezone,
		&updated.CreatedAt,
		&updated.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
		}

		return nil, fmt.Errorf("update hotel: %w", err)
	}

	return updated, nil
}

// List retrieves hotels with optional city filtering and pagination.

func (r *Repository) List(
	ctx context.Context,
	params ListParams,
) (*ListResult, error) {
	result := &ListResult{
		Hotels: make([]Hotel, 0),
		Limit:  params.Limit,
		Offset: params.Offset,
	}

	countQuery := `
		SELECT COUNT(*)
		FROM hotels
	`

	listQuery := `
		SELECT
			id,
			name,
			description,
			address,
			city,
			country,
			timezone,
			created_at,
			updated_at
		FROM hotels
	`

	var countArgs []any
	var listArgs []any

	if params.City != "" {
		countQuery += ` WHERE LOWER(city) = LOWER($1)`
		listQuery += ` WHERE LOWER(city) = LOWER($1)`

		countArgs = append(countArgs, params.City)
		listArgs = append(listArgs, params.City)
	}

	listQuery += `
		ORDER BY created_at DESC, id DESC
	`

	listArgs = append(listArgs, params.Limit)
	listArgs = append(listArgs, params.Offset)

	listQuery += `
		LIMIT $` + fmt.Sprint(len(listArgs)-1) +
		` OFFSET $` + fmt.Sprint(len(listArgs))

	err := r.pool.QueryRow(
		ctx,
		countQuery,
		countArgs...,
	).Scan(&result.Total)
	if err != nil {
		return nil, fmt.Errorf("count hotels: %w", err)
	}

	rows, err := r.pool.Query(
		ctx,
		listQuery,
		listArgs...,
	)
	if err != nil {
		return nil, fmt.Errorf("list hotels: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var h Hotel

		if err := rows.Scan(
			&h.ID,
			&h.Name,
			&h.Description,
			&h.Address,
			&h.City,
			&h.Country,
			&h.Timezone,
			&h.CreatedAt,
			&h.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan hotel: %w", err)
		}

		result.Hotels = append(result.Hotels, h)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate hotels: %w", err)
	}

	return result, nil
}

// Search finds hotels with room types that have sufficient
// inventory for every night of the requested stay.
func (r *Repository) Search(
	ctx context.Context,
	params SearchParams,
) ([]SearchResult, error) {
	query := `
		SELECT
			h.id,
			h.name,
			h.description,
			h.address,
			h.city,
			h.country,
			h.timezone,
			h.created_at,
			h.updated_at,

			rt.id,
			rt.name,
			rt.description,
			rt.max_occupancy,
			rt.price_per_night_paise,

			MIN(ri.available_rooms) AS available_rooms

		FROM hotels h

		JOIN room_types rt
			ON rt.hotel_id = h.id

		JOIN room_inventory ri
			ON ri.room_type_id = rt.id
			AND ri.stay_date >= $2::date
			AND ri.stay_date < $3::date

		WHERE LOWER(h.city) = LOWER($1)
			AND rt.max_occupancy::BIGINT * $5::BIGINT >= $4::BIGINT

		GROUP BY
			h.id,
			h.name,
			h.description,
			h.address,
			h.city,
			h.country,
			h.timezone,
			h.created_at,
			h.updated_at,
			rt.id,
			rt.name,
			rt.description,
			rt.max_occupancy,
			rt.price_per_night_paise

		HAVING
			COUNT(ri.stay_date) = ($3::date - $2::date)
			AND MIN(ri.available_rooms) >= $5

		ORDER BY
			h.created_at DESC,
			h.id DESC,
			rt.name ASC,
			rt.id ASC
	`

	rows, err := r.pool.Query(
		ctx,
		query,
		params.City,
		params.CheckIn,
		params.CheckOut,
		params.Guests,
		params.Rooms,
	)
	if err != nil {
		return nil, fmt.Errorf("search available hotels: %w", err)
	}
	defer rows.Close()

	results := make([]SearchResult, 0)
	hotelIndexes := make(map[string]int)

	for rows.Next() {
		var h Hotel
		var room AvailableRoom

		if err := rows.Scan(
			&h.ID,
			&h.Name,
			&h.Description,
			&h.Address,
			&h.City,
			&h.Country,
			&h.Timezone,
			&h.CreatedAt,
			&h.UpdatedAt,
			&room.ID,
			&room.Name,
			&room.Description,
			&room.MaxOccupancy,
			&room.PricePerNightPaise,
			&room.AvailableRooms,
		); err != nil {
			return nil, fmt.Errorf("scan hotel search result: %w", err)
		}

		index, exists := hotelIndexes[h.ID]
		if !exists {
			index = len(results)
			hotelIndexes[h.ID] = index

			results = append(results, SearchResult{
				Hotel: h,
				Rooms: make([]AvailableRoom, 0),
			})
		}

		results[index].Rooms = append(
			results[index].Rooms,
			room,
		)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate hotel search results: %w", err)
	}

	return results, nil
}
