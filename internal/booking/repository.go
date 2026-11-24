package booking

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return NewRepositoryWithClock(pool, time.Now)
}

func NewRepositoryWithClock(
	pool *pgxpool.Pool,
	now func() time.Time,
) *Repository {
	if now == nil {
		now = time.Now
	}

	return &Repository{
		pool: pool,
		now:  now,
	}
}

func hotelLocalToday(
	now time.Time,
	timezone string,
) (time.Time, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"load hotel timezone %q: %w",
			timezone,
			err,
		)
	}

	localNow := now.In(location)

	year, month, day := localNow.Date()

	// Store the calendar date as UTC midnight.
	// This matches the existing dateOnly convention.
	return time.Date(
		year,
		month,
		day,
		0,
		0,
		0,
		0,
		time.UTC,
	), nil
}

// Create reserves inventory and creates a pending-payment booking atomically.
func (r *Repository) Create(
	ctx context.Context,
	booking Booking,
) (*Booking, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin booking transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	// Lock the room type while reading its current occupancy and price.
	// This prevents those values changing during this booking transaction.
	var maxOccupancy int
	var pricePerNightPaise int64
	var timezone string

	err = tx.QueryRow(
		ctx,
		`
    SELECT
        rt.max_occupancy,
        rt.price_per_night_paise,
        h.timezone
    FROM room_types rt
    JOIN hotels h ON h.id = rt.hotel_id
    WHERE rt.id = $1
    FOR SHARE OF rt, h
    `,
		booking.RoomTypeID,
	).Scan(
		&maxOccupancy,
		&pricePerNightPaise,
		&timezone,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRoomTypeNotFound
		}

		return nil, fmt.Errorf("get room type details: %w", err)
	}

	today, err := hotelLocalToday(r.now(), timezone)
	if err != nil {
		return nil, err
	}

	if dateOnly(booking.CheckIn).Before(today) {
		return nil, ErrBookingDateInPast
	}

	// Occupancy is a per-room limit.
	if exceedsGuestCapacity(
		maxOccupancy,
		booking.RoomsCount,
		booking.GuestsCount,
	) {
		return nil, ErrGuestCapacityExceeded
	}

	// Check-out is exclusive, so this calculates the number of nights.
	nights := int64(booking.CheckOut.Sub(booking.CheckIn) / (24 * time.Hour))

	if nights <= 0 {
		return nil, fmt.Errorf("invalid booking night count: %d", nights)
	}

	totalAmount, err := calculateTotalAmount(
		pricePerNightPaise,
		int64(booking.RoomsCount),
		nights,
	)
	if err != nil {
		return nil, err
	}

	// Lock every inventory row in ascending date order.
	// Competing booking transactions must wait for these locks.
	rows, err := tx.Query(
		ctx,
		`
    SELECT
        stay_date,
        available_rooms
    FROM room_inventory
    WHERE room_type_id = $1
        AND stay_date >= $2
        AND stay_date < $3
    ORDER BY stay_date ASC
    FOR UPDATE
    `,
		booking.RoomTypeID,
		booking.CheckIn,
		booking.CheckOut,
	)
	if err != nil {
		return nil, fmt.Errorf("lock booking inventory: %w", err)
	}

	inventory := make([]lockedInventory, 0, int(nights))

	for rows.Next() {
		var daily lockedInventory

		if err := rows.Scan(
			&daily.StayDate,
			&daily.AvailableRooms,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan booking inventory: %w", err)
		}

		inventory = append(inventory, daily)
	}

	rowsErr := rows.Err()
	rows.Close()

	if rowsErr != nil {
		return nil, fmt.Errorf("iterate booking inventory: %w", rowsErr)
	}

	// Every requested night must have a configured inventory row.
	if int64(len(inventory)) != nights {
		return nil, ErrInventoryNotConfigured
	}

	// Validate all nights before modifying any inventory.
	for _, daily := range inventory {
		if daily.AvailableRooms < booking.RoomsCount {
			return nil, ErrInsufficientAvailability
		}
	}

	// Decrement each locked inventory row.
	for _, daily := range inventory {
		tag, err := tx.Exec(
			ctx,
			`
			UPDATE room_inventory
			SET available_rooms = available_rooms - $3,
				updated_at = NOW()
			WHERE room_type_id = $1
				AND stay_date = $2
				AND available_rooms >= $3
			`,
			booking.RoomTypeID,
			daily.StayDate,
			booking.RoomsCount,
		)
		if err != nil {
			return nil, fmt.Errorf("decrement booking inventory: %w", err)
		}

		if tag.RowsAffected() != 1 {
			return nil, ErrInsufficientAvailability
		}
	}

	// Price is read from the database, never supplied by the customer.
	booking.PricePerNightPaise = pricePerNightPaise
	booking.TotalAmountPaise = totalAmount

	created := &Booking{}

	err = tx.QueryRow(
		ctx,
		`
		INSERT INTO bookings (
    booking_reference,
    customer_id,
    room_type_id,
    check_in,
    check_out,
    rooms_count,
    guests_count,
    price_per_night_paise,
    total_amount_paise,
    status,
    payment_expires_at
	)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	RETURNING
    id::text,
    booking_reference,
    customer_id::text,
    room_type_id::text,
    check_in,
    check_out,
    rooms_count,
    guests_count,
    price_per_night_paise,
    total_amount_paise,
    status,
    payment_expires_at,
    expired_at,
    created_at,
    updated_at
		`,
		booking.BookingReference,
		booking.CustomerID,
		booking.RoomTypeID,
		booking.CheckIn,
		booking.CheckOut,
		booking.RoomsCount,
		booking.GuestsCount,
		booking.PricePerNightPaise,
		booking.TotalAmountPaise,
		booking.Status,
		booking.PaymentExpiresAt,
	).Scan(
		&created.ID,
		&created.BookingReference,
		&created.CustomerID,
		&created.RoomTypeID,
		&created.CheckIn,
		&created.CheckOut,
		&created.RoomsCount,
		&created.GuestsCount,
		&created.PricePerNightPaise,
		&created.TotalAmountPaise,
		&created.Status,
		&created.PaymentExpiresAt,
		&created.ExpiredAt,
		&created.CreatedAt,
		&created.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert booking: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit booking transaction: %w", err)
	}

	return created, nil
}

