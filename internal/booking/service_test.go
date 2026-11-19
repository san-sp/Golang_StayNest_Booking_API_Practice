package booking

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

type fakeBookingRepository struct {
	booking          Booking
	result           *Booking
	err              error
	called           bool
	cancelledBooking *Booking
	cancelErr        error
	cancelCalled     bool
	cancelCustomerID string
	cancelBookingID  string
	modifyInput      ModifyInput
	modifyCalled     bool
	modifyCustomerID string
	modifyBookingID  string
	modifyResult     *Booking
	modifyErr        error
}

func (f *fakeBookingRepository) Create(
	ctx context.Context,
	booking Booking,
) (*Booking, error) {
	f.called = true
	f.booking = booking

	if f.err != nil {
		return nil, f.err
	}

	if f.result != nil {
		return f.result, nil
	}

	created := booking
	created.ID = "booking-123"
	created.CreatedAt = time.Now().UTC()
	created.UpdatedAt = created.CreatedAt

	return &created, nil
}

func (f *fakeBookingRepository) Modify(
	ctx context.Context,
	customerID string,
	bookingID string,
	input ModifyInput,
) (*Booking, error) {
	f.modifyCalled = true
	f.modifyCustomerID = customerID
	f.modifyBookingID = bookingID
	f.modifyInput = input

	if f.modifyErr != nil {
		return nil, f.modifyErr
	}

	return f.modifyResult, nil
}

func validBookingInput() CreateInput {
	checkIn := time.Now().UTC().AddDate(0, 0, 10)
	checkIn = dateOnly(checkIn)

	return CreateInput{
		RoomTypeID:  "room-123",
		CheckIn:     checkIn,
		CheckOut:    checkIn.AddDate(0, 0, 3),
		RoomsCount:  2,
		GuestsCount: 4,
	}
}

func (f *fakeBookingRepository) Cancel(
	ctx context.Context,
	customerID string,
	bookingID string,
) (*Booking, error) {
	f.cancelCalled = true
	f.cancelCustomerID = customerID
	f.cancelBookingID = bookingID

	if f.cancelErr != nil {
		return nil, f.cancelErr
	}

	return f.cancelledBooking, nil
}

func (f *fakeBookingRepository) GetByID(
	ctx context.Context,
	customerID string,
	bookingID string,
) (*Booking, error) {
	return nil, nil
}

func (f *fakeBookingRepository) ListByCustomerID(
	ctx context.Context,
	customerID string,
	filter ListFilter,
) (ListResult, error) {
	return ListResult{
		Bookings: []Booking{},
		Total:    0,
		Limit:    filter.Limit,
		Offset:   filter.Offset,
	}, nil
}

