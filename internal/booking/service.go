package booking

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

var (
	ErrInvalidCustomerID  = errors.New("customer ID is required")
	ErrInvalidRoomTypeID  = errors.New("room type ID is required")
	ErrInvalidCheckIn     = errors.New("check-in date is required")
	ErrInvalidCheckOut    = errors.New("check-out date is required")
	ErrInvalidDateRange   = errors.New("check-out must be after check-in")
	ErrInvalidRoomsCount  = errors.New("rooms count must be greater than zero")
	ErrInvalidGuestsCount = errors.New("guests count must be greater than zero")

	ErrRoomTypeNotFound             = errors.New("room type not found")
	ErrGuestCapacityExceeded        = errors.New("guest count exceeds room capacity")
	ErrInventoryNotConfigured       = errors.New("inventory is not configured for the requested dates")
	ErrInsufficientAvailability     = errors.New("insufficient room availability")
	ErrBookingAmountOverflow        = errors.New("booking amount exceeds the supported limit")
	ErrInvalidBookingID             = errors.New("booking ID is required")
	ErrBookingNotFound              = errors.New("booking not found")
	ErrBookingInventoryInconsistent = errors.New("booking inventory is inconsistent")
	ErrInvalidBookingStatus         = errors.New("invalid booking status")
	ErrInvalidFilterRange           = errors.New("invalid booking date filter range")
	ErrInvalidPagination            = errors.New("invalid pagination parameters")
	ErrBookingNotModifiable         = errors.New("booking cannot be modified")
	ErrInvalidBookingCount          = errors.New("booking count exceeds the supported limit")
	ErrBookingDateInPast            = errors.New("booking check-in date cannot be in the past")
)

type BookingRepository interface {
	Create(ctx context.Context, booking Booking) (*Booking, error)
	Modify(
		ctx context.Context,
		customerID string,
		bookingID string,
		input ModifyInput,
	) (*Booking, error)
	Cancel(ctx context.Context, customerID string, bookingID string) (*Booking, error)

	GetByID(ctx context.Context, customerID string, bookingID string) (*Booking, error)
	ListByCustomerID(
		ctx context.Context,
		customerID string,
		filter ListFilter,
	) (ListResult, error)
}

const DefaultPaymentWindow = 15 * time.Minute

type Service struct {
	repo          BookingRepository
	paymentWindow time.Duration
	now           func() time.Time
}

func NewService(repo BookingRepository) *Service {
	return NewServiceWithClock(
		repo,
		DefaultPaymentWindow,
		time.Now,
	)
}

// NewServiceWithClock allows the payment window and clock
// to be controlled in tests.
func NewServiceWithClock(
	repo BookingRepository,
	paymentWindow time.Duration,
	now func() time.Time,
) *Service {
	if paymentWindow <= 0 {
		paymentWindow = DefaultPaymentWindow
	}

	if now == nil {
		now = time.Now
	}

	return &Service{
		repo:          repo,
		paymentWindow: paymentWindow,
		now:           now,
	}
}

// Create validates the booking request and delegates the
// transactional booking operation to the repository.
func (s *Service) Create(
	ctx context.Context,
	customerID string,
	input CreateInput,
) (*Booking, error) {
	customerID = strings.TrimSpace(customerID)
	input.RoomTypeID = strings.TrimSpace(input.RoomTypeID)

	if customerID == "" {
		return nil, ErrInvalidCustomerID
	}

	if input.RoomTypeID == "" {
		return nil, ErrInvalidRoomTypeID
	}

	if input.CheckIn.IsZero() {
		return nil, ErrInvalidCheckIn
	}

	if input.CheckOut.IsZero() {
		return nil, ErrInvalidCheckOut
	}

	checkIn := dateOnly(input.CheckIn)
	checkOut := dateOnly(input.CheckOut)

	if !checkOut.After(checkIn) {
		return nil, ErrInvalidDateRange
	}

	if input.RoomsCount <= 0 {
		return nil, ErrInvalidRoomsCount
	}

	if input.GuestsCount <= 0 {
		return nil, ErrInvalidGuestsCount
	}

	if int64(input.RoomsCount) > math.MaxInt32 ||
		int64(input.GuestsCount) > math.MaxInt32 {
		return nil, ErrInvalidBookingCount
	}

	reference, err := generateBookingReference()
	if err != nil {
		return nil, fmt.Errorf("generate booking reference: %w", err)
	}

	paymentExpiresAt := s.now().UTC().Add(s.paymentWindow)

	booking := Booking{
		BookingReference: reference,
		CustomerID:       customerID,
		RoomTypeID:       input.RoomTypeID,
		CheckIn:          checkIn,
		CheckOut:         checkOut,
		RoomsCount:       input.RoomsCount,
		GuestsCount:      input.GuestsCount,
		Status:           StatusPendingPayment,
		PaymentExpiresAt: &paymentExpiresAt,
	}

	created, err := s.repo.Create(ctx, booking)
	if err != nil {
		if errors.Is(err, ErrRoomTypeNotFound) ||
			errors.Is(err, ErrGuestCapacityExceeded) ||
			errors.Is(err, ErrInventoryNotConfigured) ||
			errors.Is(err, ErrInsufficientAvailability) ||
			errors.Is(err, ErrBookingDateInPast) ||
			errors.Is(err, ErrBookingAmountOverflow) {
			return nil, err
		}

		return nil, fmt.Errorf("create booking: %w", err)
	}

	return created, nil
}