type lockedInventory struct {
	StayDate       time.Time
	TotalRooms     int
	AvailableRooms int
}

// Modify updates a confirmed booking and its inventory atomically.
func (r *Repository) Modify(
	ctx context.Context,
	customerID string,
	bookingID string,
	input ModifyInput,
) (*Booking, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin booking modification transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	// Lock the booking and verify customer ownership.
	modified := &Booking{}

	err = tx.QueryRow(
		ctx,
		`
		SELECT
			id::text,
			booking_reference,
			customer_id::text,
			room_type_id::text,
			check_in,
			check_out,
			rooms_count,
			guests_count,
			price_per_night_paise,
			total_amount_paise,
			status,
			payment_expires_at,
			expired_at,
			created_at,
			updated_at,
			cancelled_at
		FROM bookings
		WHERE id = $1
		  AND customer_id = $2
		FOR UPDATE
		`,
		bookingID,
		customerID,
	).Scan(
		&modified.ID,
		&modified.BookingReference,
		&modified.CustomerID,
		&modified.RoomTypeID,
		&modified.CheckIn,
		&modified.CheckOut,
		&modified.RoomsCount,
		&modified.GuestsCount,
		&modified.PricePerNightPaise,
		&modified.TotalAmountPaise,
		&modified.Status,
		&modified.PaymentExpiresAt,
		&modified.ExpiredAt,
		&modified.CreatedAt,
		&modified.UpdatedAt,
		&modified.CancelledAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBookingNotFound
		}

		return nil, fmt.Errorf("lock booking for modification: %w", err)
	}

	if modified.Status != StatusConfirmed {
		return nil, ErrBookingNotModifiable
	}

	// Lock the room type while reading its current price and capacity.
	var maxOccupancy int
	var pricePerNightPaise int64
	var timezone string

	err = tx.QueryRow(
		ctx,
		`
    SELECT
        rt.max_occupancy,
        rt.price_per_night_paise,
        h.timezone
    FROM room_types rt
    JOIN hotels h ON h.id = rt.hotel_id
    WHERE rt.id = $1
    FOR SHARE OF rt, h
    `,
		modified.RoomTypeID,
	).Scan(
		&maxOccupancy,
		&pricePerNightPaise,
		&timezone,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRoomTypeNotFound
		}

		return nil, fmt.Errorf("get room type details: %w", err)
	}

	today, err := hotelLocalToday(r.now(), timezone)
	if err != nil {
		return nil, err
	}

	// A booking that starts today or earlier is treated as
	// already started because we only store calendar dates.
	if !dateOnly(modified.CheckIn).After(today) {
		return nil, ErrBookingNotModifiable
	}

	if dateOnly(input.CheckIn).Before(today) {
		return nil, ErrBookingDateInPast
	}

	// Validate the new guest capacity.
	if exceedsGuestCapacity(
		maxOccupancy,
		input.RoomsCount,
		input.GuestsCount,
	) {
		return nil, ErrGuestCapacityExceeded
	}

	oldCheckIn := dateOnly(modified.CheckIn)
	oldCheckOut := dateOnly(modified.CheckOut)

	newCheckIn := dateOnly(input.CheckIn)
	newCheckOut := dateOnly(input.CheckOut)

	oldNights := int64(oldCheckOut.Sub(oldCheckIn) / (24 * time.Hour))
	newNights := int64(newCheckOut.Sub(newCheckIn) / (24 * time.Hour))

	if oldNights <= 0 || newNights <= 0 {
		return nil, ErrBookingInventoryInconsistent
	}

	totalAmount, err := calculateTotalAmount(
		pricePerNightPaise,
		int64(input.RoomsCount),
		newNights,
	)
	if err != nil {
		return nil, err
	}

	// Lock every inventory row in the union of the old and new
	// date ranges, always in ascending date order.
	rows, err := tx.Query(
		ctx,
		`
		SELECT
			stay_date,
			total_rooms,
			available_rooms
		FROM room_inventory
		WHERE room_type_id = $1
		  AND (
				(stay_date >= $2 AND stay_date < $3)
				OR
				(stay_date >= $4 AND stay_date < $5)
		  )
		ORDER BY stay_date ASC
		FOR UPDATE
		`,
		modified.RoomTypeID,
		oldCheckIn,
		oldCheckOut,
		newCheckIn,
		newCheckOut,
	)
	if err != nil {
		return nil, fmt.Errorf("lock modification inventory: %w", err)
	}

	inventory := make([]lockedInventory, 0)

	for rows.Next() {
		var daily lockedInventory

		if err := rows.Scan(
			&daily.StayDate,
			&daily.TotalRooms,
			&daily.AvailableRooms,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan modification inventory: %w", err)
		}

		inventory = append(inventory, daily)
	}

	rowsErr := rows.Err()
	rows.Close()

	if rowsErr != nil {
		return nil, fmt.Errorf("iterate modification inventory: %w", rowsErr)
	}

	// Verify that all original booking nights have inventory rows.
	oldInventoryCount := int64(0)
	newInventoryCount := int64(0)

	for _, daily := range inventory {
		stayDate := dateOnly(daily.StayDate)

		if !stayDate.Before(oldCheckIn) && stayDate.Before(oldCheckOut) {
			oldInventoryCount++
		}

		if !stayDate.Before(newCheckIn) && stayDate.Before(newCheckOut) {
			newInventoryCount++
		}
	}

	if oldInventoryCount != oldNights {
		return nil, ErrBookingInventoryInconsistent
	}

	if newInventoryCount != newNights {
		return nil, ErrInventoryNotConfigured
	}

	// Calculate every new availability value before changing
	// any inventory rows.
	type inventoryUpdate struct {
		StayDate       time.Time
		AvailableRooms int
	}

	updates := make([]inventoryUpdate, 0, len(inventory))

	for _, daily := range inventory {
		stayDate := dateOnly(daily.StayDate)
		newAvailable := daily.AvailableRooms

		isOldDate := !stayDate.Before(oldCheckIn) &&
			stayDate.Before(oldCheckOut)

		isNewDate := !stayDate.Before(newCheckIn) &&
			stayDate.Before(newCheckOut)

		// Release the rooms from the original booking.
		if isOldDate {
			newAvailable += modified.RoomsCount
		}

		// Reserve the rooms for the modified booking.
		if isNewDate {
			newAvailable -= input.RoomsCount
		}

		if newAvailable < 0 {
			return nil, ErrInsufficientAvailability
		}

		if newAvailable > daily.TotalRooms {
			return nil, ErrBookingInventoryInconsistent
		}

		updates = append(updates, inventoryUpdate{
			StayDate:       stayDate,
			AvailableRooms: newAvailable,
		})
	}

	// Apply inventory changes only after all dates have passed validation.
	for _, update := range updates {
		tag, err := tx.Exec(
			ctx,
			`
			UPDATE room_inventory
			SET available_rooms = $1,
			    updated_at = NOW()
			WHERE room_type_id = $2
			  AND stay_date = $3
			`,
			update.AvailableRooms,
			modified.RoomTypeID,
			update.StayDate,
		)
		if err != nil {
			return nil, fmt.Errorf("update modification inventory: %w", err)
		}

		if tag.RowsAffected() != 1 {
			return nil, ErrBookingInventoryInconsistent
		}
	}

	// Update the booking using the current room type price.
	err = tx.QueryRow(
		ctx,
		`
		UPDATE bookings
		SET check_in = $1,
		    check_out = $2,
		    rooms_count = $3,
		    guests_count = $4,
		    price_per_night_paise = $5,
		    total_amount_paise = $6,
		    updated_at = NOW()
		WHERE id = $7
		  AND customer_id = $8
		  AND status = $9
		RETURNING
		    check_in,
		    check_out,
		    rooms_count,
		    guests_count,
		    price_per_night_paise,
		    total_amount_paise,
		    updated_at
		`,
		newCheckIn,
		newCheckOut,
		input.RoomsCount,
		input.GuestsCount,
		pricePerNightPaise,
		totalAmount,
		modified.ID,
		customerID,
		StatusConfirmed,
	).Scan(
		&modified.CheckIn,
		&modified.CheckOut,
		&modified.RoomsCount,
		&modified.GuestsCount,
		&modified.PricePerNightPaise,
		&modified.TotalAmountPaise,
		&modified.UpdatedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("update booking details: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit booking modification: %w", err)
	}

	return modified, nil
}

