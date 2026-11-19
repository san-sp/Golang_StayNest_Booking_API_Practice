package inventory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidRoomTypeID   = errors.New("room type ID is required")
	ErrInvalidStayDate     = errors.New("stay date is required")
	ErrInvalidTotalRooms   = errors.New("total rooms cannot be negative")
	ErrInvalidDateRange    = errors.New("start date must be before end date")
	ErrCapacityBelowBooked = errors.New("total rooms cannot be lower than already booked rooms")
	ErrRoomTypeNotFound    = errors.New("room type not found")
)

type InventoryRepository interface {
	SetDailyCapacity(
		ctx context.Context,
		roomTypeID string,
		stayDate time.Time,
		totalRooms int,
	) (*DailyInventory, error)

	ListByRoomTypeAndDateRange(
		ctx context.Context,
		roomTypeID string,
		startDate time.Time,
		endDate time.Time,
	) ([]DailyInventory, error)
}

type Service struct {
	repo InventoryRepository
}

func NewService(repo InventoryRepository) *Service {
	return &Service{
		repo: repo,
	}
}

// SetDailyCapacity creates or updates inventory for one room type and date.
func (s *Service) SetDailyCapacity(
	ctx context.Context,
	roomTypeID string,
	stayDate time.Time,
	totalRooms int,
) (*DailyInventory, error) {
	roomTypeID = strings.TrimSpace(roomTypeID)

	if roomTypeID == "" {
		return nil, ErrInvalidRoomTypeID
	}

	if stayDate.IsZero() {
		return nil, ErrInvalidStayDate
	}

	if totalRooms < 0 {
		return nil, ErrInvalidTotalRooms
	}

	// PostgreSQL DATE does not store time-of-day information.
	stayDate = dateOnly(stayDate)

	inventory, err := s.repo.SetDailyCapacity(
		ctx,
		roomTypeID,
		stayDate,
		totalRooms,
	)
	if err != nil {
		if errors.Is(err, ErrCapacityBelowBooked) {
			return nil, ErrCapacityBelowBooked
		}

		if errors.Is(err, ErrRoomTypeNotFound) {
			return nil, ErrRoomTypeNotFound
		}

		return nil, fmt.Errorf("set daily inventory capacity: %w", err)
	}

	return inventory, nil
}

// ListByRoomTypeAndDateRange retrieves configured daily inventory.
// The date range is start-inclusive and end-exclusive.
func (s *Service) ListByRoomTypeAndDateRange(
	ctx context.Context,
	roomTypeID string,
	startDate time.Time,
	endDate time.Time,
) ([]DailyInventory, error) {
	roomTypeID = strings.TrimSpace(roomTypeID)

	if roomTypeID == "" {
		return nil, ErrInvalidRoomTypeID
	}

	if startDate.IsZero() || endDate.IsZero() {
		return nil, ErrInvalidDateRange
	}

	startDate = dateOnly(startDate)
	endDate = dateOnly(endDate)

	if !startDate.Before(endDate) {
		return nil, ErrInvalidDateRange
	}

	inventory, err := s.repo.ListByRoomTypeAndDateRange(
		ctx,
		roomTypeID,
		startDate,
		endDate,
	)
	if err != nil {
		if errors.Is(err, ErrRoomTypeNotFound) {
			return nil, ErrRoomTypeNotFound
		}

		return nil, fmt.Errorf("list daily inventory: %w", err)
	}

	return inventory, nil
}

// dateOnly normalizes a date to midnight UTC.
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
