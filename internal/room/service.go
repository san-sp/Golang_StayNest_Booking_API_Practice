package room

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrRoomTypeNotFound      = errors.New("room type not found")
	ErrInvalidHotelID        = errors.New("hotel ID is required")
	ErrInvalidRoomName       = errors.New("room name is required")
	ErrRoomNameTooLong       = errors.New("room name must not exceed 100 characters")
	ErrInvalidMaxOccupancy   = errors.New("maximum occupancy must be greater than zero")
	ErrInvalidRoomPrice      = errors.New("room price cannot be negative")
	ErrRoomTypeAlreadyExists = errors.New("room type already exists for this hotel")
	ErrHotelNotFound         = errors.New("hotel not found")
)

type RoomRepository interface {
	Create(ctx context.Context, roomType RoomType) (*RoomType, error)
	GetByID(ctx context.Context, id string) (*RoomType, error)
	ListByHotelID(ctx context.Context, hotelID string) ([]RoomType, error)
	Update(ctx context.Context, id string, roomType RoomType) (*RoomType, error)
}

type Service struct {
	repo RoomRepository
}

func NewService(repo RoomRepository) *Service {
	return &Service{
		repo: repo,
	}
}

// Create validates and creates a room type.
func (s *Service) Create(
	ctx context.Context,
	roomType RoomType,
) (*RoomType, error) {
	roomType.HotelID = strings.TrimSpace(roomType.HotelID)
	roomType.Name = strings.TrimSpace(roomType.Name)
	roomType.Description = strings.TrimSpace(roomType.Description)

	if err := validateRoomType(roomType); err != nil {
		return nil, err
	}

	created, err := s.repo.Create(ctx, roomType)
	if err != nil {
		if isDuplicateRoomTypeError(err) {
			return nil, ErrRoomTypeAlreadyExists
		}

		if isForeignKeyViolation(err) {
			return nil, ErrHotelNotFound
		}

		return nil, fmt.Errorf("create room type: %w", err)
	}

	return created, nil
}

// GetByID retrieves a room type by ID.
func (s *Service) GetByID(
	ctx context.Context,
	id string,
) (*RoomType, error) {
	found, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRoomTypeNotFound
		}

		return nil, fmt.Errorf("get room type by ID: %w", err)
	}

	return found, nil
}

// ListByHotelID retrieves room types belonging to a hotel.
func (s *Service) ListByHotelID(
	ctx context.Context,
	hotelID string,
) ([]RoomType, error) {
	hotelID = strings.TrimSpace(hotelID)

	if hotelID == "" {
		return nil, ErrInvalidHotelID
	}

	roomTypes, err := s.repo.ListByHotelID(ctx, hotelID)
	if err != nil {
		return nil, fmt.Errorf("list room types by hotel ID: %w", err)
	}

	return roomTypes, nil
}

// Update validates and updates an existing room type.
func (s *Service) Update(
	ctx context.Context,
	id string,
	roomType RoomType,
) (*RoomType, error) {
	roomType.Name = strings.TrimSpace(roomType.Name)
	roomType.Description = strings.TrimSpace(roomType.Description)

	if err := validateRoomTypeFields(roomType); err != nil {
		return nil, err
	}

	updated, err := s.repo.Update(ctx, id, roomType)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRoomTypeNotFound
		}

		if isDuplicateRoomTypeError(err) {
			return nil, ErrRoomTypeAlreadyExists
		}

		return nil, fmt.Errorf("update room type: %w", err)
	}

	return updated, nil
}

func validateRoomType(roomType RoomType) error {
	if roomType.HotelID == "" {
		return ErrInvalidHotelID
	}

	return validateRoomTypeFields(roomType)
}

func validateRoomTypeFields(roomType RoomType) error {
	if roomType.Name == "" {
		return ErrInvalidRoomName
	}

	if utf8.RuneCountInString(roomType.Name) > 100 {
		return ErrRoomNameTooLong
	}

	if roomType.MaxOccupancy <= 0 {
		return ErrInvalidMaxOccupancy
	}

	if roomType.PricePerNightPaise < 0 {
		return ErrInvalidRoomPrice
	}

	return nil
}

func isDuplicateRoomTypeError(err error) bool {
	var pgErr *pgconn.PgError

	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == "uq_room_types_hotel_name"
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError

	return errors.As(err, &pgErr) &&
		pgErr.Code == "23503"
}
