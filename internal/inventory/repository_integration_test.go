package inventory

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

func setupInventoryRepository(
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

	return NewRepository(pool), pool
}

func createTestRoomTypeForInventory(
	t *testing.T,
	pool *pgxpool.Pool,
) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	var hotelID string

	err := pool.QueryRow(ctx, `
		INSERT INTO hotels (
			name,
			description,
			address,
			city,
			country,
			timezone
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`,
		"Inventory Integration Test Hotel",
		"Temporary hotel for inventory integration tests",
		"123 Inventory Test Street",
		"Thiruvananthapuram",
		"India",
		"Asia/Kolkata",
	).Scan(&hotelID)

	if err != nil {
		t.Fatalf("failed to create test hotel: %v", err)
	}

	t.Cleanup(func() {
		_, err := pool.Exec(
			context.Background(),
			"DELETE FROM hotels WHERE id = $1",
			hotelID,
		)
		if err != nil {
			t.Errorf("failed to clean up test hotel: %v", err)
		}
	})

	var roomTypeID string

	err = pool.QueryRow(ctx, `
		INSERT INTO room_types (
			hotel_id,
			name,
			description,
			max_occupancy,
			price_per_night_paise
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`,
		hotelID,
		fmt.Sprintf("Inventory Test Room %d", time.Now().UnixNano()),
		"Temporary room type for inventory tests",
		3,
		899900,
	).Scan(&roomTypeID)

	if err != nil {
		t.Fatalf("failed to create test room type: %v", err)
	}

	// Room cleanup runs before hotel cleanup.
	t.Cleanup(func() {
		_, err := pool.Exec(
			context.Background(),
			"DELETE FROM room_types WHERE id = $1",
			roomTypeID,
		)
		if err != nil {
			t.Errorf("failed to clean up test room type: %v", err)
		}
	})

	return roomTypeID
}

func inventoryTestDate(day int) time.Time {
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

func cleanupInventoryDate(
	t *testing.T,
	pool *pgxpool.Pool,
	roomTypeID string,
	stayDate time.Time,
) {
	t.Helper()

	// Registered after room cleanup, so this executes first.
	t.Cleanup(func() {
		_, err := pool.Exec(
			context.Background(),
			`DELETE FROM room_inventory
			 WHERE room_type_id = $1 AND stay_date = $2`,
			roomTypeID,
			stayDate,
		)
		if err != nil {
			t.Errorf("failed to clean up inventory: %v", err)
		}
	})
}

func TestRepositorySetDailyCapacityCreatesInventory(t *testing.T) {
	repo, pool := setupInventoryRepository(t)
	roomTypeID := createTestRoomTypeForInventory(t, pool)

	stayDate := inventoryTestDate(15)

	created, err := repo.SetDailyCapacity(
		context.Background(),
		roomTypeID,
		stayDate,
		10,
	)
	if err != nil {
		t.Fatalf("SetDailyCapacity() error = %v", err)
	}

	cleanupInventoryDate(t, pool, roomTypeID, stayDate)

	if created.RoomTypeID != roomTypeID {
		t.Errorf("expected room type ID %q, got %q",
			roomTypeID, created.RoomTypeID)
	}

	if created.TotalRooms != 10 {
		t.Errorf("expected total rooms 10, got %d", created.TotalRooms)
	}

	if created.AvailableRooms != 10 {
		t.Errorf("expected available rooms 10, got %d",
			created.AvailableRooms)
	}

	if created.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be populated")
	}
}

func TestRepositorySetDailyCapacityPreservesBookedRooms(t *testing.T) {
	repo, pool := setupInventoryRepository(t)
	roomTypeID := createTestRoomTypeForInventory(t, pool)

	stayDate := inventoryTestDate(16)

	created, err := repo.SetDailyCapacity(
		context.Background(),
		roomTypeID,
		stayDate,
		10,
	)
	if err != nil {
		t.Fatalf("initial SetDailyCapacity() error = %v", err)
	}

	cleanupInventoryDate(t, pool, roomTypeID, stayDate)

	// Simulate three rooms already booked.
	_, err = pool.Exec(
		context.Background(),
		`UPDATE room_inventory
		 SET available_rooms = 7
		 WHERE room_type_id = $1 AND stay_date = $2`,
		roomTypeID,
		stayDate,
	)
	if err != nil {
		t.Fatalf("failed to simulate booked rooms: %v", err)
	}

	// Increase capacity from 10 to 12.
	increased, err := repo.SetDailyCapacity(
		context.Background(),
		roomTypeID,
		stayDate,
		12,
	)
	if err != nil {
		t.Fatalf("increase capacity error = %v", err)
	}

	if increased.TotalRooms != 12 {
		t.Errorf("expected total rooms 12, got %d", increased.TotalRooms)
	}

	if increased.AvailableRooms != 9 {
		t.Errorf("expected available rooms 9, got %d",
			increased.AvailableRooms)
	}

	// Decrease capacity from 12 to 8.
	decreased, err := repo.SetDailyCapacity(
		context.Background(),
		roomTypeID,
		stayDate,
		8,
	)
	if err != nil {
		t.Fatalf("decrease capacity error = %v", err)
	}

	if decreased.TotalRooms != 8 {
		t.Errorf("expected total rooms 8, got %d", decreased.TotalRooms)
	}

	if decreased.AvailableRooms != 5 {
		t.Errorf("expected available rooms 5, got %d",
			decreased.AvailableRooms)
	}

	// Three rooms are booked, so capacity cannot be reduced below three.
	_, err = repo.SetDailyCapacity(
		context.Background(),
		roomTypeID,
		stayDate,
		2,
	)
	if !errors.Is(err, ErrCapacityBelowBooked) {
		t.Fatalf("expected ErrCapacityBelowBooked, got %v", err)
	}

	// Rejected update must not modify the existing inventory.
	var totalRooms, availableRooms int

	err = pool.QueryRow(
		context.Background(),
		`SELECT total_rooms, available_rooms
		 FROM room_inventory
		 WHERE room_type_id = $1 AND stay_date = $2`,
		roomTypeID,
		stayDate,
	).Scan(&totalRooms, &availableRooms)

	if err != nil {
		t.Fatalf("failed to read inventory after rejected update: %v", err)
	}

	if totalRooms != 8 || availableRooms != 5 {
		t.Errorf(
			"rejected update modified inventory: total=%d available=%d",
			totalRooms,
			availableRooms,
		)
	}

	if created.AvailableRooms != 10 {
		t.Errorf("initial inventory should have 10 available rooms, got %d",
			created.AvailableRooms)
	}
}

