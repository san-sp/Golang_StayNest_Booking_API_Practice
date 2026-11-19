package room

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeRoomRepository struct {
	createFn      func(context.Context, RoomType) (*RoomType, error)
	getByIDFn     func(context.Context, string) (*RoomType, error)
	listByHotelFn func(context.Context, string) ([]RoomType, error)
	updateFn      func(context.Context, string, RoomType) (*RoomType, error)
}

func (f *fakeRoomRepository) Create(
	ctx context.Context,
	roomType RoomType,
) (*RoomType, error) {
	if f.createFn != nil {
		return f.createFn(ctx, roomType)
	}
	return nil, nil
}

func (f *fakeRoomRepository) GetByID(
	ctx context.Context,
	id string,
) (*RoomType, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, id)
	}
	return nil, nil
}

func (f *fakeRoomRepository) ListByHotelID(
	ctx context.Context,
	hotelID string,
) ([]RoomType, error) {
	if f.listByHotelFn != nil {
		return f.listByHotelFn(ctx, hotelID)
	}
	return nil, nil
}

func (f *fakeRoomRepository) Update(
	ctx context.Context,
	id string,
	roomType RoomType,
) (*RoomType, error) {
	if f.updateFn != nil {
		return f.updateFn(ctx, id, roomType)
	}
	return nil, nil
}

func validRoomType() RoomType {
	return RoomType{
		HotelID:            "hotel-123",
		Name:               "Deluxe Room",
		Description:        "Spacious room",
		MaxOccupancy:       3,
		PricePerNightPaise: 899900,
	}
}

func TestServiceCreate(t *testing.T) {
	repo := &fakeRoomRepository{
		createFn: func(_ context.Context, roomType RoomType) (*RoomType, error) {
			return &roomType, nil
		},
	}

	service := NewService(repo)

	input := validRoomType()
	input.Name = "  Deluxe Room  "
	input.Description = "  Spacious room  "

	created, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if created.Name != "Deluxe Room" {
		t.Errorf("expected trimmed name, got %q", created.Name)
	}

	if created.Description != "Spacious room" {
		t.Errorf("expected trimmed description, got %q", created.Description)
	}
}