// GetByID retrieves a booking belonging to the authenticated customer.
func (s *Service) GetByID(
	ctx context.Context,
	customerID string,
	bookingID string,
) (*Booking, error) {
	customerID = strings.TrimSpace(customerID)
	bookingID = strings.TrimSpace(bookingID)

	if customerID == "" {
		return nil, ErrInvalidCustomerID
	}

	if bookingID == "" {
		return nil, ErrInvalidBookingID
	}

	found, err := s.repo.GetByID(ctx, customerID, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, err
		}

		return nil, fmt.Errorf("get booking: %w", err)
	}

	return found, nil
}

// ListByCustomerID retrieves filtered and paginated bookings
// belonging to the authenticated customer.
func (s *Service) ListByCustomerID(
	ctx context.Context,
	customerID string,
	filter ListFilter,
) (ListResult, error) {
	customerID = strings.TrimSpace(customerID)
	filter.Status = strings.ToLower(strings.TrimSpace(filter.Status))

	if customerID == "" {
		return ListResult{}, ErrInvalidCustomerID
	}

	if filter.Status != "" &&
		filter.Status != StatusPendingPayment &&
		filter.Status != StatusConfirmed &&
		filter.Status != StatusCancelled &&
		filter.Status != StatusExpired {
		return ListResult{}, ErrInvalidBookingStatus
	}

	if filter.Limit == 0 {
		filter.Limit = 10
	}

	if filter.Limit < 1 || filter.Limit > 100 || filter.Offset < 0 {
		return ListResult{}, ErrInvalidPagination
	}

	if filter.CheckInFrom != nil {
		value := dateOnly(*filter.CheckInFrom)
		filter.CheckInFrom = &value
	}

	if filter.CheckInTo != nil {
		value := dateOnly(*filter.CheckInTo)
		filter.CheckInTo = &value
	}

	if filter.CheckOutFrom != nil {
		value := dateOnly(*filter.CheckOutFrom)
		filter.CheckOutFrom = &value
	}

	if filter.CheckOutTo != nil {
		value := dateOnly(*filter.CheckOutTo)
		filter.CheckOutTo = &value
	}

	if filter.CheckInFrom != nil &&
		filter.CheckInTo != nil &&
		filter.CheckInTo.Before(*filter.CheckInFrom) {
		return ListResult{}, ErrInvalidFilterRange
	}

	if filter.CheckOutFrom != nil &&
		filter.CheckOutTo != nil &&
		filter.CheckOutTo.Before(*filter.CheckOutFrom) {
		return ListResult{}, ErrInvalidFilterRange
	}

	result, err := s.repo.ListByCustomerID(ctx, customerID, filter)
	if err != nil {
		return ListResult{}, fmt.Errorf("list customer bookings: %w", err)
	}

	return result, nil
}

// Modify validates and modifies a confirmed booking.
func (s *Service) Modify(
	ctx context.Context,
	customerID string,
	bookingID string,
	input ModifyInput,
) (*Booking, error) {
	customerID = strings.TrimSpace(customerID)
	bookingID = strings.TrimSpace(bookingID)

	if customerID == "" {
		return nil, ErrInvalidCustomerID
	}

	if bookingID == "" {
		return nil, ErrInvalidBookingID
	}

	if input.CheckIn.IsZero() {
		return nil, ErrInvalidCheckIn
	}

	if input.CheckOut.IsZero() {
		return nil, ErrInvalidCheckOut
	}

	checkIn := dateOnly(input.CheckIn)
	checkOut := dateOnly(input.CheckOut)

	if !checkOut.After(checkIn) {
		return nil, ErrInvalidDateRange
	}

	if input.RoomsCount <= 0 {
		return nil, ErrInvalidRoomsCount
	}

	if input.GuestsCount <= 0 {
		return nil, ErrInvalidGuestsCount
	}

	if int64(input.RoomsCount) > math.MaxInt32 ||
		int64(input.GuestsCount) > math.MaxInt32 {
		return nil, ErrInvalidBookingCount
	}

	input.CheckIn = checkIn
	input.CheckOut = checkOut

	modified, err := s.repo.Modify(
		ctx,
		customerID,
		bookingID,
		input,
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrBookingNotFound),
			errors.Is(err, ErrBookingNotModifiable),
			errors.Is(err, ErrBookingInventoryInconsistent),
			errors.Is(err, ErrGuestCapacityExceeded),
			errors.Is(err, ErrInventoryNotConfigured),
			errors.Is(err, ErrInsufficientAvailability),
			errors.Is(err, ErrBookingDateInPast),
			errors.Is(err, ErrBookingAmountOverflow):
			return nil, err
		default:
			return nil, fmt.Errorf("modify booking: %w", err)
		}
	}

	return modified, nil
}

// Cancel cancels a booking belonging to the authenticated customer.
func (s *Service) Cancel(
	ctx context.Context,
	customerID string,
	bookingID string,
) (*Booking, error) {
	customerID = strings.TrimSpace(customerID)
	bookingID = strings.TrimSpace(bookingID)

	if customerID == "" {
		return nil, ErrInvalidCustomerID
	}

	if bookingID == "" {
		return nil, ErrInvalidBookingID
	}

	cancelled, err := s.repo.Cancel(ctx, customerID, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) ||
			errors.Is(err, ErrBookingInventoryInconsistent) {
			return nil, err
		}

		return nil, fmt.Errorf("cancel booking: %w", err)
	}

	return cancelled, nil
}

// dateOnly normalizes a date to UTC midnight.
func dateOnly(value time.Time) time.Time {
	year, month, day := value.Date()

	return time.Date(
		year,
		month,
		day,
		0,
		0,
		0,
		0,
		time.UTC,
	)
}

// generateBookingReference creates a non-guessable reference.
// The database UNIQUE constraint remains the final protection
// against the extremely unlikely possibility of a collision.
func generateBookingReference() (string, error) {
	randomBytes := make([]byte, 10)

	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}

	return "STN-" + strings.ToUpper(hex.EncodeToString(randomBytes)), nil
}
