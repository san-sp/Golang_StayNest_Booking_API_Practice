package booking

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"staynest/internal/database"
)

type bookingTestFixture struct {
	pool        *pgxpool.Pool
	customerIDs []string
	hotelID     string
	roomTypeID  string
}

func lockIntegrationTestDatabase(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()

	const lockKey int64 = 2026112101

	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("failed to acquire integration test connection: %v", err)
	}

	_, err = conn.Exec(
		context.Background(),
		`SELECT pg_advisory_lock($1)`,
		lockKey,
	)
	if err != nil {
		conn.Release()
		t.Fatalf("failed to acquire integration test lock: %v", err)
	}

	t.Cleanup(func() {
		_, err := conn.Exec(
			context.Background(),
			`SELECT pg_advisory_unlock($1)`,
			lockKey,
		)
		if err != nil {
			t.Errorf("failed to release integration test lock: %v", err)
		}

		conn.Release()
	})
}

func setupBookingRepository(
	t *testing.T,
) (*Repository, *pgxpool.Pool) {
	t.Helper()

	databaseURL := os.Getenv("STAYNEST_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("STAYNEST_TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	pool, err := database.NewPool(ctx, databaseURL)
	if err != nil {
		t.Fatalf("failed to connect to database: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
	})

	lockIntegrationTestDatabase(t, pool)

	return NewRepository(pool), pool
}

func newBookingRepositoryWithFixedDate(
	pool *pgxpool.Pool,
	year int,
	month time.Month,
	day int,
) *Repository {
	fixedNow := time.Date(
		year,
		month,
		day,
		0, 0, 0, 0,
		time.UTC,
	)

	return NewRepositoryWithClock(pool, func() time.Time {
		return fixedNow
	})
}

func createBookingTestFixture(
	t *testing.T,
	pool *pgxpool.Pool,
	maxOccupancy int,
	pricePerNight int64,
) *bookingTestFixture {
	t.Helper()

	ctx := context.Background()

	fixture := &bookingTestFixture{
		pool: pool,
	}

	var customerID string

	err := pool.QueryRow(ctx, `
		INSERT INTO customers (
			name,
			email,
			password_hash
		)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`,
		"Booking Integration Customer",
		fmt.Sprintf("booking-test-customer-%d@example.com", time.Now().UnixNano()),
		"test-password-hash",
	).Scan(&customerID)

	if err != nil {
		t.Fatalf("failed to create test customer: %v", err)
	}

	fixture.customerIDs = append(fixture.customerIDs, customerID)

	err = pool.QueryRow(ctx, `
    INSERT INTO hotels (
        name,
        description,
        address,
        city,
        country,
        timezone
    )
    VALUES ($1, $2, $3, $4, $5, $6)
    RETURNING id::text
	`,
		"Test Hotel",
		"A test hotel",
		"123 Test Street",
		"Thiruvananthapuram",
		"India",
		"Asia/Kolkata",
	).Scan(&fixture.hotelID)

	if err != nil {
		t.Fatalf("failed to create test hotel: %v", err)
	}

	err = pool.QueryRow(ctx, `
		INSERT INTO room_types (
			hotel_id,
			name,
			description,
			max_occupancy,
			price_per_night_paise
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text
	`,
		fixture.hotelID,
		fmt.Sprintf("Booking Test Room %d", time.Now().UnixNano()),
		"Temporary room type for booking integration tests",
		maxOccupancy,
		pricePerNight,
	).Scan(&fixture.roomTypeID)

	if err != nil {
		t.Fatalf("failed to create test room type: %v", err)
	}

	// Cleanup bookings and inventory before deleting their parent records.
	t.Cleanup(func() {
		cleanupCtx := context.Background()

		_, err := pool.Exec(
			cleanupCtx,
			`DELETE FROM bookings WHERE room_type_id = $1`,
			fixture.roomTypeID,
		)
		if err != nil {
			t.Errorf("failed to clean up bookings: %v", err)
		}

		_, err = pool.Exec(
			cleanupCtx,
			`DELETE FROM room_inventory WHERE room_type_id = $1`,
			fixture.roomTypeID,
		)
		if err != nil {
			t.Errorf("failed to clean up inventory: %v", err)
		}

		_, err = pool.Exec(
			cleanupCtx,
			`DELETE FROM room_types WHERE id = $1`,
			fixture.roomTypeID,
		)
		if err != nil {
			t.Errorf("failed to clean up room type: %v", err)
		}

		_, err = pool.Exec(
			cleanupCtx,
			`DELETE FROM hotels WHERE id = $1`,
			fixture.hotelID,
		)
		if err != nil {
			t.Errorf("failed to clean up hotel: %v", err)
		}

		for _, id := range fixture.customerIDs {
			_, err := pool.Exec(
				cleanupCtx,
				`DELETE FROM customers WHERE id = $1`,
				id,
			)
			if err != nil {
				t.Errorf("failed to clean up customer %s: %v", id, err)
			}
		}
	})

	return fixture
}

func (f *bookingTestFixture) addCustomer(t *testing.T) string {
	t.Helper()

	var customerID string

	err := f.pool.QueryRow(
		context.Background(),
		`
		INSERT INTO customers (name, email, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id::text
		`,
		"Second Booking Customer",
		fmt.Sprintf("booking-test-second-%d@example.com", time.Now().UnixNano()),
		"test-password-hash",
	).Scan(&customerID)

	if err != nil {
		t.Fatalf("failed to create second test customer: %v", err)
	}

	f.customerIDs = append(f.customerIDs, customerID)

	return customerID
}

func bookingTestDate(day int) time.Time {
	return time.Date(
		2027,
		time.January,
		day,
		0,
		0,
		0,
		0,
		time.UTC,
	)
}

func insertBookingTestInventory(
	t *testing.T,
	pool *pgxpool.Pool,
	roomTypeID string,
	startDate time.Time,
	days int,
	totalRooms int,
) {
	t.Helper()

	for i := 0; i < days; i++ {
		stayDate := startDate.AddDate(0, 0, i)

		_, err := pool.Exec(
			context.Background(),
			`
			INSERT INTO room_inventory (
				room_type_id,
				stay_date,
				total_rooms,
				available_rooms
			)
			VALUES ($1, $2, $3, $3)
			`,
			roomTypeID,
			stayDate,
			totalRooms,
		)

		if err != nil {
			t.Fatalf("failed to insert inventory for %s: %v",
				stayDate.Format("2006-01-02"), err)
		}
	}
}

func newTestBooking(
	customerID string,
	roomTypeID string,
	checkIn time.Time,
	checkOut time.Time,
	rooms int,
	guests int,
	reference string,
) Booking {
	return Booking{
		BookingReference: reference,
		CustomerID:       customerID,
		RoomTypeID:       roomTypeID,
		CheckIn:          checkIn,
		CheckOut:         checkOut,
		RoomsCount:       rooms,
		GuestsCount:      guests,
		Status:           StatusConfirmed,
	}
}

func getAvailableRooms(
	t *testing.T,
	pool *pgxpool.Pool,
	roomTypeID string,
	stayDate time.Time,
) int {
	t.Helper()

	var available int

	err := pool.QueryRow(
		context.Background(),
		`
		SELECT available_rooms
		FROM room_inventory
		WHERE room_type_id = $1 AND stay_date = $2
		`,
		roomTypeID,
		stayDate,
	).Scan(&available)

	if err != nil {
		t.Fatalf("failed to read available rooms: %v", err)
	}

	return available
}

func TestRepositoryCreateBookingRejectsPastCheckIn(t *testing.T) {
	_, pool := setupBookingRepository(t)

	repo := newBookingRepositoryWithFixedDate(
		pool,
		2027,
		time.January,
		15,
	)

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	checkIn := bookingTestDate(14) // Yesterday
	checkOut := bookingTestDate(15)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		checkIn,
		3,
		5,
	)

	input := newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		checkIn,
		checkOut,
		1,
		2,
		fmt.Sprintf("STN-PAST-%d", time.Now().UnixNano()),
	)

	_, err := repo.Create(context.Background(), input)

	if !errors.Is(err, ErrBookingDateInPast) {
		t.Fatalf("expected ErrBookingDateInPast, got %v", err)
	}

	// Rejected booking must not modify inventory.
	for i := 0; i < 3; i++ {
		stayDate := checkIn.AddDate(0, 0, i)
		available := getAvailableRooms(
			t,
			pool,
			fixture.roomTypeID,
			stayDate,
		)

		if available != 5 {
			t.Errorf(
				"inventory changed after rejected booking on %s: expected 5, got %d",
				stayDate.Format("2006-01-02"),
				available,
			)
		}
	}
}