func TestServiceCreateValidation(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*RoomType)
		wantErr error
	}{
		{
			name:    "missing hotel ID",
			modify:  func(r *RoomType) { r.HotelID = " " },
			wantErr: ErrInvalidHotelID,
		},
		{
			name:    "missing room name",
			modify:  func(r *RoomType) { r.Name = " " },
			wantErr: ErrInvalidRoomName,
		},
		{
			name: "room name too long",
			modify: func(r *RoomType) {
				r.Name = string(make([]byte, 101))
			},
			wantErr: ErrRoomNameTooLong,
		},
		{
			name:    "invalid occupancy",
			modify:  func(r *RoomType) { r.MaxOccupancy = 0 },
			wantErr: ErrInvalidMaxOccupancy,
		},
		{
			name:    "negative price",
			modify:  func(r *RoomType) { r.PricePerNightPaise = -1 },
			wantErr: ErrInvalidRoomPrice,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRoomRepository{}
			service := NewService(repo)

			input := validRoomType()
			tt.modify(&input)

			_, err := service.Create(context.Background(), input)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestServiceCreateDuplicateRoomType(t *testing.T) {
	repo := &fakeRoomRepository{
		createFn: func(_ context.Context, _ RoomType) (*RoomType, error) {
			return nil, &pgconn.PgError{
				Code:           "23505",
				ConstraintName: "uq_room_types_hotel_name",
			}
		},
	}

	service := NewService(repo)

	_, err := service.Create(context.Background(), validRoomType())

	if !errors.Is(err, ErrRoomTypeAlreadyExists) {
		t.Fatalf("expected duplicate room error, got %v", err)
	}
}

func TestServiceCreateHotelNotFound(t *testing.T) {
	repo := &fakeRoomRepository{
		createFn: func(_ context.Context, _ RoomType) (*RoomType, error) {
			return nil, &pgconn.PgError{
				Code: "23503",
			}
		},
	}

	service := NewService(repo)

	_, err := service.Create(context.Background(), validRoomType())

	if !errors.Is(err, ErrHotelNotFound) {
		t.Fatalf("expected hotel not found, got %v", err)
	}
}

func TestServiceGetByID(t *testing.T) {
	expected := validRoomType()

	repo := &fakeRoomRepository{
		getByIDFn: func(_ context.Context, id string) (*RoomType, error) {
			expected.ID = id
			return &expected, nil
		},
	}

	service := NewService(repo)

	found, err := service.GetByID(context.Background(), "room-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if found.ID != "room-123" {
		t.Errorf("expected room-123, got %s", found.ID)
	}
}

func TestServiceGetByIDNotFound(t *testing.T) {
	repo := &fakeRoomRepository{
		getByIDFn: func(_ context.Context, _ string) (*RoomType, error) {
			return nil, pgx.ErrNoRows
		},
	}

	service := NewService(repo)

	_, err := service.GetByID(context.Background(), "missing")

	if !errors.Is(err, ErrRoomTypeNotFound) {
		t.Fatalf("expected room type not found, got %v", err)
	}
}

func TestServiceListByHotelID(t *testing.T) {
	repo := &fakeRoomRepository{
		listByHotelFn: func(_ context.Context, hotelID string) ([]RoomType, error) {
			if hotelID != "hotel-123" {
				t.Fatalf("expected trimmed hotel ID, got %q", hotelID)
			}

			return []RoomType{validRoomType()}, nil
		},
	}

	service := NewService(repo)

	rooms, err := service.ListByHotelID(context.Background(), " hotel-123 ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(rooms) != 1 {
		t.Fatalf("expected 1 room type, got %d", len(rooms))
	}
}

func TestServiceListByHotelIDRejectsEmptyID(t *testing.T) {
	service := NewService(&fakeRoomRepository{})

	_, err := service.ListByHotelID(context.Background(), " ")

	if !errors.Is(err, ErrInvalidHotelID) {
		t.Fatalf("expected invalid hotel ID, got %v", err)
	}
}

func TestServiceUpdateNotFound(t *testing.T) {
	repo := &fakeRoomRepository{
		updateFn: func(_ context.Context, _ string, _ RoomType) (*RoomType, error) {
			return nil, pgx.ErrNoRows
		},
	}

	service := NewService(repo)

	_, err := service.Update(
		context.Background(),
		"missing",
		validRoomType(),
	)

	if !errors.Is(err, ErrRoomTypeNotFound) {
		t.Fatalf("expected room type not found, got %v", err)
	}
}

func TestServiceUpdateDuplicateRoomType(t *testing.T) {
	repo := &fakeRoomRepository{
		updateFn: func(_ context.Context, _ string, _ RoomType) (*RoomType, error) {
			return nil, &pgconn.PgError{
				Code:           "23505",
				ConstraintName: "uq_room_types_hotel_name",
			}
		},
	}

	service := NewService(repo)

	_, err := service.Update(
		context.Background(),
		"room-123",
		validRoomType(),
	)

	if !errors.Is(err, ErrRoomTypeAlreadyExists) {
		t.Fatalf("expected duplicate room error, got %v", err)
	}
}

func TestServiceUpdate(t *testing.T) {
	repo := &fakeRoomRepository{
		updateFn: func(_ context.Context, id string, roomType RoomType) (*RoomType, error) {
			roomType.ID = id
			return &roomType, nil
		},
	}

	service := NewService(repo)

	input := validRoomType()
	input.Name = "  Premium Room  "

	updated, err := service.Update(context.Background(), "room-123", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if updated.Name != "Premium Room" {
		t.Errorf("expected trimmed name, got %q", updated.Name)
	}
}

func TestServiceUpdateDoesNotRequireHotelID(t *testing.T) {
	repo := &fakeRoomRepository{
		updateFn: func(
			_ context.Context,
			id string,
			roomType RoomType,
		) (*RoomType, error) {
			roomType.ID = id
			roomType.HotelID = "existing-hotel"
			return &roomType, nil
		},
	}

	service := NewService(repo)

	input := validRoomType()
	input.HotelID = ""

	updated, err := service.Update(
		context.Background(),
		"room-123",
		input,
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if updated.HotelID != "existing-hotel" {
		t.Errorf("unexpected hotel ID: %s", updated.HotelID)
	}
}