func TestServiceCreateSuccess(t *testing.T) {
	repo := &fakeBookingRepository{}
	service := NewService(repo)

	input := validBookingInput()

	created, err := service.Create(
		context.Background(),
		" customer-123 ",
		input,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if created.ID != "booking-123" {
		t.Errorf("expected booking ID booking-123, got %q", created.ID)
	}

	if created.CustomerID != "customer-123" {
		t.Errorf("expected trimmed customer ID, got %q", created.CustomerID)
	}

	if created.Status != StatusPendingPayment {
		t.Fatalf(
			"expected status %q, got %q",
			StatusPendingPayment,
			created.Status,
		)
	}

	expectedExpiry := time.Now().UTC().Add(DefaultPaymentWindow)

	if created.PaymentExpiresAt.Before(expectedExpiry.Add(-time.Second)) ||
		created.PaymentExpiresAt.After(expectedExpiry.Add(time.Second)) {
		t.Fatalf(
			"unexpected payment expiration time: got %v",
			created.PaymentExpiresAt,
		)
	}

	if created.PaymentExpiresAt == nil {
		t.Fatal("expected payment expiration time to be set")
	}

	if !strings.HasPrefix(created.BookingReference, "STN-") {
		t.Errorf("invalid booking reference: %q", created.BookingReference)
	}

	if created.RoomTypeID != "room-123" {
		t.Errorf("expected room type ID room-123, got %q", created.RoomTypeID)
	}

	if !repo.called {
		t.Fatal("expected repository to be called")
	}

	if repo.booking.PricePerNightPaise != 0 {
		t.Error("service must not accept or assign the booking price")
	}

	if !repo.booking.CheckIn.Equal(dateOnly(input.CheckIn)) {
		t.Errorf("check-in was not normalized: %v", repo.booking.CheckIn)
	}

	if !repo.booking.CheckOut.Equal(dateOnly(input.CheckOut)) {
		t.Errorf("check-out was not normalized: %v", repo.booking.CheckOut)
	}
}

func TestServiceCreateNormalizesISTDates(t *testing.T) {
	repo := &fakeBookingRepository{}
	service := NewService(repo)

	ist := time.FixedZone("IST", 5*60*60+30*60)

	input := validBookingInput()
	input.CheckIn = time.Date(
		2026, time.November, 14,
		15, 30, 0, 0,
		ist,
	)
	input.CheckOut = time.Date(
		2026, time.November, 17,
		11, 45, 0, 0,
		ist,
	)

	_, err := service.Create(
		context.Background(),
		"customer-123",
		input,
	)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	expectedCheckIn := time.Date(
		2026, time.November, 14,
		0, 0, 0, 0,
		time.UTC,
	)
	expectedCheckOut := time.Date(
		2026, time.November, 17,
		0, 0, 0, 0,
		time.UTC,
	)

	if !repo.booking.CheckIn.Equal(expectedCheckIn) {
		t.Errorf(
			"expected check-in %v, got %v",
			expectedCheckIn,
			repo.booking.CheckIn,
		)
	}

	if !repo.booking.CheckOut.Equal(expectedCheckOut) {
		t.Errorf(
			"expected check-out %v, got %v",
			expectedCheckOut,
			repo.booking.CheckOut,
		)
	}

	if repo.booking.CheckIn.Location() != time.UTC {
		t.Error("expected check-in location to be UTC")
	}

	if repo.booking.CheckOut.Location() != time.UTC {
		t.Error("expected check-out location to be UTC")
	}
}

func TestServiceModifyNormalizesISTDates(t *testing.T) {
	repo := &fakeBookingRepository{
		modifyResult: &Booking{
			ID: "booking-123",
		},
	}
	service := NewService(repo)

	ist := time.FixedZone("IST", 5*60*60+30*60)

	input := validModifyInput()
	input.CheckIn = time.Date(
		2026, time.December, 31,
		18, 30, 0, 0,
		ist,
	)
	input.CheckOut = time.Date(
		2027, time.January, 3,
		10, 15, 0, 0,
		ist,
	)

	_, err := service.Modify(
		context.Background(),
		"customer-123",
		"booking-123",
		input,
	)
	if err != nil {
		t.Fatalf("Modify() error = %v", err)
	}

	expectedCheckIn := time.Date(
		2026, time.December, 31,
		0, 0, 0, 0,
		time.UTC,
	)
	expectedCheckOut := time.Date(
		2027, time.January, 3,
		0, 0, 0, 0,
		time.UTC,
	)

	if !repo.modifyInput.CheckIn.Equal(expectedCheckIn) {
		t.Errorf("unexpected check-in: %v", repo.modifyInput.CheckIn)
	}

	if !repo.modifyInput.CheckOut.Equal(expectedCheckOut) {
		t.Errorf("unexpected check-out: %v", repo.modifyInput.CheckOut)
	}
}

func TestServiceCreateCalendarDateBoundaries(t *testing.T) {
	tests := []struct {
		name           string
		checkIn        time.Time
		checkOut       time.Time
		expectedNights int
	}{
		{
			name: "leap day 2024",
			checkIn: time.Date(
				2024, time.February, 28, 0, 0, 0, 0, time.UTC,
			),
			checkOut: time.Date(
				2024, time.February, 29, 0, 0, 0, 0, time.UTC,
			),
			expectedNights: 1,
		},
		{
			name: "leap day to march",
			checkIn: time.Date(
				2024, time.February, 29, 0, 0, 0, 0, time.UTC,
			),
			checkOut: time.Date(
				2024, time.March, 1, 0, 0, 0, 0, time.UTC,
			),
			expectedNights: 1,
		},
		{
			name: "non leap year february",
			checkIn: time.Date(
				2025, time.February, 28, 0, 0, 0, 0, time.UTC,
			),
			checkOut: time.Date(
				2025, time.March, 1, 0, 0, 0, 0, time.UTC,
			),
			expectedNights: 1,
		},
		{
			name: "december to january",
			checkIn: time.Date(
				2026, time.December, 31, 0, 0, 0, 0, time.UTC,
			),
			checkOut: time.Date(
				2027, time.January, 1, 0, 0, 0, 0, time.UTC,
			),
			expectedNights: 1,
		},
		{
			name: "three nights across month boundary",
			checkIn: time.Date(
				2026, time.November, 29, 0, 0, 0, 0, time.UTC,
			),
			checkOut: time.Date(
				2026, time.December, 2, 0, 0, 0, 0, time.UTC,
			),
			expectedNights: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeBookingRepository{}
			service := NewService(repo)

			input := validBookingInput()
			input.CheckIn = tt.checkIn
			input.CheckOut = tt.checkOut

			_, err := service.Create(
				context.Background(),
				"customer-123",
				input,
			)
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			actualNights := int(
				repo.booking.CheckOut.Sub(repo.booking.CheckIn) /
					(24 * time.Hour),
			)

			if actualNights != tt.expectedNights {
				t.Errorf(
					"expected %d nights, got %d",
					tt.expectedNights,
					actualNights,
				)
			}
		})
	}
}