func TestRepositoryCreateBookingAllowsTodayAndTomorrow(t *testing.T) {
	_, pool := setupBookingRepository(t)

	repo := newBookingRepositoryWithFixedDate(
		pool,
		2027,
		time.January,
		15,
	)

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	checkIn := bookingTestDate(15)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		checkIn,
		3,
		5,
	)

	// Check-in today.
	todayBooking := newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		bookingTestDate(15),
		bookingTestDate(16),
		1,
		2,
		fmt.Sprintf("STN-TODAY-%d", time.Now().UnixNano()),
	)

	todayCreated, err := repo.Create(
		context.Background(),
		todayBooking,
	)
	if err != nil {
		t.Fatalf("expected today's booking to succeed, got %v", err)
	}

	if todayCreated.ID == "" {
		t.Fatal("expected today's booking ID to be populated")
	}

	// Check-in tomorrow.
	tomorrowBooking := newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		bookingTestDate(16),
		bookingTestDate(17),
		1,
		2,
		fmt.Sprintf("STN-TMR-%d", time.Now().UnixNano()),
	)

	tomorrowCreated, err := repo.Create(
		context.Background(),
		tomorrowBooking,
	)
	if err != nil {
		t.Fatalf("expected tomorrow's booking to succeed, got %v", err)
	}

	if tomorrowCreated.ID == "" {
		t.Fatal("expected tomorrow's booking ID to be populated")
	}

	// Today and tomorrow are separate one-night stays.
	expectedAvailability := []int{4, 4, 5}

	for i, expected := range expectedAvailability {
		stayDate := checkIn.AddDate(0, 0, i)

		available := getAvailableRooms(
			t,
			pool,
			fixture.roomTypeID,
			stayDate,
		)

		if available != expected {
			t.Errorf(
				"date %s: expected %d available rooms, got %d",
				stayDate.Format("2006-01-02"),
				expected,
				available,
			)
		}
	}
}

func TestRepositoryCreateBookingSuccess(t *testing.T) {
	repo, pool := setupBookingRepository(t)

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	checkIn := bookingTestDate(15)
	checkOut := bookingTestDate(18)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		checkIn,
		4,
		5,
	)

	// Checkout date must not be reserved.
	_, err := pool.Exec(
		context.Background(),
		`
    UPDATE room_inventory
    SET available_rooms = 0
    WHERE room_type_id = $1 AND stay_date = $2
    `,
		fixture.roomTypeID,
		checkOut,
	)
	if err != nil {
		t.Fatalf("failed to set checkout-date inventory: %v", err)
	}

	input := newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		checkIn,
		checkOut,
		2,
		4,
		fmt.Sprintf("STN-TEST-%d", time.Now().UnixNano()),
	)

	input.Status = StatusPendingPayment

	paymentExpiresAt := time.Now().UTC().Add(15 * time.Minute)
	input.PaymentExpiresAt = &paymentExpiresAt

	created, err := repo.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if created.ID == "" {
		t.Error("expected booking ID to be populated")
	}

	if created.Status != StatusPendingPayment {
		t.Errorf("expected status %q, got %q",
			StatusPendingPayment, created.Status)
	}

	if created.PaymentExpiresAt == nil {
		t.Fatal("expected PaymentExpiresAt to be populated")
	}

	if created.ExpiredAt != nil {
		t.Error("expected ExpiredAt to be nil for a pending booking")
	}

	if created.PricePerNightPaise != 899900 {
		t.Errorf("expected price 899900, got %d",
			created.PricePerNightPaise)
	}

	// 899900 paise × 2 rooms × 3 nights.
	expectedTotal := int64(5399400)

	if created.TotalAmountPaise != expectedTotal {
		t.Errorf("expected total %d, got %d",
			expectedTotal, created.TotalAmountPaise)
	}

	if created.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be populated")
	}

	for i := 0; i < 3; i++ {
		stayDate := checkIn.AddDate(0, 0, i)
		available := getAvailableRooms(t, pool, fixture.roomTypeID, stayDate)

		if available != 3 {
			t.Errorf("expected 3 available rooms on %s, got %d",
				stayDate.Format("2006-01-02"), available)
		}
	}

	checkoutAvailability := getAvailableRooms(
		t,
		pool,
		fixture.roomTypeID,
		checkOut,
	)

	if checkoutAvailability != 0 {
		t.Errorf(
			"expected checkout-date availability to remain 0, got %d",
			checkoutAvailability,
		)
	}
}

func TestRepositoryCreateBookingMissingInventoryRollsBack(t *testing.T) {
	repo, pool := setupBookingRepository(t)

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	checkIn := bookingTestDate(20)
	checkOut := bookingTestDate(23)

	// Deliberately omit the second night.
	insertBookingTestInventory(t, pool, fixture.roomTypeID, checkIn, 1, 5)
	insertBookingTestInventory(t, pool, fixture.roomTypeID, checkIn.AddDate(0, 0, 2), 1, 5)

	input := newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		checkIn,
		checkOut,
		1,
		2,
		fmt.Sprintf("STN-TEST-%d", time.Now().UnixNano()),
	)

	_, err := repo.Create(context.Background(), input)

	if !errors.Is(err, ErrInventoryNotConfigured) {
		t.Fatalf("expected ErrInventoryNotConfigured, got %v", err)
	}

	for _, day := range []int{20, 22} {
		stayDate := bookingTestDate(day)
		available := getAvailableRooms(t, pool, fixture.roomTypeID, stayDate)

		if available != 5 {
			t.Errorf("inventory changed after rejected booking: date=%s available=%d",
				stayDate.Format("2006-01-02"), available)
		}
	}
}

