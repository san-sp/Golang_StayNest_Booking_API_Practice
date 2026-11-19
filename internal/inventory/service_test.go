package inventory

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeInventoryRepository struct {
	setResult *DailyInventory
	setErr    error
	setCalled bool
	setID     string
	setDate   time.Time
	setTotal  int

	listResult []DailyInventory
	listErr    error
	listCalled bool
	listID     string
	listStart  time.Time
	listEnd    time.Time
}

func (f *fakeInventoryRepository) SetDailyCapacity(
	ctx context.Context,
	roomTypeID string,
	stayDate time.Time,
	totalRooms int,
) (*DailyInventory, error) {
	f.setCalled = true
	f.setID = roomTypeID
	f.setDate = stayDate
	f.setTotal = totalRooms

	return f.setResult, f.setErr
}

func (f *fakeInventoryRepository) ListByRoomTypeAndDateRange(
	ctx context.Context,
	roomTypeID string,
	startDate time.Time,
	endDate time.Time,
) ([]DailyInventory, error) {
	f.listCalled = true
	f.listID = roomTypeID
	f.listStart = startDate
	f.listEnd = endDate

	return f.listResult, f.listErr
}

func TestSetDailyCapacitySuccess(t *testing.T) {
	repo := &fakeInventoryRepository{
		setResult: &DailyInventory{
			RoomTypeID:     "room-123",
			TotalRooms:     10,
			AvailableRooms: 10,
		},
	}

	service := NewService(repo)

	stayDate := time.Date(
		2026, 11, 15, 14, 30, 0, 0, time.FixedZone("IST", 19800),
	)

	result, err := service.SetDailyCapacity(
		context.Background(),
		" room-123 ",
		stayDate,
		10,
	)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result == nil {
		t.Fatal("expected inventory result")
	}

	if !repo.setCalled {
		t.Fatal("expected repository to be called")
	}

	if repo.setID != "room-123" {
		t.Errorf("expected trimmed room ID, got %q", repo.setID)
	}

	if repo.setTotal != 10 {
		t.Errorf("expected total rooms 10, got %d", repo.setTotal)
	}

	if repo.setDate.Hour() != 0 || repo.setDate.Location() != time.UTC {
		t.Errorf("expected normalized UTC date, got %v", repo.setDate)
	}
}

func TestSetDailyCapacityValidation(t *testing.T) {
	tests := []struct {
		name       string
		roomTypeID string
		stayDate   time.Time
		totalRooms int
		wantErr    error
	}{
		{
			name:       "missing room type ID",
			roomTypeID: " ",
			stayDate:   time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
			totalRooms: 10,
			wantErr:    ErrInvalidRoomTypeID,
		},
		{
			name:       "missing stay date",
			roomTypeID: "room-123",
			totalRooms: 10,
			wantErr:    ErrInvalidStayDate,
		},
		{
			name:       "negative total rooms",
			roomTypeID: "room-123",
			stayDate:   time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
			totalRooms: -1,
			wantErr:    ErrInvalidTotalRooms,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeInventoryRepository{}
			service := NewService(repo)

			_, err := service.SetDailyCapacity(
				context.Background(),
				tt.roomTypeID,
				tt.stayDate,
				tt.totalRooms,
			)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}

			if repo.setCalled {
				t.Fatal("repository must not be called for invalid input")
			}
		})
	}
}

func TestSetDailyCapacityRepositoryErrors(t *testing.T) {
	tests := []struct {
		name    string
		repoErr error
		wantErr error
	}{
		{
			name:    "capacity below booked rooms",
			repoErr: ErrCapacityBelowBooked,
			wantErr: ErrCapacityBelowBooked,
		},
		{
			name:    "room type not found",
			repoErr: ErrRoomTypeNotFound,
			wantErr: ErrRoomTypeNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeInventoryRepository{
				setErr: tt.repoErr,
			}

			service := NewService(repo)

			_, err := service.SetDailyCapacity(
				context.Background(),
				"room-123",
				time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
				10,
			)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestListByRoomTypeAndDateRangeSuccess(t *testing.T) {
	repo := &fakeInventoryRepository{
		listResult: []DailyInventory{
			{
				RoomTypeID:     "room-123",
				TotalRooms:     10,
				AvailableRooms: 7,
			},
		},
	}

	service := NewService(repo)

	start := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)

	result, err := service.ListByRoomTypeAndDateRange(
		context.Background(),
		" room-123 ",
		start,
		end,
	)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 inventory record, got %d", len(result))
	}

	if !repo.listCalled {
		t.Fatal("expected repository to be called")
	}

	if repo.listID != "room-123" {
		t.Errorf("expected trimmed room ID, got %q", repo.listID)
	}
}

func TestListByRoomTypeAndDateRangeValidation(t *testing.T) {
	tests := []struct {
		name       string
		roomTypeID string
		start      time.Time
		end        time.Time
		wantErr    error
	}{
		{
			name:    "missing room type ID",
			start:   time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
			end:     time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
			wantErr: ErrInvalidRoomTypeID,
		},
		{
			name:       "missing start date",
			roomTypeID: "room-123",
			end:        time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
			wantErr:    ErrInvalidDateRange,
		},
		{
			name:       "end before start",
			roomTypeID: "room-123",
			start:      time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
			end:        time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
			wantErr:    ErrInvalidDateRange,
		},
		{
			name:       "same start and end",
			roomTypeID: "room-123",
			start:      time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
			end:        time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
			wantErr:    ErrInvalidDateRange,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeInventoryRepository{}
			service := NewService(repo)

			_, err := service.ListByRoomTypeAndDateRange(
				context.Background(),
				tt.roomTypeID,
				tt.start,
				tt.end,
			)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}

			if repo.listCalled {
				t.Fatal("repository must not be called for invalid input")
			}
		})
	}
}

func TestListByRoomTypeAndDateRangeRepositoryError(t *testing.T) {
	repo := &fakeInventoryRepository{
		listErr: ErrRoomTypeNotFound,
	}

	service := NewService(repo)

	_, err := service.ListByRoomTypeAndDateRange(
		context.Background(),
		"room-123",
		time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
	)

	if !errors.Is(err, ErrRoomTypeNotFound) {
		t.Fatalf("expected room type not found, got %v", err)
	}
}