func TestServiceCreateValidation(t *testing.T) {
	tests := []struct {
		name       string
		customerID string
		modify     func(*CreateInput)
		wantErr    error
	}{
		{
			name:       "missing customer ID",
			customerID: " ",
			wantErr:    ErrInvalidCustomerID,
		},
		{
			name:       "missing room type ID",
			customerID: "customer-123",
			modify: func(input *CreateInput) {
				input.RoomTypeID = " "
			},
			wantErr: ErrInvalidRoomTypeID,
		},
		{
			name:       "missing check-in",
			customerID: "customer-123",
			modify: func(input *CreateInput) {
				input.CheckIn = time.Time{}
			},
			wantErr: ErrInvalidCheckIn,
		},
		{
			name:       "missing check-out",
			customerID: "customer-123",
			modify: func(input *CreateInput) {
				input.CheckOut = time.Time{}
			},
			wantErr: ErrInvalidCheckOut,
		},
		{
			name:       "check-out before check-in",
			customerID: "customer-123",
			modify: func(input *CreateInput) {
				input.CheckOut = input.CheckIn
			},
			wantErr: ErrInvalidDateRange,
		},
		{
			name:       "zero rooms",
			customerID: "customer-123",
			modify: func(input *CreateInput) {
				input.RoomsCount = 0
			},
			wantErr: ErrInvalidRoomsCount,
		},
		{
			name:       "negative rooms",
			customerID: "customer-123",
			modify: func(input *CreateInput) {
				input.RoomsCount = -1
			},
			wantErr: ErrInvalidRoomsCount,
		},
		{
			name:       "zero guests",
			customerID: "customer-123",
			modify: func(input *CreateInput) {
				input.GuestsCount = 0
			},
			wantErr: ErrInvalidGuestsCount,
		},
		{
			name:       "negative guests",
			customerID: "customer-123",
			modify: func(input *CreateInput) {
				input.GuestsCount = -1
			},
			wantErr: ErrInvalidGuestsCount,
		},
		{
			name: "rooms count exceeds maximum int32",
			modify: func(input *CreateInput) {
				input.RoomsCount = int(int64(math.MaxInt32) + 1)
			},
			wantErr:    ErrInvalidBookingCount,
			customerID: "valid-customer-id",
		},
		{
			name: "guests count exceeds maximum int32",
			modify: func(input *CreateInput) {
				input.GuestsCount = int(int64(math.MaxInt32) + 1)
			},
			wantErr:    ErrInvalidBookingCount,
			customerID: "valid-customer-id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeBookingRepository{}
			service := NewService(repo)

			input := validBookingInput()

			if tt.modify != nil {
				tt.modify(&input)
			}

			_, err := service.Create(
				context.Background(),
				tt.customerID,
				input,
			)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}

			if repo.called {
				t.Fatal("repository should not be called for invalid input")
			}
		})
	}
}