func TestRepositoryCreateBookingInsufficientAvailabilityRollsBack(t *testing.T) {
	repo, pool := setupBookingRepository(t)

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	checkIn := bookingTestDate(25)
	checkOut := bookingTestDate(28)

	insertBookingTestInventory(t, pool, fixture.roomTypeID, checkIn, 3, 5)

	// Only one room is available on the second night.
	_, err := pool.Exec(
		context.Background(),
		`
		UPDATE room_inventory
		SET available_rooms = 1
		WHERE room_type_id = $1 AND stay_date = $2
		`,
		fixture.roomTypeID,
		checkIn.AddDate(0, 0, 1),
	)
	if err != nil {
		t.Fatalf("failed to reduce inventory: %v", err)
	}

	input := newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		checkIn,
		checkOut,
		2,
		4,
		fmt.Sprintf("STN-TEST-%d", time.Now().UnixNano()),
	)

	_, err = repo.Create(context.Background(), input)

	if !errors.Is(err, ErrInsufficientAvailability) {
		t.Fatalf("expected ErrInsufficientAvailability, got %v", err)
	}

	// All nights must remain unchanged because validation happens
	// before any inventory decrement.
	expectedAvailability := []int{5, 1, 5}

	for i, expected := range expectedAvailability {
		stayDate := checkIn.AddDate(0, 0, i)
		available := getAvailableRooms(t, pool, fixture.roomTypeID, stayDate)

		if available != expected {
			t.Errorf("inventory changed on %s: expected %d, got %d",
				stayDate.Format("2006-01-02"), expected, available)
		}
	}
}

func TestRepositoryCreateBookingGuestCapacityExceeded(t *testing.T) {
	repo, pool := setupBookingRepository(t)

	fixture := createBookingTestFixture(t, pool, 2, 899900)

	input := newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		bookingTestDate(10),
		bookingTestDate(11),
		1,
		3,
		fmt.Sprintf("STN-TEST-%d", time.Now().UnixNano()),
	)

	_, err := repo.Create(context.Background(), input)

	if !errors.Is(err, ErrGuestCapacityExceeded) {
		t.Fatalf("expected ErrGuestCapacityExceeded, got %v", err)
	}
}

func TestRepositoryConcurrentBookingsCannotOversell(t *testing.T) {
	repo, pool := setupBookingRepository(t)

	fixture := createBookingTestFixture(t, pool, 2, 899900)
	secondCustomerID := fixture.addCustomer(t)

	checkIn := bookingTestDate(12)
	checkOut := bookingTestDate(13)

	insertBookingTestInventory(t, pool, fixture.roomTypeID, checkIn, 1, 1)

	bookings := []Booking{
		newTestBooking(
			fixture.customerIDs[0],
			fixture.roomTypeID,
			checkIn,
			checkOut,
			1,
			2,
			fmt.Sprintf("STN-TEST-A-%d", time.Now().UnixNano()),
		),
		newTestBooking(
			secondCustomerID,
			fixture.roomTypeID,
			checkIn,
			checkOut,
			1,
			2,
			fmt.Sprintf("STN-TEST-B-%d", time.Now().UnixNano()),
		),
	}

	start := make(chan struct{})
	var wg sync.WaitGroup

	type result struct {
		booking *Booking
		err     error
	}

	results := make(chan result, len(bookings))

	for _, input := range bookings {
		wg.Add(1)

		go func(input Booking) {
			defer wg.Done()

			<-start

			created, err := repo.Create(context.Background(), input)

			results <- result{
				booking: created,
				err:     err,
			}
		}(input)
	}

	close(start)

	wg.Wait()
	close(results)

	successCount := 0
	availabilityErrorCount := 0

	for result := range results {
		if result.err == nil {
			successCount++
			continue
		}

		if errors.Is(result.err, ErrInsufficientAvailability) {
			availabilityErrorCount++
			continue
		}

		t.Errorf("unexpected booking error: %v", result.err)
	}

	if successCount != 1 {
		t.Errorf("expected exactly one successful booking, got %d", successCount)
	}

	if availabilityErrorCount != 1 {
		t.Errorf("expected exactly one insufficient availability error, got %d",
			availabilityErrorCount)
	}

	available := getAvailableRooms(t, pool, fixture.roomTypeID, checkIn)

	if available != 0 {
		t.Errorf("expected 0 available rooms after booking, got %d", available)
	}
}

func TestRepositoryCancelBookingRestoresInventoryOnlyOnce(t *testing.T) {
	repo, pool := setupBookingRepository(t)

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	ctx := context.Background()

	checkIn := bookingTestDate(24)
	checkOut := bookingTestDate(27)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		checkIn,
		4,
		5,
	)

	// Checkout date must not be affected by booking or cancellation.
	_, err := pool.Exec(ctx, `
    UPDATE room_inventory
    SET available_rooms = 0
    WHERE room_type_id = $1 AND stay_date = $2
`, fixture.roomTypeID, checkOut)

	if err != nil {
		t.Fatalf("failed to set checkout-date inventory: %v", err)
	}

	bookingInput := newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		checkIn,
		checkOut,
		2,
		4,
		"STN-CANCEL-RESTORE-001",
	)

	created, err := repo.Create(ctx, bookingInput)
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	// Two rooms should have been reserved for each night.
	for day := 0; day < 3; day++ {
		available := getAvailableRooms(
			t,
			pool,
			fixture.roomTypeID,
			bookingTestDate(24+day),
		)

		if available != 3 {
			t.Fatalf(
				"expected 3 available rooms before cancellation, got %d",
				available,
			)
		}
	}

	// First cancellation.
	cancelled, err := repo.Cancel(
		ctx,
		fixture.customerIDs[0],
		created.ID,
	)
	if err != nil {
		t.Fatalf("cancel booking: %v", err)
	}

	if cancelled.Status != StatusCancelled {
		t.Fatalf("expected cancelled status, got %q", cancelled.Status)
	}

	if cancelled.CancelledAt == nil {
		t.Fatal("expected cancelled_at to be set")
	}

	firstCancelledAt := *cancelled.CancelledAt

	// Second cancellation must not restore inventory again.
	cancelledAgain, err := repo.Cancel(
		ctx,
		fixture.customerIDs[0],
		created.ID,
	)
	if err != nil {
		t.Fatalf("cancel booking again: %v", err)
	}

	if cancelledAgain.Status != StatusCancelled {
		t.Fatalf(
			"expected cancelled status after repeated cancellation, got %q",
			cancelledAgain.Status,
		)
	}

	if cancelledAgain.CancelledAt == nil ||
		!cancelledAgain.CancelledAt.Equal(firstCancelledAt) {
		t.Fatal("repeated cancellation should preserve cancelled_at")
	}

	// Inventory must be restored exactly once.
	for day := 0; day < 3; day++ {
		available := getAvailableRooms(
			t,
			pool,
			fixture.roomTypeID,
			bookingTestDate(24+day),
		)

		if available != 5 {
			t.Fatalf(
				"expected 5 available rooms after cancellation, got %d on day %d",
				available,
				day,
			)
		}
	}

	checkoutAvailability := getAvailableRooms(
		t,
		pool,
		fixture.roomTypeID,
		checkOut,
	)

	if checkoutAvailability != 0 {
		t.Errorf(
			"expected checkout-date availability to remain 0 after cancellation, got %d",
			checkoutAvailability,
		)
	}
}