func TestRepositorySetDailyCapacityUpdatesExistingRecord(t *testing.T) {
	repo, pool := setupInventoryRepository(t)
	roomTypeID := createTestRoomTypeForInventory(t, pool)

	stayDate := inventoryTestDate(17)

	_, err := repo.SetDailyCapacity(
		context.Background(),
		roomTypeID,
		stayDate,
		10,
	)
	if err != nil {
		t.Fatalf("initial SetDailyCapacity() error = %v", err)
	}

	cleanupInventoryDate(t, pool, roomTypeID, stayDate)

	updated, err := repo.SetDailyCapacity(
		context.Background(),
		roomTypeID,
		stayDate,
		15,
	)
	if err != nil {
		t.Fatalf("second SetDailyCapacity() error = %v", err)
	}

	if updated.TotalRooms != 15 || updated.AvailableRooms != 15 {
		t.Errorf(
			"expected total=15 and available=15, got total=%d available=%d",
			updated.TotalRooms,
			updated.AvailableRooms,
		)
	}

	var count int

	err = pool.QueryRow(
		context.Background(),
		`SELECT COUNT(*)
		 FROM room_inventory
		 WHERE room_type_id = $1 AND stay_date = $2`,
		roomTypeID,
		stayDate,
	).Scan(&count)

	if err != nil {
		t.Fatalf("failed to count inventory records: %v", err)
	}

	if count != 1 {
		t.Errorf("expected exactly one inventory record, got %d", count)
	}
}

func TestRepositorySetDailyCapacityRoomTypeNotFound(t *testing.T) {
	repo, _ := setupInventoryRepository(t)

	_, err := repo.SetDailyCapacity(
		context.Background(),
		"00000000-0000-0000-0000-000000000000",
		inventoryTestDate(18),
		10,
	)

	if !errors.Is(err, ErrRoomTypeNotFound) {
		t.Fatalf("expected ErrRoomTypeNotFound, got %v", err)
	}
}

func TestRepositoryListByRoomTypeAndDateRange(t *testing.T) {
	repo, pool := setupInventoryRepository(t)
	roomTypeID := createTestRoomTypeForInventory(t, pool)

	dates := []time.Time{
		inventoryTestDate(20),
		inventoryTestDate(18),
		inventoryTestDate(19),
	}

	for i, stayDate := range dates {
		_, err := repo.SetDailyCapacity(
			context.Background(),
			roomTypeID,
			stayDate,
			10+i,
		)
		if err != nil {
			t.Fatalf("SetDailyCapacity() for %v error = %v", stayDate, err)
		}

		cleanupInventoryDate(t, pool, roomTypeID, stayDate)
	}

	inventory, err := repo.ListByRoomTypeAndDateRange(
		context.Background(),
		roomTypeID,
		inventoryTestDate(18),
		inventoryTestDate(20),
	)
	if err != nil {
		t.Fatalf("ListByRoomTypeAndDateRange() error = %v", err)
	}

	if len(inventory) != 2 {
		t.Fatalf("expected 2 inventory records, got %d", len(inventory))
	}

	if !inventory[0].StayDate.Equal(inventoryTestDate(18)) {
		t.Errorf("expected first date January 18, got %v",
			inventory[0].StayDate)
	}

	if !inventory[1].StayDate.Equal(inventoryTestDate(19)) {
		t.Errorf("expected second date January 19, got %v",
			inventory[1].StayDate)
	}

	if inventory[0].TotalRooms != 11 {
		t.Errorf("expected first day's total rooms 11, got %d",
			inventory[0].TotalRooms)
	}

	if inventory[1].TotalRooms != 12 {
		t.Errorf("expected second day's total rooms 12, got %d",
			inventory[1].TotalRooms)
	}
}