func TestServiceCreatePropagatesDomainErrors(t *testing.T) {
	tests := []struct {
		name    string
		repoErr error
	}{
		{
			name:    "room type not found",
			repoErr: ErrRoomTypeNotFound,
		},
		{
			name:    "guest capacity exceeded",
			repoErr: ErrGuestCapacityExceeded,
		},
		{
			name:    "inventory not configured",
			repoErr: ErrInventoryNotConfigured,
		},
		{
			name:    "insufficient availability",
			repoErr: ErrInsufficientAvailability,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeBookingRepository{
				err: tt.repoErr,
			}
			service := NewService(repo)

			_, err := service.Create(
				context.Background(),
				"customer-123",
				validBookingInput(),
			)

			if !errors.Is(err, tt.repoErr) {
				t.Fatalf("expected error %v, got %v", tt.repoErr, err)
			}
		})
	}
}

func TestServiceCreateWrapsUnexpectedRepositoryError(t *testing.T) {
	repositoryErr := errors.New("database connection failed")

	repo := &fakeBookingRepository{
		err: repositoryErr,
	}
	service := NewService(repo)

	_, err := service.Create(
		context.Background(),
		"customer-123",
		validBookingInput(),
	)

	if !errors.Is(err, repositoryErr) {
		t.Fatalf("expected wrapped repository error, got %v", err)
	}
}

func TestServiceCancelSuccess(t *testing.T) {
	now := time.Now().UTC()

	expected := &Booking{
		ID:          "booking-123",
		CustomerID:  "customer-123",
		Status:      StatusCancelled,
		CancelledAt: &now,
	}

	repo := &fakeBookingRepository{
		cancelledBooking: expected,
	}
	service := NewService(repo)

	cancelled, err := service.Cancel(
		context.Background(),
		" customer-123 ",
		" booking-123 ",
	)
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}

	if cancelled.Status != StatusCancelled {
		t.Errorf("expected status %q, got %q",
			StatusCancelled, cancelled.Status)
	}

	if !repo.cancelCalled {
		t.Fatal("expected repository Cancel() to be called")
	}

	if repo.cancelCustomerID != "customer-123" {
		t.Errorf("expected trimmed customer ID, got %q",
			repo.cancelCustomerID)
	}

	if repo.cancelBookingID != "booking-123" {
		t.Errorf("expected trimmed booking ID, got %q",
			repo.cancelBookingID)
	}
}

func TestServiceCancelValidation(t *testing.T) {
	tests := []struct {
		name       string
		customerID string
		bookingID  string
		wantErr    error
	}{
		{
			name:       "missing customer ID",
			customerID: " ",
			bookingID:  "booking-123",
			wantErr:    ErrInvalidCustomerID,
		},
		{
			name:       "missing booking ID",
			customerID: "customer-123",
			bookingID:  " ",
			wantErr:    ErrInvalidBookingID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeBookingRepository{}
			service := NewService(repo)

			_, err := service.Cancel(
				context.Background(),
				tt.customerID,
				tt.bookingID,
			)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}

			if repo.cancelCalled {
				t.Fatal("repository should not be called for invalid input")
			}
		})
	}
}