func TestRepositoryModifyBookingSuccess(t *testing.T) {
	repo, pool := setupBookingRepository(t)
	ctx := context.Background()

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	oldCheckIn := bookingTestDate(15)
	oldCheckOut := bookingTestDate(18)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		oldCheckIn,
		6,
		5,
	)

	created, err := repo.Create(ctx, newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		oldCheckIn,
		oldCheckOut,
		2,
		4,
		fmt.Sprintf("STN-MODIFY-%d", time.Now().UnixNano()),
	))
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	// Change the current room price after the original booking.
	_, err = pool.Exec(ctx, `
		UPDATE room_types
		SET price_per_night_paise = 999900
		WHERE id = $1
	`, fixture.roomTypeID)
	if err != nil {
		t.Fatalf("update room price: %v", err)
	}

	newCheckIn := bookingTestDate(16)
	newCheckOut := bookingTestDate(20)

	modified, err := repo.Modify(
		ctx,
		fixture.customerIDs[0],
		created.ID,
		ModifyInput{
			CheckIn:     newCheckIn,
			CheckOut:    newCheckOut,
			RoomsCount:  3,
			GuestsCount: 6,
		},
	)
	if err != nil {
		t.Fatalf("Modify() error: %v", err)
	}

	if !modified.CheckIn.Equal(newCheckIn) {
		t.Errorf("expected check-in %v, got %v",
			newCheckIn, modified.CheckIn)
	}

	if !modified.CheckOut.Equal(newCheckOut) {
		t.Errorf("expected check-out %v, got %v",
			newCheckOut, modified.CheckOut)
	}

	if modified.RoomsCount != 3 {
		t.Errorf("expected 3 rooms, got %d", modified.RoomsCount)
	}

	if modified.GuestsCount != 6 {
		t.Errorf("expected 6 guests, got %d", modified.GuestsCount)
	}

	if modified.PricePerNightPaise != 999900 {
		t.Errorf("expected current price 999900, got %d",
			modified.PricePerNightPaise)
	}

	// 999900 × 3 rooms × 4 nights.
	expectedTotal := int64(11998800)

	if modified.TotalAmountPaise != expectedTotal {
		t.Errorf("expected total %d, got %d",
			expectedTotal, modified.TotalAmountPaise)
	}

	// Jan 15: old booking only. All rooms released.
	// Jan 16-17: overlapping dates. Net reservation increases by 1.
	// Jan 18-19: new booking only. Three rooms reserved.
	expectedAvailability := []int{5, 2, 2, 2, 2}

	for i, expected := range expectedAvailability {
		stayDate := oldCheckIn.AddDate(0, 0, i)
		available := getAvailableRooms(
			t,
			pool,
			fixture.roomTypeID,
			stayDate,
		)

		if available != expected {
			t.Errorf(
				"date %s: expected %d available rooms, got %d",
				stayDate.Format("2006-01-02"),
				expected,
				available,
			)
		}
	}

	// New checkout date must remain untouched.
	checkoutAvailability := getAvailableRooms(
		t,
		pool,
		fixture.roomTypeID,
		newCheckOut,
	)

	if checkoutAvailability != 5 {
		t.Errorf(
			"expected new checkout-date availability to remain 5, got %d",
			checkoutAvailability,
		)
	}
}

func TestRepositoryModifyBookingInsufficientAvailabilityRollsBack(t *testing.T) {
	repo, pool := setupBookingRepository(t)
	ctx := context.Background()

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	oldCheckIn := bookingTestDate(15)
	oldCheckOut := bookingTestDate(18)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		oldCheckIn,
		4,
		5,
	)

	created, err := repo.Create(ctx, newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		oldCheckIn,
		oldCheckOut,
		2,
		4,
		"STN-MOD-FAIL-001",
	))
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	// Only one room remains available on the new-only date.
	newOnlyDate := bookingTestDate(18)

	_, err = pool.Exec(ctx, `
		UPDATE room_inventory
		SET available_rooms = 1
		WHERE room_type_id = $1 AND stay_date = $2
	`, fixture.roomTypeID, newOnlyDate)
	if err != nil {
		t.Fatalf("reduce inventory: %v", err)
	}

	_, err = repo.Modify(
		ctx,
		fixture.customerIDs[0],
		created.ID,
		ModifyInput{
			CheckIn:     bookingTestDate(16),
			CheckOut:    bookingTestDate(19),
			RoomsCount:  2,
			GuestsCount: 4,
		},
	)

	if !errors.Is(err, ErrInsufficientAvailability) {
		t.Fatalf("expected ErrInsufficientAvailability, got %v", err)
	}

	// Verify the original booking is unchanged.
	current, err := repo.GetByID(
		ctx,
		fixture.customerIDs[0],
		created.ID,
	)
	if err != nil {
		t.Fatalf("get original booking: %v", err)
	}

	if !current.CheckIn.Equal(oldCheckIn) {
		t.Errorf("original check-in changed: %v", current.CheckIn)
	}

	if !current.CheckOut.Equal(oldCheckOut) {
		t.Errorf("original check-out changed: %v", current.CheckOut)
	}

	if current.RoomsCount != 2 {
		t.Errorf("original room count changed: %d", current.RoomsCount)
	}

	// All inventory must retain its original value.
	// The modification must not partially release or reserve rooms.
	expectedAvailability := []int{3, 3, 3, 1}

	for i, expected := range expectedAvailability {
		stayDate := oldCheckIn.AddDate(0, 0, i)

		available := getAvailableRooms(
			t,
			pool,
			fixture.roomTypeID,
			stayDate,
		)

		if available != expected {
			t.Errorf(
				"inventory changed on %s: expected %d, got %d",
				stayDate.Format("2006-01-02"),
				expected,
				available,
			)
		}
	}
}

func TestRepositoryModifyCancelledBooking(t *testing.T) {
	repo, pool := setupBookingRepository(t)
	ctx := context.Background()

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	checkIn := bookingTestDate(15)
	checkOut := bookingTestDate(18)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		checkIn,
		3,
		5,
	)

	created, err := repo.Create(ctx, newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		checkIn,
		checkOut,
		2,
		4,
		"STN-MOD-CANCEL-001",
	))
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	_, err = repo.Cancel(ctx, fixture.customerIDs[0], created.ID)
	if err != nil {
		t.Fatalf("cancel booking: %v", err)
	}

	_, err = repo.Modify(
		ctx,
		fixture.customerIDs[0],
		created.ID,
		ModifyInput{
			CheckIn:     bookingTestDate(16),
			CheckOut:    bookingTestDate(19),
			RoomsCount:  1,
			GuestsCount: 2,
		},
	)

	if !errors.Is(err, ErrBookingNotModifiable) {
		t.Fatalf("expected ErrBookingNotModifiable, got %v", err)
	}

	current, err := repo.GetByID(ctx, fixture.customerIDs[0], created.ID)
	if err != nil {
		t.Fatalf("get booking: %v", err)
	}

	if current.Status != StatusCancelled {
		t.Errorf("expected status %q, got %q",
			StatusCancelled, current.Status)
	}

	if !current.CheckIn.Equal(checkIn) ||
		!current.CheckOut.Equal(checkOut) {
		t.Error("booking dates changed after rejected modification")
	}

	if current.RoomsCount != 2 || current.GuestsCount != 4 {
		t.Error("booking counts changed after rejected modification")
	}

	// Cancellation should have restored all inventory.
	for day := 0; day < 3; day++ {
		available := getAvailableRooms(
			t,
			pool,
			fixture.roomTypeID,
			checkIn.AddDate(0, 0, day),
		)

		if available != 5 {
			t.Errorf("expected 5 available rooms, got %d on day %d",
				available, day)
		}
	}
}

