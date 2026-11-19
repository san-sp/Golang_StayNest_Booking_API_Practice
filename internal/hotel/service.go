package hotel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

var (
	ErrHotelNotFound          = errors.New("hotel not found")
	ErrInvalidHotelName       = errors.New("hotel name is required")
	ErrHotelNameTooLong       = errors.New("hotel name must not exceed 150 characters")
	ErrInvalidHotelAddress    = errors.New("hotel address is required")
	ErrInvalidHotelCity       = errors.New("hotel city is required")
	ErrHotelCityTooLong       = errors.New("hotel city must not exceed 100 characters")
	ErrInvalidHotelCountry    = errors.New("hotel country is required")
	ErrHotelCountryTooLong    = errors.New("hotel country must not exceed 100 characters")
	ErrInvalidHotelListOffset = errors.New("hotel list offset cannot be negative")
	ErrInvalidHotelListLimit  = errors.New("hotel list limit must be positive")
	ErrInvalidSearchCity      = errors.New("search city is required")
	ErrInvalidSearchCheckIn   = errors.New("check-in date is required")
	ErrInvalidSearchCheckOut  = errors.New("check-out date is required")
	ErrInvalidSearchDateRange = errors.New("check-out must be after check-in")
	ErrInvalidSearchGuests    = errors.New("guest count must be positive")
	ErrInvalidSearchRooms     = errors.New("room count must be positive")
	ErrInvalidHotelTimezone   = errors.New("a valid IANA hotel timezone is required")
)

const (
	defaultHotelListLimit = 20
	maxHotelListLimit     = 100
)

type HotelRepository interface {
	Create(ctx context.Context, hotel Hotel) (*Hotel, error)
	GetByID(ctx context.Context, id string) (*Hotel, error)
	List(ctx context.Context, params ListParams) (*ListResult, error)
	Update(ctx context.Context, id string, hotel Hotel) (*Hotel, error)

	Search(ctx context.Context, params SearchParams) ([]SearchResult, error)
}

type Service struct {
	repo HotelRepository
}

func NewService(repo HotelRepository) *Service {
	return &Service{
		repo: repo,
	}
}

func normalizeHotel(hotel *Hotel) {
	hotel.Name = strings.TrimSpace(hotel.Name)
	hotel.Description = strings.TrimSpace(hotel.Description)
	hotel.Address = strings.TrimSpace(hotel.Address)
	hotel.City = strings.TrimSpace(hotel.City)
	hotel.Country = strings.TrimSpace(hotel.Country)
	hotel.Timezone = strings.TrimSpace(hotel.Timezone)
}

// Create validates hotel details and creates a hotel.
func (s *Service) Create(
	ctx context.Context,
	hotel Hotel,
) (*Hotel, error) {
	normalizeHotel(&hotel)

	if err := validateHotel(hotel); err != nil {
		return nil, err
	}

	created, err := s.repo.Create(ctx, hotel)
	if err != nil {
		return nil, fmt.Errorf("create hotel: %w", err)
	}

	return created, nil
}

// GetByID retrieves a hotel by its ID.
func (s *Service) GetByID(
	ctx context.Context,
	id string,
) (*Hotel, error) {
	found, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrHotelNotFound
		}

		return nil, fmt.Errorf("get hotel by ID: %w", err)
	}

	return found, nil
}

// Update validates and updates an existing hotel.
func (s *Service) Update(
	ctx context.Context,
	id string,
	hotel Hotel,
) (*Hotel, error) {
	normalizeHotel(&hotel)

	if err := validateHotel(hotel); err != nil {
		return nil, err
	}

	updated, err := s.repo.Update(ctx, id, hotel)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrHotelNotFound
		}

		return nil, fmt.Errorf("update hotel: %w", err)
	}

	return updated, nil
}

func validateHotel(hotel Hotel) error {
	if hotel.Name == "" {
		return ErrInvalidHotelName
	}

	if utf8.RuneCountInString(hotel.Name) > 150 {
		return ErrHotelNameTooLong
	}

	if hotel.Address == "" {
		return ErrInvalidHotelAddress
	}

	if hotel.City == "" {
		return ErrInvalidHotelCity
	}

	if utf8.RuneCountInString(hotel.City) > 100 {
		return ErrHotelCityTooLong
	}

	if hotel.Country == "" {
		return ErrInvalidHotelCountry
	}

	if utf8.RuneCountInString(hotel.Country) > 100 {
		return ErrHotelCountryTooLong
	}

	if hotel.Timezone == "" {
		return ErrInvalidHotelTimezone
	}

	if _, err := time.LoadLocation(hotel.Timezone); err != nil {
		return ErrInvalidHotelTimezone
	}

	return nil
}

func (s *Service) List(
	ctx context.Context,
	params ListParams,
) (*ListResult, error) {
	params.City = strings.TrimSpace(params.City)

	if params.Limit < 0 {
		return nil, ErrInvalidHotelListLimit
	}

	if params.Limit == 0 {
		params.Limit = defaultHotelListLimit
	}

	if params.Limit > maxHotelListLimit {
		params.Limit = maxHotelListLimit
	}

	if params.Offset < 0 {
		return nil, ErrInvalidHotelListOffset
	}

	result, err := s.repo.List(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list hotels: %w", err)
	}

	result.Limit = params.Limit
	result.Offset = params.Offset

	return result, nil
}

// Search validates the search criteria and retrieves available hotels.
func (s *Service) Search(
	ctx context.Context,
	params SearchParams,
) ([]SearchResult, error) {
	params.City = strings.TrimSpace(params.City)

	if params.City == "" {
		return nil, ErrInvalidSearchCity
	}

	if params.CheckIn.IsZero() {
		return nil, ErrInvalidSearchCheckIn
	}

	if params.CheckOut.IsZero() {
		return nil, ErrInvalidSearchCheckOut
	}

	params.CheckIn = normalizeSearchDate(params.CheckIn)
	params.CheckOut = normalizeSearchDate(params.CheckOut)

	if !params.CheckOut.After(params.CheckIn) {
		return nil, ErrInvalidSearchDateRange
	}

	if params.Guests <= 0 {
		return nil, ErrInvalidSearchGuests
	}

	if params.Rooms <= 0 {
		return nil, ErrInvalidSearchRooms
	}

	results, err := s.repo.Search(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("search available hotels: %w", err)
	}

	return results, nil
}

// normalizeSearchDate converts a date to UTC midnight.
// Hotel searches operate on calendar dates, not times of day.
func normalizeSearchDate(value time.Time) time.Time {
	year, month, day := value.Date()

	return time.Date(
		year,
		month,
		day,
		0, 0, 0, 0,
		time.UTC,
	)
}