// Cancel restores reserved inventory and cancels the booking atomically.
// Repeated cancellation is safe: inventory is restored only once.
func (r *Repository) Cancel(
	ctx context.Context,
	customerID string,
	bookingID string,
) (*Booking, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin cancellation transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	cancelled := &Booking{}

	// Lock the booking and verify ownership in the same query.
	// Concurrent cancellation requests for this booking must wait.
	err = tx.QueryRow(
		ctx,
		`
        SELECT
            id::text,
            booking_reference,
            customer_id::text,
            room_type_id::text,
            check_in,
            check_out,
            rooms_count,
            guests_count,
            price_per_night_paise,
            total_amount_paise,
            status,
						payment_expires_at,
						expired_at,
            created_at,
            updated_at,
            cancelled_at
        FROM bookings
        WHERE id = $1
          AND customer_id = $2
        FOR UPDATE
        `,
		bookingID,
		customerID,
	).Scan(
		&cancelled.ID,
		&cancelled.BookingReference,
		&cancelled.CustomerID,
		&cancelled.RoomTypeID,
		&cancelled.CheckIn,
		&cancelled.CheckOut,
		&cancelled.RoomsCount,
		&cancelled.GuestsCount,
		&cancelled.PricePerNightPaise,
		&cancelled.TotalAmountPaise,
		&cancelled.Status,
		&cancelled.PaymentExpiresAt,
		&cancelled.ExpiredAt,
		&cancelled.CreatedAt,
		&cancelled.UpdatedAt,
		&cancelled.CancelledAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBookingNotFound
		}

		return nil, fmt.Errorf("lock booking for cancellation: %w", err)
	}

	// Idempotency: a repeated cancellation must not restore inventory again.
	if cancelled.Status == StatusCancelled {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit repeated cancellation: %w", err)
		}

		return cancelled, nil
	}

	if cancelled.Status != StatusConfirmed &&
		cancelled.Status != StatusPendingPayment {
		return nil, fmt.Errorf(
			"cannot cancel booking with status %q",
			cancelled.Status,
		)
	}

	nights := int64(
		cancelled.CheckOut.Sub(cancelled.CheckIn) / (24 * time.Hour),
	)

	if nights <= 0 {
		return nil, ErrBookingInventoryInconsistent
	}

	// Lock all inventory rows in ascending date order.
	rows, err := tx.Query(
		ctx,
		`
        SELECT stay_date
        FROM room_inventory
        WHERE room_type_id = $1
          AND stay_date >= $2
          AND stay_date < $3
        ORDER BY stay_date ASC
        FOR UPDATE
        `,
		cancelled.RoomTypeID,
		cancelled.CheckIn,
		cancelled.CheckOut,
	)
	if err != nil {
		return nil, fmt.Errorf("lock cancellation inventory: %w", err)
	}

	dates := make([]time.Time, 0, int(nights))

	for rows.Next() {
		var stayDate time.Time

		if err := rows.Scan(&stayDate); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan cancellation inventory: %w", err)
		}

		dates = append(dates, stayDate)
	}

	rowsErr := rows.Err()
	rows.Close()

	if rowsErr != nil {
		return nil, fmt.Errorf("iterate cancellation inventory: %w", rowsErr)
	}

	if int64(len(dates)) != nights {
		return nil, ErrBookingInventoryInconsistent
	}

	// Restore every reserved night.
	for _, stayDate := range dates {
		tag, err := tx.Exec(
			ctx,
			`
            UPDATE room_inventory
            SET available_rooms = available_rooms + $3,
                updated_at = NOW()
            WHERE room_type_id = $1
              AND stay_date = $2
              AND available_rooms + $3 <= total_rooms
            `,
			cancelled.RoomTypeID,
			stayDate,
			cancelled.RoomsCount,
		)
		if err != nil {
			return nil, fmt.Errorf("restore cancellation inventory: %w", err)
		}

		if tag.RowsAffected() != 1 {
			return nil, ErrBookingInventoryInconsistent
		}
	}

	// Mark the booking cancelled in the same transaction.
	err = tx.QueryRow(
		ctx,
		`
        UPDATE bookings
        SET status = $1,
            cancelled_at = NOW(),
            updated_at = NOW()
        WHERE id = $2
        RETURNING updated_at, cancelled_at
        `,
		StatusCancelled,
		cancelled.ID,
	).Scan(
		&cancelled.UpdatedAt,
		&cancelled.CancelledAt,
	)
	if err != nil {
		return nil, fmt.Errorf("update booking cancellation: %w", err)
	}

	cancelled.Status = StatusCancelled

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit cancellation transaction: %w", err)
	}

	return cancelled, nil
}