func TestRepositoryModifyBookingDifferentCustomerNotFound(t *testing.T) {
	repo, pool := setupBookingRepository(t)
	ctx := context.Background()

	fixture := createBookingTestFixture(t, pool, 3, 899900)
	anotherCustomerID := fixture.addCustomer(t)

	checkIn := bookingTestDate(15)
	checkOut := bookingTestDate(18)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		checkIn,
		3,
		5,
	)

	created, err := repo.Create(ctx, newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		checkIn,
		checkOut,
		2,
		4,
		"STN-MOD-OWNER-001",
	))
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	_, err = repo.Modify(
		ctx,
		anotherCustomerID,
		created.ID,
		ModifyInput{
			CheckIn:     bookingTestDate(16),
			CheckOut:    bookingTestDate(19),
			RoomsCount:  1,
			GuestsCount: 2,
		},
	)

	if !errors.Is(err, ErrBookingNotFound) {
		t.Fatalf("expected ErrBookingNotFound, got %v", err)
	}

	current, err := repo.GetByID(ctx, fixture.customerIDs[0], created.ID)
	if err != nil {
		t.Fatalf("get original booking: %v", err)
	}

	if !current.CheckIn.Equal(checkIn) ||
		!current.CheckOut.Equal(checkOut) {
		t.Error("booking dates changed after unauthorized modification")
	}

	if current.RoomsCount != 2 || current.GuestsCount != 4 {
		t.Error("booking counts changed after unauthorized modification")
	}

	for day := 0; day < 3; day++ {
		available := getAvailableRooms(
			t,
			pool,
			fixture.roomTypeID,
			checkIn.AddDate(0, 0, day),
		)

		if available != 3 {
			t.Errorf("expected 3 available rooms, got %d on day %d",
				available, day)
		}
	}
}

func TestRepositoryModifyMissingOldInventory(t *testing.T) {
	repo, pool := setupBookingRepository(t)
	ctx := context.Background()

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	oldCheckIn := bookingTestDate(15)
	oldCheckOut := bookingTestDate(18)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		oldCheckIn,
		5,
		5,
	)

	created, err := repo.Create(ctx, newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		oldCheckIn,
		oldCheckOut,
		2,
		4,
		"STN-MOD-OLD-INV-001",
	))
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	// Simulate an inconsistent inventory record for an existing booking.
	missingDate := bookingTestDate(16)

	_, err = pool.Exec(ctx, `
        DELETE FROM room_inventory
        WHERE room_type_id = $1 AND stay_date = $2
    `, fixture.roomTypeID, missingDate)
	if err != nil {
		t.Fatalf("delete old inventory: %v", err)
	}

	_, err = repo.Modify(
		ctx,
		fixture.customerIDs[0],
		created.ID,
		ModifyInput{
			CheckIn:     bookingTestDate(18),
			CheckOut:    bookingTestDate(20),
			RoomsCount:  2,
			GuestsCount: 4,
		},
	)

	if !errors.Is(err, ErrBookingInventoryInconsistent) {
		t.Fatalf("expected ErrBookingInventoryInconsistent, got %v", err)
	}

	current, err := repo.GetByID(ctx, fixture.customerIDs[0], created.ID)
	if err != nil {
		t.Fatalf("get booking: %v", err)
	}

	if !current.CheckIn.Equal(oldCheckIn) ||
		!current.CheckOut.Equal(oldCheckOut) {
		t.Error("booking dates changed after rejected modification")
	}

	if current.RoomsCount != 2 || current.GuestsCount != 4 {
		t.Error("booking counts changed after rejected modification")
	}

	// Existing inventory records must remain unchanged.
	for _, day := range []int{15, 17, 18, 19} {
		expected := 5
		if day == 15 || day == 17 {
			expected = 3
		}

		available := getAvailableRooms(
			t,
			pool,
			fixture.roomTypeID,
			bookingTestDate(day),
		)

		if available != expected {
			t.Errorf(
				"inventory changed on day %d: expected %d, got %d",
				day, expected, available,
			)
		}
	}
}

func TestRepositoryModifyAlreadyStartedBooking(t *testing.T) {
	repo, pool := setupBookingRepository(t)
	ctx := context.Background()

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	checkIn := bookingTestDate(15)
	checkOut := bookingTestDate(17)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		checkIn,
		4,
		5,
	)

	// Create the booking while January 15 is still in the future
	// according to the real current date.
	created, err := repo.Create(ctx, newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		checkIn,
		checkOut,
		2,
		4,
		fmt.Sprintf("STN-START-%d", time.Now().UnixNano()),
	))
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	// Simulate January 15 at the hotel.
	fixedRepo := newBookingRepositoryWithFixedDate(
		pool,
		2027,
		time.January,
		15,
	)

	_, err = fixedRepo.Modify(
		ctx,
		fixture.customerIDs[0],
		created.ID,
		ModifyInput{
			CheckIn:     bookingTestDate(16),
			CheckOut:    bookingTestDate(18),
			RoomsCount:  1,
			GuestsCount: 2,
		},
	)

	if !errors.Is(err, ErrBookingNotModifiable) {
		t.Fatalf("expected ErrBookingNotModifiable, got %v", err)
	}

	// The original booking must remain unchanged.
	current, err := repo.GetByID(ctx, fixture.customerIDs[0], created.ID)
	if err != nil {
		t.Fatalf("get original booking: %v", err)
	}

	if !current.CheckIn.Equal(checkIn) ||
		!current.CheckOut.Equal(checkOut) {
		t.Error("booking dates changed after rejected modification")
	}

	if current.RoomsCount != 2 || current.GuestsCount != 4 {
		t.Error("booking counts changed after rejected modification")
	}

	// The original two-room reservation must remain intact.
	expectedAvailability := []int{3, 3, 5, 5}

	for i, expected := range expectedAvailability {
		stayDate := checkIn.AddDate(0, 0, i)

		available := getAvailableRooms(
			t,
			pool,
			fixture.roomTypeID,
			stayDate,
		)

		if available != expected {
			t.Errorf(
				"inventory changed on %s: expected %d, got %d",
				stayDate.Format("2006-01-02"),
				expected,
				available,
			)
		}
	}
}