func TestServiceCancelPropagatesDomainErrors(t *testing.T) {
	tests := []struct {
		name    string
		repoErr error
	}{
		{
			name:    "booking not found",
			repoErr: ErrBookingNotFound,
		},
		{
			name:    "booking inventory inconsistent",
			repoErr: ErrBookingInventoryInconsistent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeBookingRepository{
				cancelErr: tt.repoErr,
			}
			service := NewService(repo)

			_, err := service.Cancel(
				context.Background(),
				"customer-123",
				"booking-123",
			)

			if !errors.Is(err, tt.repoErr) {
				t.Fatalf("expected %v, got %v", tt.repoErr, err)
			}
		})
	}
}

func TestServiceCancelWrapsUnexpectedRepositoryError(t *testing.T) {
	repositoryErr := errors.New("database connection failed")

	repo := &fakeBookingRepository{
		cancelErr: repositoryErr,
	}
	service := NewService(repo)

	_, err := service.Cancel(
		context.Background(),
		"customer-123",
		"booking-123",
	)

	if !errors.Is(err, repositoryErr) {
		t.Fatalf("expected wrapped repository error, got %v", err)
	}
}

func validModifyInput() ModifyInput {
	checkIn := dateOnly(time.Now().UTC().AddDate(0, 0, 15))

	return ModifyInput{
		CheckIn:     checkIn,
		CheckOut:    checkIn.AddDate(0, 0, 4),
		RoomsCount:  2,
		GuestsCount: 4,
	}
}

func TestServiceModifySuccess(t *testing.T) {
	repo := &fakeBookingRepository{
		modifyResult: &Booking{
			ID:          "booking-123",
			CustomerID:  "customer-123",
			RoomsCount:  2,
			GuestsCount: 4,
			Status:      StatusConfirmed,
		},
	}

	service := NewService(repo)
	input := validModifyInput()

	modified, err := service.Modify(
		context.Background(),
		" customer-123 ",
		" booking-123 ",
		input,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if modified.ID != "booking-123" {
		t.Errorf("unexpected booking ID: %q", modified.ID)
	}

	if !repo.modifyCalled {
		t.Fatal("expected repository Modify to be called")
	}

	if repo.modifyCustomerID != "customer-123" {
		t.Errorf("customer ID was not trimmed: %q", repo.modifyCustomerID)
	}

	if repo.modifyBookingID != "booking-123" {
		t.Errorf("booking ID was not trimmed: %q", repo.modifyBookingID)
	}

	if !repo.modifyInput.CheckIn.Equal(dateOnly(input.CheckIn)) {
		t.Error("check-in was not normalized")
	}

	if !repo.modifyInput.CheckOut.Equal(dateOnly(input.CheckOut)) {
		t.Error("check-out was not normalized")
	}

	if repo.modifyInput.RoomsCount != input.RoomsCount {
		t.Error("rooms count was not passed correctly")
	}

	if repo.modifyInput.GuestsCount != input.GuestsCount {
		t.Error("guests count was not passed correctly")
	}
}

func TestServiceModifyValidation(t *testing.T) {
	tests := []struct {
		name       string
		customerID string
		bookingID  string
		modify     func(*ModifyInput)
		wantErr    error
	}{
		{
			name:       "missing customer ID",
			customerID: " ",
			bookingID:  "booking-123",
			wantErr:    ErrInvalidCustomerID,
		},
		{
			name:       "missing booking ID",
			customerID: "customer-123",
			bookingID:  " ",
			wantErr:    ErrInvalidBookingID,
		},
		{
			name:       "missing check-in",
			customerID: "customer-123",
			bookingID:  "booking-123",
			modify: func(input *ModifyInput) {
				input.CheckIn = time.Time{}
			},
			wantErr: ErrInvalidCheckIn,
		},
		{
			name:       "missing check-out",
			customerID: "customer-123",
			bookingID:  "booking-123",
			modify: func(input *ModifyInput) {
				input.CheckOut = time.Time{}
			},
			wantErr: ErrInvalidCheckOut,
		},
		{
			name:       "invalid date range",
			customerID: "customer-123",
			bookingID:  "booking-123",
			modify: func(input *ModifyInput) {
				input.CheckOut = input.CheckIn
			},
			wantErr: ErrInvalidDateRange,
		},
		{
			name:       "zero rooms",
			customerID: "customer-123",
			bookingID:  "booking-123",
			modify: func(input *ModifyInput) {
				input.RoomsCount = 0
			},
			wantErr: ErrInvalidRoomsCount,
		},
		{
			name:       "zero guests",
			customerID: "customer-123",
			bookingID:  "booking-123",
			modify: func(input *ModifyInput) {
				input.GuestsCount = 0
			},
			wantErr: ErrInvalidGuestsCount,
		},
		{
			name:       "rooms count exceeds maximum int32",
			customerID: "customer-123",
			bookingID:  "booking-123",
			modify: func(input *ModifyInput) {
				input.RoomsCount = int(int64(math.MaxInt32) + 1)
			},
			wantErr: ErrInvalidBookingCount,
		},
		{
			name:       "guests count exceeds maximum int32",
			customerID: "customer-123",
			bookingID:  "booking-123",
			modify: func(input *ModifyInput) {
				input.GuestsCount = int(int64(math.MaxInt32) + 1)
			},
			wantErr: ErrInvalidBookingCount,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeBookingRepository{}
			service := NewService(repo)

			input := validModifyInput()
			if tt.modify != nil {
				tt.modify(&input)
			}

			_, err := service.Modify(
				context.Background(),
				tt.customerID,
				tt.bookingID,
				input,
			)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}

			if repo.modifyCalled {
				t.Fatal("repository should not be called for invalid input")
			}
		})
	}
}