// GetByID retrieves a booking belonging to the specified customer.
func (r *Repository) GetByID(
	ctx context.Context,
	customerID string,
	bookingID string,
) (*Booking, error) {
	found := &Booking{}

	err := r.pool.QueryRow(
		ctx,
		`
        SELECT
            id::text,
            booking_reference,
            customer_id::text,
            room_type_id::text,
            check_in,
            check_out,
            rooms_count,
            guests_count,
            price_per_night_paise,
            total_amount_paise,
            status,
						payment_expires_at,
						expired_at,
            created_at,
            updated_at,
            cancelled_at
        FROM bookings
        WHERE id = $1
          AND customer_id = $2
        `,
		bookingID,
		customerID,
	).Scan(
		&found.ID,
		&found.BookingReference,
		&found.CustomerID,
		&found.RoomTypeID,
		&found.CheckIn,
		&found.CheckOut,
		&found.RoomsCount,
		&found.GuestsCount,
		&found.PricePerNightPaise,
		&found.TotalAmountPaise,
		&found.Status,
		&found.PaymentExpiresAt,
		&found.ExpiredAt,
		&found.CreatedAt,
		&found.UpdatedAt,
		&found.CancelledAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBookingNotFound
		}

		return nil, fmt.Errorf("get booking by ID: %w", err)
	}

	return found, nil
}