func TestRepositoryModifyMissingNewInventory(t *testing.T) {
	repo, pool := setupBookingRepository(t)
	ctx := context.Background()

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	oldCheckIn := bookingTestDate(15)
	oldCheckOut := bookingTestDate(18)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		oldCheckIn,
		5,
		5,
	)

	created, err := repo.Create(ctx, newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		oldCheckIn,
		oldCheckOut,
		2,
		4,
		"STN-MOD-NEW-INV-001",
	))
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	// Remove inventory for a date required by the modified booking.
	missingDate := bookingTestDate(19)

	_, err = pool.Exec(ctx, `
        DELETE FROM room_inventory
        WHERE room_type_id = $1 AND stay_date = $2
    `, fixture.roomTypeID, missingDate)
	if err != nil {
		t.Fatalf("delete new inventory: %v", err)
	}

	_, err = repo.Modify(
		ctx,
		fixture.customerIDs[0],
		created.ID,
		ModifyInput{
			CheckIn:     bookingTestDate(16),
			CheckOut:    bookingTestDate(20),
			RoomsCount:  2,
			GuestsCount: 4,
		},
	)

	if !errors.Is(err, ErrInventoryNotConfigured) {
		t.Fatalf("expected ErrInventoryNotConfigured, got %v", err)
	}

	current, err := repo.GetByID(ctx, fixture.customerIDs[0], created.ID)
	if err != nil {
		t.Fatalf("get booking: %v", err)
	}

	if !current.CheckIn.Equal(oldCheckIn) ||
		!current.CheckOut.Equal(oldCheckOut) {
		t.Error("booking dates changed after rejected modification")
	}

	if current.RoomsCount != 2 || current.GuestsCount != 4 {
		t.Error("booking counts changed after rejected modification")
	}

	// Original inventory must remain unchanged.
	expectedAvailability := []int{3, 3, 3, 5}

	for i, expected := range expectedAvailability {
		available := getAvailableRooms(
			t,
			pool,
			fixture.roomTypeID,
			bookingTestDate(15+i),
		)

		if available != expected {
			t.Errorf(
				"inventory changed on day %d: expected %d, got %d",
				15+i, expected, available,
			)
		}
	}
}

func TestRepositoryModifyReduceRoomCountRestoresInventory(t *testing.T) {
	repo, pool := setupBookingRepository(t)
	ctx := context.Background()

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	checkIn := bookingTestDate(15)
	checkOut := bookingTestDate(18)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		checkIn,
		3,
		5,
	)

	created, err := repo.Create(ctx, newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		checkIn,
		checkOut,
		3,
		6,
		"STN-MOD-REDUCE-001",
	))
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	// Initially, 3 rooms are reserved each night.
	for day := 0; day < 3; day++ {
		available := getAvailableRooms(
			t,
			pool,
			fixture.roomTypeID,
			checkIn.AddDate(0, 0, day),
		)

		if available != 2 {
			t.Fatalf("expected 2 available rooms, got %d on day %d",
				available, day)
		}
	}

	modified, err := repo.Modify(
		ctx,
		fixture.customerIDs[0],
		created.ID,
		ModifyInput{
			CheckIn:     checkIn,
			CheckOut:    checkOut,
			RoomsCount:  1,
			GuestsCount: 2,
		},
	)
	if err != nil {
		t.Fatalf("modify booking: %v", err)
	}

	if modified.RoomsCount != 1 {
		t.Errorf("expected 1 room, got %d", modified.RoomsCount)
	}

	if modified.GuestsCount != 2 {
		t.Errorf("expected 2 guests, got %d", modified.GuestsCount)
	}

	// Two previously reserved rooms should be released each night.
	for day := 0; day < 3; day++ {
		available := getAvailableRooms(
			t,
			pool,
			fixture.roomTypeID,
			checkIn.AddDate(0, 0, day),
		)

		if available != 4 {
			t.Errorf("expected 4 available rooms, got %d on day %d",
				available, day)
		}
	}

	current, err := repo.GetByID(ctx, fixture.customerIDs[0], created.ID)
	if err != nil {
		t.Fatalf("get modified booking: %v", err)
	}

	if current.RoomsCount != 1 || current.GuestsCount != 2 {
		t.Errorf(
			"expected booking with 1 room and 2 guests, got %d rooms and %d guests",
			current.RoomsCount,
			current.GuestsCount,
		)
	}
}

func TestRepositoryConcurrentModifySameBooking(t *testing.T) {
	repo, pool := setupBookingRepository(t)
	ctx := context.Background()

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	oldCheckIn := bookingTestDate(15)
	oldCheckOut := bookingTestDate(18)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		oldCheckIn,
		5,
		5,
	)

	created, err := repo.Create(ctx, newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		oldCheckIn,
		oldCheckOut,
		2,
		4,
		"STN-MOD-CONCURRENT-001",
	))
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	input := ModifyInput{
		CheckIn:     bookingTestDate(16),
		CheckOut:    bookingTestDate(20),
		RoomsCount:  1,
		GuestsCount: 2,
	}

	start := make(chan struct{})
	results := make(chan error, 2)

	for i := 0; i < 2; i++ {
		go func() {
			<-start

			_, err := repo.Modify(
				ctx,
				fixture.customerIDs[0],
				created.ID,
				input,
			)

			results <- err
		}()
	}

	// Release both goroutines simultaneously.
	close(start)

	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Errorf("concurrent modification failed: %v", err)
		}
	}

	// Verify the final booking.
	current, err := repo.GetByID(
		ctx,
		fixture.customerIDs[0],
		created.ID,
	)
	if err != nil {
		t.Fatalf("get modified booking: %v", err)
	}

	if !current.CheckIn.Equal(input.CheckIn) ||
		!current.CheckOut.Equal(input.CheckOut) {
		t.Error("unexpected final booking dates")
	}

	if current.RoomsCount != 1 || current.GuestsCount != 2 {
		t.Errorf(
			"expected 1 room and 2 guests, got %d rooms and %d guests",
			current.RoomsCount,
			current.GuestsCount,
		)
	}

	// Expected inventory:
	// Jan 15: 5 (original reservation released)
	// Jan 16-19: 4 (one room reserved)
	expectedAvailability := []int{5, 4, 4, 4, 4}

	for i, expected := range expectedAvailability {
		stayDate := bookingTestDate(15 + i)

		available := getAvailableRooms(
			t,
			pool,
			fixture.roomTypeID,
			stayDate,
		)

		if available != expected {
			t.Errorf(
				"inventory mismatch on %s: expected %d, got %d",
				stayDate.Format("2006-01-02"),
				expected,
				available,
			)
		}
	}
}

func TestRepositoryConcurrentModifyAndCancelSameBooking(t *testing.T) {
	repo, pool := setupBookingRepository(t)
	ctx := context.Background()

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	oldCheckIn := bookingTestDate(15)
	oldCheckOut := bookingTestDate(18)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		oldCheckIn,
		5,
		5,
	)

	created, err := repo.Create(ctx, newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		oldCheckIn,
		oldCheckOut,
		2,
		4,
		"STN-MOD-CANCEL-001",
	))
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	input := ModifyInput{
		CheckIn:     bookingTestDate(16),
		CheckOut:    bookingTestDate(20),
		RoomsCount:  1,
		GuestsCount: 2,
	}

	start := make(chan struct{})
	modifyResults := make(chan error, 1)
	cancelResults := make(chan error, 1)

	go func() {
		<-start

		_, err := repo.Modify(
			ctx,
			fixture.customerIDs[0],
			created.ID,
			input,
		)

		modifyResults <- err
	}()

	go func() {
		<-start

		_, err := repo.Cancel(
			ctx,
			fixture.customerIDs[0],
			created.ID,
		)

		cancelResults <- err
	}()

	close(start)

	modifyErr := <-modifyResults
	cancelErr := <-cancelResults

	if cancelErr != nil {
		t.Fatalf("concurrent cancellation failed: %v", cancelErr)
	}

	if modifyErr != nil && !errors.Is(modifyErr, ErrBookingNotModifiable) {
		t.Fatalf("unexpected modification error: %v", modifyErr)
	}

	current, err := repo.GetByID(ctx, fixture.customerIDs[0], created.ID)
	if err != nil {
		t.Fatalf("get final booking: %v", err)
	}

	if current.Status != StatusCancelled {
		t.Errorf("expected cancelled status, got %s", current.Status)
	}

	for i := 0; i < 5; i++ {
		stayDate := bookingTestDate(15 + i)

		available := getAvailableRooms(
			t,
			pool,
			fixture.roomTypeID,
			stayDate,
		)

		if available != 5 {
			t.Errorf(
				"expected all 5 rooms available on %s, got %d",
				stayDate.Format("2006-01-02"),
				available,
			)
		}
	}
}

