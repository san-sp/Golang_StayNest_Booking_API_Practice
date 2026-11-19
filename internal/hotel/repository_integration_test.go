package hotel

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"staynest/internal/database"
)

func setupHotelRepository(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()

	databaseURL := os.Getenv("STAYNEST_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("STAYNEST_TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

func createTestHotel(t *testing.T, repo *Repository, pool *pgxpool.Pool) *Hotel {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	created, err := repo.Create(ctx, Hotel{
		Name:        "StayNest Test Hotel",
		Description: "Hotel created for repository integration tests",
		Address:     "123 Test Street",
		City:        "Thiruvananthapuram",
		Country:     "India",
		Timezone:    "Asia/Kolkata",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	t.Cleanup(func() {
		_, err := pool.Exec(
			context.Background(),
			"DELETE FROM hotels WHERE id = $1",
			created.ID,
		)
		if err != nil {
			t.Errorf("failed to clean up test hotel: %v", err)
		}
	})

	return created
}

func createTestHotelInCity(
	t *testing.T,
	repo *Repository,
	pool *pgxpool.Pool,
	name string,
	city string,
) *Hotel {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	created, err := repo.Create(ctx, Hotel{
		Name:     name,
		Address:  "123 Integration Test Street",
		City:     city,
		Country:  "India",
		Timezone: "Asia/Kolkata",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	t.Cleanup(func() {
		_, err := pool.Exec(
			context.Background(),
			"DELETE FROM hotels WHERE id = $1",
			created.ID,
		)
		if err != nil {
			t.Errorf("failed to clean up test hotel: %v", err)
		}
	})

	return created
}

func TestRepositoryCreateHotel(t *testing.T) {
	repo, pool := setupHotelRepository(t)

	created := createTestHotel(t, repo, pool)

	if created.ID == "" {
		t.Fatal("expected generated hotel ID")
	}

	if created.Name != "StayNest Test Hotel" {
		t.Errorf("expected hotel name %q, got %q",
			"StayNest Test Hotel", created.Name)
	}

	if created.City != "Thiruvananthapuram" {
		t.Errorf("expected city Thiruvananthapuram, got %q", created.City)
	}

	if created.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be populated")
	}

	if created.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be populated")
	}
}

func TestRepositoryGetHotelByID(t *testing.T) {
	repo, pool := setupHotelRepository(t)

	created := createTestHotel(t, repo, pool)

	found, err := repo.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}

	if found.ID != created.ID {
		t.Errorf("expected ID %q, got %q", created.ID, found.ID)
	}

	if found.Name != created.Name {
		t.Errorf("expected name %q, got %q", created.Name, found.Name)
	}

	if found.Address != created.Address {
		t.Errorf("expected address %q, got %q", created.Address, found.Address)
	}
}

func TestRepositoryGetHotelByIDNotFound(t *testing.T) {
	repo, _ := setupHotelRepository(t)

	_, err := repo.GetByID(context.Background(), "00000000-0000-0000-0000-000000000000")

	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows, got %v", err)
	}
}

func TestRepositoryUpdateHotel(t *testing.T) {
	repo, pool := setupHotelRepository(t)

	created := createTestHotel(t, repo, pool)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Move updated_at into the past so the update can be verified.
	_, err := pool.Exec(
		ctx,
		`UPDATE hotels
		 SET updated_at = NOW() - INTERVAL '1 minute'
		 WHERE id = $1`,
		created.ID,
	)
	if err != nil {
		t.Fatalf("failed to prepare hotel timestamp: %v", err)
	}

	beforeUpdate, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() before update error = %v", err)
	}

	updated, err := repo.Update(ctx, created.ID, Hotel{
		Name:        "Updated StayNest Hotel",
		Description: "Updated hotel description",
		Address:     "456 New Street",
		City:        "Kochi",
		Country:     "India",
		Timezone:    "Asia/Kolkata",
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if updated.Name != "Updated StayNest Hotel" {
		t.Errorf("unexpected updated name: %q", updated.Name)
	}

	if updated.City != "Kochi" {
		t.Errorf("expected city Kochi, got %q", updated.City)
	}

	if updated.UpdatedAt.Before(beforeUpdate.UpdatedAt) ||
		updated.UpdatedAt.Equal(beforeUpdate.UpdatedAt) {
		t.Error("expected UpdatedAt to advance")
	}

	if updated.CreatedAt != created.CreatedAt {
		t.Error("CreatedAt should not change during an update")
	}
}

func TestRepositoryUpdateHotelNotFound(t *testing.T) {
	repo, _ := setupHotelRepository(t)

	_, err := repo.Update(
		context.Background(),
		"00000000-0000-0000-0000-000000000000",
		Hotel{
			Name:     "Missing Hotel",
			Address:  "Unknown Street",
			City:     "Kochi",
			Country:  "India",
			Timezone: "Asia/Kolkata",
		},
	)

	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows, got %v", err)
	}
}

func TestRepositoryGetHotelDatabaseError(t *testing.T) {
	repo, _ := setupHotelRepository(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000000")

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRepositoryCreateHotelDatabaseError(t *testing.T) {
	repo, _ := setupHotelRepository(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := repo.Create(ctx, Hotel{
		Name:     "Test Hotel",
		Address:  "Test Street",
		City:     "Kochi",
		Country:  "India",
		Timezone: "Asia/Kolkata",
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRepositoryListHotelsWithCityFilterAndPagination(t *testing.T) {
	repo, pool := setupHotelRepository(t)

	// Generate a city name unique to this test.
	city := "StayNestList-" + time.Now().UTC().Format("20060102150405.000000000")

	createTestHotelInCity(t, repo, pool, "List Hotel One", city)
	createTestHotelInCity(t, repo, pool, "List Hotel Two", city)
	createTestHotelInCity(t, repo, pool, "List Hotel Three", city)

	// This hotel must not be included in the city-filtered results.
	createTestHotelInCity(t, repo, pool, "Other City Hotel", city+"-Other")

	result, err := repo.List(context.Background(), ListParams{
		City:   strings.ToUpper(city),
		Limit:  2,
		Offset: 1,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if result.Total != 3 {
		t.Errorf("expected total 3, got %d", result.Total)
	}

	if len(result.Hotels) != 2 {
		t.Fatalf("expected 2 hotels, got %d", len(result.Hotels))
	}

	if result.Limit != 2 {
		t.Errorf("expected limit 2, got %d", result.Limit)
	}

	if result.Offset != 1 {
		t.Errorf("expected offset 1, got %d", result.Offset)
	}

	for _, found := range result.Hotels {
		if !strings.EqualFold(found.City, city) {
			t.Errorf("expected city %q, got %q", city, found.City)
		}
	}
}

func TestRepositoryListHotelsEmptyResult(t *testing.T) {
	repo, _ := setupHotelRepository(t)

	result, err := repo.List(context.Background(), ListParams{
		City:   "StayNestCityThatDoesNotExist",
		Limit:  20,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil ListResult")
	}

	if result.Hotels == nil {
		t.Fatal("expected non-nil empty Hotels slice")
	}

	if len(result.Hotels) != 0 {
		t.Errorf("expected 0 hotels, got %d", len(result.Hotels))
	}

	if result.Total != 0 {
		t.Errorf("expected total 0, got %d", result.Total)
	}
}

func TestRepositoryListHotelsDatabaseError(t *testing.T) {
	repo, _ := setupHotelRepository(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := repo.List(ctx, ListParams{
		Limit:  20,
		Offset: 0,
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRepositorySearchCalendarDateBoundaries(t *testing.T) {
	repo, pool := setupHotelRepository(t)

	city := "StayNestSearch-" + time.Now().UTC().Format("20060102150405.000000000")
	hotel := createTestHotelInCity(t, repo, pool, "Calendar Boundary Hotel", city)

	ctx := context.Background()

	var roomTypeID string
	err := pool.QueryRow(ctx, `
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
		hotel.ID,
		"Calendar Boundary Room",
		"Room for date-boundary integration tests",
		2,
		250000,
	).Scan(&roomTypeID)

	if err != nil {
		t.Fatalf("failed to create room type: %v", err)
	}

	// Clean up child records before createTestHotelInCity
	// removes the hotel. Cleanup functions run in LIFO order.
	t.Cleanup(func() {
		_, err := pool.Exec(ctx,
			`DELETE FROM room_inventory WHERE room_type_id = $1`,
			roomTypeID,
		)
		if err != nil {
			t.Errorf("failed to clean up inventory: %v", err)
		}

		_, err = pool.Exec(ctx,
			`DELETE FROM room_types WHERE id = $1`,
			roomTypeID,
		)
		if err != nil {
			t.Errorf("failed to clean up room type: %v", err)
		}
	})

	insertInventory := func(date time.Time, available int) {
		t.Helper()

		_, err := pool.Exec(ctx, `
			INSERT INTO room_inventory (
				room_type_id,
				stay_date,
				total_rooms,
				available_rooms
			)
			VALUES ($1, $2, $3, $4)
		`, roomTypeID, date, 5, available)

		if err != nil {
			t.Fatalf(
				"failed to insert inventory for %s: %v",
				date.Format("2006-01-02"),
				err,
			)
		}
	}

	search := func(checkIn, checkOut time.Time) ([]SearchResult, error) {
		t.Helper()

		return repo.Search(ctx, SearchParams{
			City:     city,
			CheckIn:  checkIn,
			CheckOut: checkOut,
			Guests:   2,
			Rooms:    1,
		})
	}

	// Case 1: Leap day.
	// February 28 and February 29 are stay nights.
	// March 1 is checkout and must not be required.
	insertInventory(time.Date(2028, time.February, 28, 0, 0, 0, 0, time.UTC), 2)
	insertInventory(time.Date(2028, time.February, 29, 0, 0, 0, 0, time.UTC), 2)
	insertInventory(time.Date(2028, time.March, 1, 0, 0, 0, 0, time.UTC), 0)

	results, err := search(
		time.Date(2028, time.February, 28, 0, 0, 0, 0, time.UTC),
		time.Date(2028, time.March, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("Search() leap-day error = %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected hotel for leap-day stay, got %d results", len(results))
	}

	if len(results[0].Rooms) != 1 {
		t.Fatalf("expected 1 available room type, got %d", len(results[0].Rooms))
	}

	if results[0].Rooms[0].AvailableRooms != 2 {
		t.Errorf(
			"expected 2 available rooms, got %d",
			results[0].Rooms[0].AvailableRooms,
		)
	}

	// Case 2: Year boundary.
	// December 31 and January 1 are stay nights.
	// January 2 is checkout.
	insertInventory(time.Date(2028, time.December, 31, 0, 0, 0, 0, time.UTC), 2)
	insertInventory(time.Date(2029, time.January, 1, 0, 0, 0, 0, time.UTC), 2)
	insertInventory(time.Date(2029, time.January, 2, 0, 0, 0, 0, time.UTC), 0)

	results, err = search(
		time.Date(2028, time.December, 31, 0, 0, 0, 0, time.UTC),
		time.Date(2029, time.January, 2, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("Search() year-boundary error = %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected hotel for year-boundary stay, got %d results", len(results))
	}

	// Case 3: Missing middle night.
	// March 2 has no inventory, so this three-night stay
	// must not appear in search results.
	insertInventory(time.Date(2028, time.March, 10, 0, 0, 0, 0, time.UTC), 2)
	insertInventory(time.Date(2028, time.March, 12, 0, 0, 0, 0, time.UTC), 2)

	results, err = search(
		time.Date(2028, time.March, 10, 0, 0, 0, 0, time.UTC),
		time.Date(2028, time.March, 13, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("Search() missing-night error = %v", err)
	}

	if len(results) != 0 {
		t.Errorf(
			"expected no hotels when a middle night is missing, got %d",
			len(results),
		)
	}
}