func TestServiceModifyPropagatesDomainErrors(t *testing.T) {
	tests := []struct {
		name    string
		repoErr error
	}{
		{name: "booking not found", repoErr: ErrBookingNotFound},
		{name: "booking not modifiable", repoErr: ErrBookingNotModifiable},
		{name: "inventory inconsistent", repoErr: ErrBookingInventoryInconsistent},
		{name: "guest capacity exceeded", repoErr: ErrGuestCapacityExceeded},
		{name: "inventory not configured", repoErr: ErrInventoryNotConfigured},
		{name: "insufficient availability", repoErr: ErrInsufficientAvailability},
		{name: "amount overflow", repoErr: ErrBookingAmountOverflow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeBookingRepository{
				modifyErr: tt.repoErr,
			}
			service := NewService(repo)

			_, err := service.Modify(
				context.Background(),
				"customer-123",
				"booking-123",
				validModifyInput(),
			)

			if !errors.Is(err, tt.repoErr) {
				t.Fatalf("expected error %v, got %v", tt.repoErr, err)
			}
		})
	}
}

func TestExceedsGuestCapacity(t *testing.T) {
	maxInt := int(^uint(0) >> 1)

	tests := []struct {
		name         string
		maxOccupancy int
		roomsCount   int
		guestsCount  int
		wantExceeded bool
	}{
		{
			name:         "exact capacity",
			maxOccupancy: 2,
			roomsCount:   2,
			guestsCount:  4,
			wantExceeded: false,
		},
		{
			name:         "exceeds capacity",
			maxOccupancy: 2,
			roomsCount:   2,
			guestsCount:  5,
			wantExceeded: true,
		},
		{
			name:         "capacity across multiple rooms",
			maxOccupancy: 3,
			roomsCount:   2,
			guestsCount:  6,
			wantExceeded: false,
		},
		{
			name:         "large values without multiplication overflow",
			maxOccupancy: 3,
			roomsCount:   maxInt,
			guestsCount:  maxInt,
			wantExceeded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := exceedsGuestCapacity(
				tt.maxOccupancy,
				tt.roomsCount,
				tt.guestsCount,
			)

			if got != tt.wantExceeded {
				t.Errorf(
					"exceedsGuestCapacity() = %v, want %v",
					got,
					tt.wantExceeded,
				)
			}
		})
	}
}