func TestRepositoryConcurrentDoubleCancel(t *testing.T) {
	repo, pool := setupBookingRepository(t)
	ctx := context.Background()

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	checkIn := bookingTestDate(15)
	checkOut := bookingTestDate(18)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		checkIn,
		5,
		5,
	)

	created, err := repo.Create(ctx, newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		checkIn,
		checkOut,
		2,
		4,
		"STN-DOUBLE-CANCEL-001",
	))
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)

	for i := 0; i < 2; i++ {
		go func() {
			<-start

			_, err := repo.Cancel(
				ctx,
				fixture.customerIDs[0],
				created.ID,
			)

			results <- err
		}()
	}

	close(start)

	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Errorf("concurrent cancellation failed: %v", err)
		}
	}

	current, err := repo.GetByID(ctx, fixture.customerIDs[0], created.ID)
	if err != nil {
		t.Fatalf("get cancelled booking: %v", err)
	}

	if current.Status != StatusCancelled {
		t.Errorf("expected cancelled status, got %s", current.Status)
	}

	for i := 0; i < 5; i++ {
		stayDate := bookingTestDate(15 + i)

		available := getAvailableRooms(
			t,
			pool,
			fixture.roomTypeID,
			stayDate,
		)

		if available != 5 {
			t.Errorf(
				"expected 5 available rooms on %s, got %d",
				stayDate.Format("2006-01-02"),
				available,
			)
		}
	}
}

func TestRepositoryExpirePendingBookingsRestoresInventory(t *testing.T) {
	repo, pool := setupBookingRepository(t)

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	checkIn := bookingTestDate(15)
	checkOut := bookingTestDate(18)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		checkIn,
		4,
		5,
	)

	now := time.Date(
		2027, time.January, 10,
		12, 0, 0, 0,
		time.UTC,
	)

	expiresAt := now.Add(-time.Minute)

	input := newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		checkIn,
		checkOut,
		2,
		4,
		fmt.Sprintf("STN-EXP-%d", time.Now().UnixNano()),
	)
	input.Status = StatusPendingPayment

	input.PaymentExpiresAt = &expiresAt

	created, err := repo.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Two rooms should have been reserved for three nights.
	for i := 0; i < 3; i++ {
		stayDate := checkIn.AddDate(0, 0, i)

		if got := getAvailableRooms(t, pool, fixture.roomTypeID, stayDate); got != 3 {
			t.Fatalf("expected 3 available rooms before expiration, got %d", got)
		}
	}

	expiredCount, err := repo.ExpirePendingBookings(
		context.Background(),
		now,
		10,
	)
	if err != nil {
		t.Fatalf("ExpirePendingBookings() error = %v", err)
	}

	if expiredCount != 1 {
		t.Fatalf("expected 1 expired booking, got %d", expiredCount)
	}

	var status string
	var expiredAt *time.Time

	err = pool.QueryRow(
		context.Background(),
		`
		SELECT status, expired_at
		FROM bookings
		WHERE id = $1
		`,
		created.ID,
	).Scan(&status, &expiredAt)

	if err != nil {
		t.Fatalf("failed to read expired booking: %v", err)
	}

	if status != StatusExpired {
		t.Errorf("expected status %q, got %q", StatusExpired, status)
	}

	if expiredAt == nil {
		t.Fatal("expected expired_at to be populated")
	}

	if !expiredAt.Equal(now) {
		t.Errorf("expected expired_at %v, got %v", now, *expiredAt)
	}

	// Inventory must be completely restored.
	for i := 0; i < 3; i++ {
		stayDate := checkIn.AddDate(0, 0, i)

		if got := getAvailableRooms(t, pool, fixture.roomTypeID, stayDate); got != 5 {
			t.Errorf(
				"expected 5 available rooms after expiration on %s, got %d",
				stayDate.Format("2006-01-02"),
				got,
			)
		}
	}
}

func TestRepositoryExpirePendingBookingsDoesNotExpireActiveBooking(t *testing.T) {
	repo, pool := setupBookingRepository(t)

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	checkIn := bookingTestDate(15)
	checkOut := bookingTestDate(18)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		checkIn,
		3,
		5,
	)

	now := time.Date(
		2027, time.January, 10,
		12, 0, 0, 0,
		time.UTC,
	)

	expiresAt := now.Add(time.Minute)

	input := newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		checkIn,
		checkOut,
		2,
		4,
		fmt.Sprintf("STN-ACTIVE-%d", time.Now().UnixNano()),
	)

	input.Status = StatusPendingPayment

	input.PaymentExpiresAt = &expiresAt

	created, err := repo.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	expiredCount, err := repo.ExpirePendingBookings(
		context.Background(),
		now,
		10,
	)
	if err != nil {
		t.Fatalf("ExpirePendingBookings() error = %v", err)
	}

	if expiredCount != 0 {
		t.Errorf("expected 0 expired bookings, got %d", expiredCount)
	}

	var status string

	err = pool.QueryRow(
		context.Background(),
		`SELECT status FROM bookings WHERE id = $1`,
		created.ID,
	).Scan(&status)

	if err != nil {
		t.Fatalf("failed to read booking status: %v", err)
	}

	if status != StatusPendingPayment {
		t.Errorf("expected status %q, got %q", StatusPendingPayment, status)
	}

	// Inventory must remain reserved.
	for i := 0; i < 3; i++ {
		stayDate := checkIn.AddDate(0, 0, i)

		if got := getAvailableRooms(t, pool, fixture.roomTypeID, stayDate); got != 3 {
			t.Errorf("expected 3 available rooms, got %d", got)
		}
	}
}

func TestRepositoryExpirePendingBookingsDoesNotRestoreInventoryTwice(t *testing.T) {
	repo, pool := setupBookingRepository(t)

	fixture := createBookingTestFixture(t, pool, 3, 899900)

	checkIn := bookingTestDate(15)
	checkOut := bookingTestDate(18)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		checkIn,
		3,
		5,
	)

	now := time.Date(
		2027, time.January, 10,
		12, 0, 0, 0,
		time.UTC,
	)

	expiresAt := now.Add(-time.Minute)

	input := newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		checkIn,
		checkOut,
		2,
		4,
		fmt.Sprintf("STN-IDEMP-%d", time.Now().Unix()),
	)

	input.Status = StatusPendingPayment

	input.PaymentExpiresAt = &expiresAt

	_, err := repo.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	firstCount, err := repo.ExpirePendingBookings(
		context.Background(),
		now,
		10,
	)
	if err != nil {
		t.Fatalf("first expiration error = %v", err)
	}

	secondCount, err := repo.ExpirePendingBookings(
		context.Background(),
		now,
		10,
	)
	if err != nil {
		t.Fatalf("second expiration error = %v", err)
	}

	if firstCount != 1 {
		t.Errorf("expected first run to expire 1 booking, got %d", firstCount)
	}

	if secondCount != 0 {
		t.Errorf("expected second run to expire 0 bookings, got %d", secondCount)
	}

	// Inventory must be restored exactly once.
	for i := 0; i < 3; i++ {
		stayDate := checkIn.AddDate(0, 0, i)

		if got := getAvailableRooms(t, pool, fixture.roomTypeID, stayDate); got != 5 {
			t.Errorf(
				"expected 5 available rooms after repeated expiration, got %d",
				got,
			)
		}
	}
}