// ListByCustomerID retrieves filtered and paginated bookings
// belonging to a customer.
func (r *Repository) ListByCustomerID(
	ctx context.Context,
	customerID string,
	filter ListFilter,
) (ListResult, error) {
	result := ListResult{
		Bookings: make([]Booking, 0),
		Limit:    filter.Limit,
		Offset:   filter.Offset,
	}

	// Shared conditions ensure the count and result queries
	// apply exactly the same filters.
	const whereClause = `
        FROM bookings
        WHERE customer_id = $1
          AND ($2 = '' OR status = $2)
          AND ($3::date IS NULL OR check_in >= $3::date)
          AND ($4::date IS NULL OR check_in <= $4::date)
          AND ($5::date IS NULL OR check_out >= $5::date)
          AND ($6::date IS NULL OR check_out <= $6::date)
    `

	// Count all matching bookings before pagination.
	countQuery := `SELECT COUNT(*) ` + whereClause

	err := r.pool.QueryRow(
		ctx,
		countQuery,
		customerID,
		filter.Status,
		filter.CheckInFrom,
		filter.CheckInTo,
		filter.CheckOutFrom,
		filter.CheckOutTo,
	).Scan(&result.Total)
	if err != nil {
		return ListResult{}, fmt.Errorf("count customer bookings: %w", err)
	}

	// Retrieve only the requested page.
	listQuery := `
        SELECT
            id::text,
            booking_reference,
            customer_id::text,
            room_type_id::text,
            check_in,
            check_out,
            rooms_count,
            guests_count,
            price_per_night_paise,
            total_amount_paise,
            status,
						payment_expires_at,
						expired_at,
            created_at,
            updated_at,
            cancelled_at
    ` + whereClause + `
        ORDER BY created_at DESC, id DESC
        LIMIT $7 OFFSET $8
    `

	rows, err := r.pool.Query(
		ctx,
		listQuery,
		customerID,
		filter.Status,
		filter.CheckInFrom,
		filter.CheckInTo,
		filter.CheckOutFrom,
		filter.CheckOutTo,
		filter.Limit,
		filter.Offset,
	)
	if err != nil {
		return ListResult{}, fmt.Errorf("query customer bookings: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var item Booking

		if err := rows.Scan(
			&item.ID,
			&item.BookingReference,
			&item.CustomerID,
			&item.RoomTypeID,
			&item.CheckIn,
			&item.CheckOut,
			&item.RoomsCount,
			&item.GuestsCount,
			&item.PricePerNightPaise,
			&item.TotalAmountPaise,
			&item.Status,
			&item.PaymentExpiresAt,
			&item.ExpiredAt,
			&item.CreatedAt,
			&item.UpdatedAt,
			&item.CancelledAt,
		); err != nil {
			return ListResult{}, fmt.Errorf("scan customer booking: %w", err)
		}

		result.Bookings = append(result.Bookings, item)
	}

	if err := rows.Err(); err != nil {
		return ListResult{}, fmt.Errorf("iterate customer bookings: %w", err)
	}

	return result, nil
}

// exceedsGuestCapacity checks whether the requested guest count
// exceeds the combined capacity of the requested rooms.
//
// It uses division to avoid integer overflow.
func exceedsGuestCapacity(
	maxOccupancy int,
	roomsCount int,
	guestsCount int,
) bool {
	if maxOccupancy <= 0 || roomsCount <= 0 || guestsCount <= 0 {
		return true
	}

	requiredRooms := guestsCount / maxOccupancy

	if guestsCount%maxOccupancy != 0 {
		requiredRooms++
	}

	return requiredRooms > roomsCount
}

// calculateTotalAmount prevents integer overflow before calculating
// price per night multiplied by rooms and nights.
func calculateTotalAmount(
	pricePerNight int64,
	rooms int64,
	nights int64,
) (int64, error) {
	if pricePerNight < 0 || rooms <= 0 || nights <= 0 {
		return 0, ErrBookingAmountOverflow
	}

	if pricePerNight != 0 && rooms > math.MaxInt64/pricePerNight {
		return 0, ErrBookingAmountOverflow
	}

	subtotal := pricePerNight * rooms

	if subtotal != 0 && nights > math.MaxInt64/subtotal {
		return 0, ErrBookingAmountOverflow
	}

	return subtotal * nights, nil
}

// ExpirePendingBookings expires overdue pending-payment bookings
// and restores their reserved inventory atomically.
//
// Each booking is processed in its own transaction so that one
// inconsistent booking does not block other expired bookings.
func (r *Repository) ExpirePendingBookings(
	ctx context.Context,
	now time.Time,
	limit int,
) (int, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("expiration batch limit must be positive")
	}

	expiredCount := 0

	for i := 0; i < limit; i++ {
		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return expiredCount, fmt.Errorf(
				"begin booking expiration transaction: %w",
				err,
			)
		}

		// Always attempt rollback when the transaction has not committed.
		deferRollback := func() {
			_ = tx.Rollback(context.Background())
		}

		var booking Booking

		// Lock one overdue booking.
		// SKIP LOCKED allows multiple workers to process different bookings.
		err = tx.QueryRow(
			ctx,
			`
			SELECT
				id::text,
				room_type_id::text,
				check_in,
				check_out,
				rooms_count
			FROM bookings
			WHERE status = $1
			  AND payment_expires_at <= $2
			ORDER BY payment_expires_at ASC, id ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
			`,
			StatusPendingPayment,
			now,
		).Scan(
			&booking.ID,
			&booking.RoomTypeID,
			&booking.CheckIn,
			&booking.CheckOut,
			&booking.RoomsCount,
		)

		if err != nil {
			deferRollback()

			if errors.Is(err, pgx.ErrNoRows) {
				// No more eligible bookings.
				return expiredCount, nil
			}

			return expiredCount, fmt.Errorf(
				"find pending booking for expiration: %w",
				err,
			)
		}

		nights := int64(
			booking.CheckOut.Sub(booking.CheckIn) / (24 * time.Hour),
		)

		if nights <= 0 || booking.RoomsCount <= 0 {
			deferRollback()
			return expiredCount, ErrBookingInventoryInconsistent
		}

		// Lock all inventory rows in ascending date order.
		rows, err := tx.Query(
			ctx,
			`
			SELECT
				stay_date,
				total_rooms,
				available_rooms
			FROM room_inventory
			WHERE room_type_id = $1
			  AND stay_date >= $2
			  AND stay_date < $3
			ORDER BY stay_date ASC
			FOR UPDATE
			`,
			booking.RoomTypeID,
			booking.CheckIn,
			booking.CheckOut,
		)

		if err != nil {
			deferRollback()
			return expiredCount, fmt.Errorf(
				"lock inventory for expired booking: %w",
				err,
			)
		}

		type inventoryRow struct {
			StayDate       time.Time
			TotalRooms     int
			AvailableRooms int
		}

		inventory := make([]inventoryRow, 0, int(nights))

		for rows.Next() {
			var daily inventoryRow

			if err := rows.Scan(
				&daily.StayDate,
				&daily.TotalRooms,
				&daily.AvailableRooms,
			); err != nil {
				rows.Close()
				deferRollback()
				return expiredCount, fmt.Errorf(
					"scan expiration inventory: %w",
					err,
				)
			}

			inventory = append(inventory, daily)
		}

		rowsErr := rows.Err()
		rows.Close()

		if rowsErr != nil {
			deferRollback()
			return expiredCount, fmt.Errorf(
				"iterate expiration inventory: %w",
				rowsErr,
			)
		}

		if int64(len(inventory)) != nights {
			deferRollback()
			return expiredCount, ErrBookingInventoryInconsistent
		}

		// Validate every night before modifying any inventory.
		for _, daily := range inventory {
			if daily.AvailableRooms < 0 ||
				daily.AvailableRooms+booking.RoomsCount > daily.TotalRooms {
				deferRollback()
				return expiredCount, ErrBookingInventoryInconsistent
			}
		}

		// Restore inventory for every night.
		for _, daily := range inventory {
			tag, err := tx.Exec(
				ctx,
				`
				UPDATE room_inventory
				SET available_rooms = available_rooms + $1,
				    updated_at = $2
				WHERE room_type_id = $3
				  AND stay_date = $4
				`,
				booking.RoomsCount,
				now,
				booking.RoomTypeID,
				daily.StayDate,
			)

			if err != nil {
				deferRollback()
				return expiredCount, fmt.Errorf(
					"restore expired booking inventory: %w",
					err,
				)
			}

			if tag.RowsAffected() != 1 {
				deferRollback()
				return expiredCount, ErrBookingInventoryInconsistent
			}
		}

		// Mark the booking expired in the same transaction.
		tag, err := tx.Exec(
			ctx,
			`
			UPDATE bookings
			SET status = $1,
			    expired_at = $2,
			    updated_at = $2
			WHERE id = $3
			  AND status = $4
			  AND payment_expires_at <= $2
			`,
			StatusExpired,
			now,
			booking.ID,
			StatusPendingPayment,
		)

		if err != nil {
			deferRollback()
			return expiredCount, fmt.Errorf(
				"mark booking as expired: %w",
				err,
			)
		}

		if tag.RowsAffected() != 1 {
			deferRollback()
			return expiredCount, ErrBookingInventoryInconsistent
		}

		if err := tx.Commit(ctx); err != nil {
			return expiredCount, fmt.Errorf(
				"commit booking expiration: %w",
				err,
			)
		}

		expiredCount++
	}

	return expiredCount, nil
}