func TestRepositoryListByRoomTypeAndDateRangeEmptyResult(t *testing.T) {
	repo, pool := setupInventoryRepository(t)
	roomTypeID := createTestRoomTypeForInventory(t, pool)

	inventory, err := repo.ListByRoomTypeAndDateRange(
		context.Background(),
		roomTypeID,
		inventoryTestDate(21),
		inventoryTestDate(25),
	)
	if err != nil {
		t.Fatalf("ListByRoomTypeAndDateRange() error = %v", err)
	}

	if inventory == nil {
		t.Fatal("expected non-nil empty inventory slice")
	}

	if len(inventory) != 0 {
		t.Errorf("expected 0 inventory records, got %d", len(inventory))
	}
}

func TestRepositoryListByRoomTypeAndDateRangeRoomTypeNotFound(t *testing.T) {
	repo, _ := setupInventoryRepository(t)

	_, err := repo.ListByRoomTypeAndDateRange(
		context.Background(),
		"00000000-0000-0000-0000-000000000000",
		inventoryTestDate(21),
		inventoryTestDate(25),
	)

	if !errors.Is(err, ErrRoomTypeNotFound) {
		t.Fatalf("expected ErrRoomTypeNotFound, got %v", err)
	}
}

func TestRepositoryConcurrentSetDailyCapacityPreservesBookedRooms(t *testing.T) {
	repo, pool := setupInventoryRepository(t)
	roomTypeID := createTestRoomTypeForInventory(t, pool)

	stayDate := inventoryTestDate(22)

	_, err := repo.SetDailyCapacity(
		context.Background(),
		roomTypeID,
		stayDate,
		10,
	)
	if err != nil {
		t.Fatalf("initial SetDailyCapacity() error = %v", err)
	}

	cleanupInventoryDate(t, pool, roomTypeID, stayDate)

	// Simulate three rooms already booked.
	_, err = pool.Exec(
		context.Background(),
		`UPDATE room_inventory
		 SET available_rooms = 7
		 WHERE room_type_id = $1 AND stay_date = $2`,
		roomTypeID,
		stayDate,
	)
	if err != nil {
		t.Fatalf("failed to simulate booked rooms: %v", err)
	}

	const workers = 10

	var wg sync.WaitGroup
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)

		go func(capacity int) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(
				context.Background(),
				10*time.Second,
			)
			defer cancel()

			_, err := repo.SetDailyCapacity(
				ctx,
				roomTypeID,
				stayDate,
				10+capacity,
			)
			if err != nil {
				errs <- err
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent SetDailyCapacity() error = %v", err)
	}

	var totalRooms, availableRooms int

	err = pool.QueryRow(
		context.Background(),
		`SELECT total_rooms, available_rooms
		 FROM room_inventory
		 WHERE room_type_id = $1 AND stay_date = $2`,
		roomTypeID,
		stayDate,
	).Scan(&totalRooms, &availableRooms)

	if err != nil {
		t.Fatalf("failed to read final inventory: %v", err)
	}

	if totalRooms-availableRooms != 3 {
		t.Errorf(
			"expected 3 booked rooms after concurrent updates, total=%d available=%d",
			totalRooms,
			availableRooms,
		)
	}

	if availableRooms < 0 || availableRooms > totalRooms {
		t.Errorf(
			"inventory constraint violated: total=%d available=%d",
			totalRooms,
			availableRooms,
		)
	}
}

func TestRepositoryConcurrentInitialInventoryCreation(t *testing.T) {
	repo, pool := setupInventoryRepository(t)
	roomTypeID := createTestRoomTypeForInventory(t, pool)

	stayDate := inventoryTestDate(23)
	cleanupInventoryDate(t, pool, roomTypeID, stayDate)

	const workers = 10

	var wg sync.WaitGroup
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)

		go func(capacity int) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(
				context.Background(),
				10*time.Second,
			)
			defer cancel()

			_, err := repo.SetDailyCapacity(
				ctx,
				roomTypeID,
				stayDate,
				10+capacity,
			)
			if err != nil {
				errs <- err
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent initial creation error = %v", err)
	}

	var count, totalRooms, availableRooms int

	err := pool.QueryRow(
		context.Background(),
		`SELECT COUNT(*), MAX(total_rooms), MAX(available_rooms)
		 FROM room_inventory
		 WHERE room_type_id = $1 AND stay_date = $2`,
		roomTypeID,
		stayDate,
	).Scan(&count, &totalRooms, &availableRooms)

	if err != nil {
		t.Fatalf("failed to inspect final inventory: %v", err)
	}

	if count != 1 {
		t.Fatalf("expected exactly one inventory record, got %d", count)
	}

	if totalRooms != availableRooms {
		t.Errorf(
			"expected all rooms available on initial creation, total=%d available=%d",
			totalRooms,
			availableRooms,
		)
	}
}