func TestRepositoryGetBookingDifferentCustomerNotFound(t *testing.T) {
	repo, pool := setupBookingRepository(t)
	ctx := context.Background()

	fixture := createBookingTestFixture(t, pool, 3, 899900)
	anotherCustomerID := fixture.addCustomer(t)

	checkIn := bookingTestDate(15)
	checkOut := bookingTestDate(18)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		checkIn,
		3,
		5,
	)

	created, err := repo.Create(ctx, newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		checkIn,
		checkOut,
		2,
		4,
		"STN-GET-OWNER-001",
	))
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	_, err = repo.GetByID(ctx, anotherCustomerID, created.ID)

	if !errors.Is(err, ErrBookingNotFound) {
		t.Fatalf("expected ErrBookingNotFound, got %v", err)
	}

	// The owner must still be able to retrieve the booking.
	current, err := repo.GetByID(ctx, fixture.customerIDs[0], created.ID)
	if err != nil {
		t.Fatalf("owner should retrieve booking: %v", err)
	}

	if current.ID != created.ID {
		t.Errorf("expected booking ID %q, got %q", created.ID, current.ID)
	}
}

func TestRepositoryCancelBookingDifferentCustomerNotFound(t *testing.T) {
	repo, pool := setupBookingRepository(t)
	ctx := context.Background()

	fixture := createBookingTestFixture(t, pool, 3, 899900)
	anotherCustomerID := fixture.addCustomer(t)

	checkIn := bookingTestDate(15)
	checkOut := bookingTestDate(18)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		checkIn,
		3,
		5,
	)

	created, err := repo.Create(ctx, newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		checkIn,
		checkOut,
		2,
		4,
		"STN-CANCEL-OWNER-001",
	))
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	// Capture inventory before the unauthorized cancellation.
	availabilityBefore := make([]int, 3)

	for day := 0; day < 3; day++ {
		availabilityBefore[day] = getAvailableRooms(
			t,
			pool,
			fixture.roomTypeID,
			checkIn.AddDate(0, 0, day),
		)
	}

	_, err = repo.Cancel(ctx, anotherCustomerID, created.ID)

	if !errors.Is(err, ErrBookingNotFound) {
		t.Fatalf("expected ErrBookingNotFound, got %v", err)
	}

	// Verify booking status and cancellation timestamp.
	current, err := repo.GetByID(ctx, fixture.customerIDs[0], created.ID)
	if err != nil {
		t.Fatalf("retrieve owner's booking: %v", err)
	}

	if current.Status != StatusConfirmed {
		t.Errorf(
			"expected booking status %q, got %q",
			StatusConfirmed,
			current.Status,
		)
	}

	if current.CancelledAt != nil {
		t.Error("cancelled_at should remain nil")
	}

	// Inventory must remain unchanged.
	for day := 0; day < 3; day++ {
		availabilityAfter := getAvailableRooms(
			t,
			pool,
			fixture.roomTypeID,
			checkIn.AddDate(0, 0, day),
		)

		if availabilityAfter != availabilityBefore[day] {
			t.Errorf(
				"inventory changed on day %d: before %d, after %d",
				day,
				availabilityBefore[day],
				availabilityAfter,
			)
		}
	}
}

func TestRepositoryListByCustomerIDIsolation(t *testing.T) {
	repo, pool := setupBookingRepository(t)
	ctx := context.Background()

	fixture := createBookingTestFixture(t, pool, 3, 899900)
	anotherCustomerID := fixture.addCustomer(t)

	checkIn := bookingTestDate(15)
	checkOut := bookingTestDate(18)

	insertBookingTestInventory(
		t,
		pool,
		fixture.roomTypeID,
		checkIn,
		3,
		10,
	)

	// Create two bookings for Customer A.
	customerABooking1, err := repo.Create(ctx, newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		checkIn,
		checkOut,
		1,
		2,
		"STN-LIST-A-001",
	))
	if err != nil {
		t.Fatalf("create Customer A booking 1: %v", err)
	}

	customerABooking2, err := repo.Create(ctx, newTestBooking(
		fixture.customerIDs[0],
		fixture.roomTypeID,
		checkIn,
		checkOut,
		1,
		2,
		"STN-LIST-A-002",
	))
	if err != nil {
		t.Fatalf("create Customer A booking 2: %v", err)
	}

	// Create one booking for Customer B.
	customerBBooking, err := repo.Create(ctx, newTestBooking(
		anotherCustomerID,
		fixture.roomTypeID,
		checkIn,
		checkOut,
		1,
		2,
		"STN-LIST-B-001",
	))
	if err != nil {
		t.Fatalf("create Customer B booking: %v", err)
	}

	// Verify Customer A sees only their own bookings.
	resultA, err := repo.ListByCustomerID(
		ctx,
		fixture.customerIDs[0],
		ListFilter{
			Limit:  10,
			Offset: 0,
		},
	)
	if err != nil {
		t.Fatalf("list Customer A bookings: %v", err)
	}

	if resultA.Total != 2 {
		t.Fatalf("expected Customer A total 2, got %d", resultA.Total)
	}

	if len(resultA.Bookings) != 2 {
		t.Fatalf("expected 2 Customer A bookings, got %d", len(resultA.Bookings))
	}

	customerAIDs := map[string]bool{
		customerABooking1.ID: true,
		customerABooking2.ID: true,
	}

	for _, item := range resultA.Bookings {
		if item.CustomerID != fixture.customerIDs[0] {
			t.Errorf(
				"Customer A received booking belonging to customer %q",
				item.CustomerID,
			)
		}

		if !customerAIDs[item.ID] {
			t.Errorf("Customer A received unexpected booking %q", item.ID)
		}
	}

	// Verify Customer B sees only their own booking.
	resultB, err := repo.ListByCustomerID(
		ctx,
		anotherCustomerID,
		ListFilter{
			Limit:  10,
			Offset: 0,
		},
	)
	if err != nil {
		t.Fatalf("list Customer B bookings: %v", err)
	}

	if resultB.Total != 1 {
		t.Fatalf("expected Customer B total 1, got %d", resultB.Total)
	}

	if len(resultB.Bookings) != 1 {
		t.Fatalf("expected 1 Customer B booking, got %d", len(resultB.Bookings))
	}

	if resultB.Bookings[0].ID != customerBBooking.ID {
		t.Errorf(
			"expected Customer B booking %q, got %q",
			customerBBooking.ID,
			resultB.Bookings[0].ID,
		)
	}

	if resultB.Bookings[0].CustomerID != anotherCustomerID {
		t.Errorf(
			"expected Customer B ID %q, got %q",
			anotherCustomerID,
			resultB.Bookings[0].CustomerID,
		)
	}
}
