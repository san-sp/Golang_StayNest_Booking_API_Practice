package room

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"staynest/internal/database"
)

func setupRoomRepository(t *testing.T) (*Repository, *pgxpool.Pool) {
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

func createTestHotelForRoom(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
		"Room Integration Test Hotel",
		"Temporary hotel for room integration tests",
		"123 Integration Street",
		"Thiruvananthapuram",
		"India",
		"Asia/Kolkata",
	).Scan(&hotelID)

	if err != nil {
		t.Fatalf("failed to create test hotel: %v", err)
	}

	// Registered before room cleanup, so hotel cleanup runs last.
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

	return hotelID
}

func createTestRoomType(
	t *testing.T,
	repo *Repository,
	pool *pgxpool.Pool,
	hotelID string,
	name string,
) *RoomType {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	created, err := repo.Create(ctx, RoomType{
		HotelID:            hotelID,
		Name:               name,
		Description:        "Room created for integration tests",
		MaxOccupancy:       3,
		PricePerNightPaise: 899900,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Registered after hotel cleanup, so this executes first.
	t.Cleanup(func() {
		_, err := pool.Exec(
			context.Background(),
			"DELETE FROM room_types WHERE id = $1",
			created.ID,
		)
		if err != nil {
			t.Errorf("failed to clean up test room type: %v", err)
		}
	})

	return created
}

func TestRepositoryCreateRoomType(t *testing.T) {
	repo, pool := setupRoomRepository(t)

	hotelID := createTestHotelForRoom(t, pool)
	created := createTestRoomType(t, repo, pool, hotelID, "Deluxe Room")

	if created.ID == "" {
		t.Fatal("expected generated room type ID")
	}

	if created.HotelID != hotelID {
		t.Errorf("expected hotel ID %q, got %q", hotelID, created.HotelID)
	}

	if created.Name != "Deluxe Room" {
		t.Errorf("expected room name %q, got %q", "Deluxe Room", created.Name)
	}

	if created.MaxOccupancy != 3 {
		t.Errorf("expected max occupancy 3, got %d", created.MaxOccupancy)
	}

	if created.PricePerNightPaise != 899900 {
		t.Errorf("expected price 899900, got %d", created.PricePerNightPaise)
	}

	if created.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be populated")
	}

	if created.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be populated")
	}
}

func TestRepositoryGetRoomTypeByID(t *testing.T) {
	repo, pool := setupRoomRepository(t)

	hotelID := createTestHotelForRoom(t, pool)
	created := createTestRoomType(t, repo, pool, hotelID, "Deluxe Room")

	found, err := repo.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}

	if found.ID != created.ID {
		t.Errorf("expected ID %q, got %q", created.ID, found.ID)
	}

	if found.HotelID != hotelID {
		t.Errorf("expected hotel ID %q, got %q", hotelID, found.HotelID)
	}

	if found.Name != created.Name {
		t.Errorf("expected name %q, got %q", created.Name, found.Name)
	}
}

func TestRepositoryGetRoomTypeByIDNotFound(t *testing.T) {
	repo, _ := setupRoomRepository(t)

	_, err := repo.GetByID(
		context.Background(),
		"00000000-0000-0000-0000-000000000000",
	)

	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows, got %v", err)
	}
}

func TestRepositoryListRoomTypesByHotelID(t *testing.T) {
	repo, pool := setupRoomRepository(t)

	hotelID := createTestHotelForRoom(t, pool)
	otherHotelID := createTestHotelForRoom(t, pool)

	createTestRoomType(t, repo, pool, hotelID, "Deluxe Room")
	createTestRoomType(t, repo, pool, hotelID, "Standard Room")
	createTestRoomType(t, repo, pool, otherHotelID, "Suite Room")

	rooms, err := repo.ListByHotelID(context.Background(), hotelID)
	if err != nil {
		t.Fatalf("ListByHotelID() error = %v", err)
	}

	if len(rooms) != 2 {
		t.Fatalf("expected 2 room types, got %d", len(rooms))
	}

	for _, roomType := range rooms {
		if roomType.HotelID != hotelID {
			t.Errorf(
				"expected hotel ID %q, got %q",
				hotelID,
				roomType.HotelID,
			)
		}
	}
}

func TestRepositoryListRoomTypesEmptyResult(t *testing.T) {
	repo, pool := setupRoomRepository(t)

	hotelID := createTestHotelForRoom(t, pool)

	rooms, err := repo.ListByHotelID(context.Background(), hotelID)
	if err != nil {
		t.Fatalf("ListByHotelID() error = %v", err)
	}

	if rooms == nil {
		t.Fatal("expected non-nil empty slice")
	}

	if len(rooms) != 0 {
		t.Errorf("expected 0 room types, got %d", len(rooms))
	}
}

func TestRepositoryUpdateRoomType(t *testing.T) {
	repo, pool := setupRoomRepository(t)

	hotelID := createTestHotelForRoom(t, pool)
	created := createTestRoomType(t, repo, pool, hotelID, "Deluxe Room")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := pool.Exec(ctx, `
		UPDATE room_types
		SET updated_at = NOW() - INTERVAL '1 minute'
		WHERE id = $1
	`, created.ID)
	if err != nil {
		t.Fatalf("failed to prepare updated_at: %v", err)
	}

	updated, err := repo.Update(
		ctx,
		created.ID,
		RoomType{
			Name:               "Premium Room",
			Description:        "Updated room description",
			MaxOccupancy:       4,
			PricePerNightPaise: 1299900,
		},
	)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if updated.HotelID != hotelID {
		t.Errorf("expected hotel ID %q, got %q", hotelID, updated.HotelID)
	}

	if updated.Name != "Premium Room" {
		t.Errorf("expected name Premium Room, got %q", updated.Name)
	}

	if updated.MaxOccupancy != 4 {
		t.Errorf("expected occupancy 4, got %d", updated.MaxOccupancy)
	}

	if updated.PricePerNightPaise != 1299900 {
		t.Errorf("expected price 1299900, got %d", updated.PricePerNightPaise)
	}

	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Error("expected UpdatedAt to advance")
	}
}

func TestRepositoryUpdateRoomTypeNotFound(t *testing.T) {
	repo, _ := setupRoomRepository(t)

	_, err := repo.Update(
		context.Background(),
		"00000000-0000-0000-0000-000000000000",
		RoomType{
			Name:               "Premium Room",
			MaxOccupancy:       2,
			PricePerNightPaise: 500000,
		},
	)

	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows, got %v", err)
	}
}

func TestRepositoryCreateDuplicateRoomType(t *testing.T) {
	repo, pool := setupRoomRepository(t)

	hotelID := createTestHotelForRoom(t, pool)
	createTestRoomType(t, repo, pool, hotelID, "Deluxe Room")

	_, err := repo.Create(context.Background(), RoomType{
		HotelID:            hotelID,
		Name:               "Deluxe Room",
		MaxOccupancy:       3,
		PricePerNightPaise: 899900,
	})

	if err == nil {
		t.Fatal("expected duplicate room type error")
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected PostgreSQL error, got %v", err)
	}

	if pgErr.Code != "23505" {
		t.Errorf("expected unique violation 23505, got %s", pgErr.Code)
	}

	if pgErr.ConstraintName != "uq_room_types_hotel_name" {
		t.Errorf(
			"expected constraint uq_room_types_hotel_name, got %s",
			pgErr.ConstraintName,
		)
	}
}

func TestRepositoryCreateRoomTypeInvalidHotel(t *testing.T) {
	repo, _ := setupRoomRepository(t)

	_, err := repo.Create(context.Background(), RoomType{
		HotelID:            "00000000-0000-0000-0000-000000000000",
		Name:               "Deluxe Room",
		MaxOccupancy:       3,
		PricePerNightPaise: 899900,
	})

	if err == nil {
		t.Fatal("expected foreign key violation")
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected PostgreSQL error, got %v", err)
	}

	if pgErr.Code != "23503" {
		t.Errorf("expected foreign key violation 23503, got %s", pgErr.Code)
	}
}

func TestRepositoryDatabaseError(t *testing.T) {
	repo, pool := setupRoomRepository(t)

	hotelID := createTestHotelForRoom(t, pool)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := repo.ListByHotelID(ctx, hotelID)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRepositoryListRoomTypesHotelNotFound(t *testing.T) {
	repo, _ := setupRoomRepository(t)

	_, err := repo.ListByHotelID(
		context.Background(),
		"00000000-0000-0000-0000-000000000000",
	)

	if !errors.Is(err, ErrHotelNotFound) {
		t.Fatalf("expected ErrHotelNotFound, got %v", err)
	}
}
