package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"staynest/internal/auth"
	"staynest/internal/booking"
	"staynest/internal/http/middleware"
)

type fakeBookingService struct {
	called           bool
	customerID       string
	input            booking.CreateInput
	result           *booking.Booking
	err              error
	cancelledBooking *booking.Booking
	cancelErr        error
	cancelCalled     bool
	cancelCustomerID string
	cancelBookingID  string
	modifiedBooking  *booking.Booking
	modifyErr        error
	modifyCalled     bool
	modifyCustomerID string
	modifyBookingID  string
	modifyInput      booking.ModifyInput
}

func (f *fakeBookingService) Create(
	ctx context.Context,
	customerID string,
	input booking.CreateInput,
) (*booking.Booking, error) {
	f.called = true
	f.customerID = customerID
	f.input = input

	if f.err != nil {
		return nil, f.err
	}

	return f.result, nil
}

func (f *fakeBookingService) Modify(
	ctx context.Context,
	customerID string,
	bookingID string,
	input booking.ModifyInput,
) (*booking.Booking, error) {
	f.modifyCalled = true
	f.modifyCustomerID = customerID
	f.modifyBookingID = bookingID
	f.modifyInput = input

	if f.modifyErr != nil {
		return nil, f.modifyErr
	}

	return f.modifiedBooking, nil
}

func (f *fakeBookingService) Cancel(
	ctx context.Context,
	customerID string,
	bookingID string,
) (*booking.Booking, error) {
	f.cancelCalled = true
	f.cancelCustomerID = customerID
	f.cancelBookingID = bookingID

	if f.cancelErr != nil {
		return nil, f.cancelErr
	}

	return f.cancelledBooking, nil
}

func (f *fakeBookingService) GetByID(
	ctx context.Context,
	customerID string,
	bookingID string,
) (*booking.Booking, error) {
	return nil, nil
}

func (f *fakeBookingService) ListByCustomerID(
	ctx context.Context,
	customerID string,
	filter booking.ListFilter,
) (booking.ListResult, error) {
	return booking.ListResult{
		Bookings: []booking.Booking{},
		Total:    0,
		Limit:    filter.Limit,
		Offset:   filter.Offset,
	}, nil
}

type bookingTestActiveCustomerChecker struct{}

func (f *bookingTestActiveCustomerChecker) IsActive(
	ctx context.Context,
	id string,
) (bool, error) {
	return true, nil
}

func newBookingHandlerTestRouter(
	t *testing.T,
	service BookingService,
) (*gin.Engine, *auth.TokenManager) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	tokenManager, err := auth.NewTokenManager(
		"test-secret-with-at-least-32-bytes-long",
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("create token manager: %v", err)
	}

	r := gin.New()

	protected := r.Group("/api/v1")
	protected.Use(middleware.Authenticate(
		tokenManager,
		&bookingTestActiveCustomerChecker{},
	))

	protected.POST("/bookings", CreateBooking(service))
	protected.PATCH("/bookings/:id", ModifyBooking(service))

	return r, tokenManager
}

func performBookingRequest(
	t *testing.T,
	r http.Handler,
	token string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/bookings",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

func performModifyBookingRequest(
	t *testing.T,
	r http.Handler,
	token string,
	bookingID string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/bookings/"+bookingID,
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

func TestCreateBookingSuccess(t *testing.T) {
	now := time.Date(2027, 1, 15, 10, 30, 0, 0, time.UTC)

	service := &fakeBookingService{
		result: &booking.Booking{
			ID:                 "booking-123",
			BookingReference:   "STN-TEST-123",
			CustomerID:         "customer-123",
			RoomTypeID:         "room-123",
			CheckIn:            time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC),
			CheckOut:           time.Date(2027, 1, 18, 0, 0, 0, 0, time.UTC),
			RoomsCount:         2,
			GuestsCount:        4,
			PricePerNightPaise: 899900,
			TotalAmountPaise:   5399400,
			Status:             booking.StatusConfirmed,
			CreatedAt:          now,
			UpdatedAt:          now,
		},
	}

	r, tokenManager := newBookingHandlerTestRouter(t, service)

	token, err := tokenManager.GenerateAccessToken("customer-123")
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	rec := performBookingRequest(
		t,
		r,
		token,
		`{
			"room_type_id": "room-123",
			"check_in": "2027-01-15",
			"check_out": "2027-01-18",
			"rooms_count": 2,
			"guests_count": 4
		}`,
	)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s",
			rec.Code, rec.Body.String())
	}

	if !service.called {
		t.Fatal("expected booking service to be called")
	}

	if service.customerID != "customer-123" {
		t.Errorf("expected authenticated customer ID customer-123, got %q",
			service.customerID)
	}

	if service.input.RoomTypeID != "room-123" {
		t.Errorf("expected room type ID room-123, got %q",
			service.input.RoomTypeID)
	}

	if service.input.RoomsCount != 2 {
		t.Errorf("expected rooms count 2, got %d",
			service.input.RoomsCount)
	}

	if service.input.GuestsCount != 4 {
		t.Errorf("expected guests count 4, got %d",
			service.input.GuestsCount)
	}

	if !service.input.CheckIn.Equal(
		time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC),
	) {
		t.Errorf("unexpected check-in date: %v", service.input.CheckIn)
	}

	body := rec.Body.String()

	expectedFields := []string{
		`"id":"booking-123"`,
		`"booking_reference":"STN-TEST-123"`,
		`"total_amount_paise":5399400`,
		`"status":"confirmed"`,
	}

	for _, field := range expectedFields {
		if !strings.Contains(body, field) {
			t.Errorf("expected response to contain %s: %s",
				field, body)
		}
	}
}

func TestCreateBookingRequiresAuthentication(t *testing.T) {
	service := &fakeBookingService{}

	r, _ := newBookingHandlerTestRouter(t, service)

	rec := performBookingRequest(
		t,
		r,
		"",
		`{
			"room_type_id": "room-123",
			"check_in": "2027-01-15",
			"check_out": "2027-01-18",
			"rooms_count": 1,
			"guests_count": 2
		}`,
	)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d: %s",
			rec.Code, rec.Body.String())
	}

	if service.called {
		t.Fatal("booking service must not be called without authentication")
	}
}

func TestCreateBookingUsesAuthenticatedCustomerID(t *testing.T) {
	service := &fakeBookingService{
		result: &booking.Booking{
			ID:        "booking-123",
			CheckIn:   time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC),
			CheckOut:  time.Date(2027, 1, 16, 0, 0, 0, 0, time.UTC),
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		},
	}

	r, tokenManager := newBookingHandlerTestRouter(t, service)

	token, err := tokenManager.GenerateAccessToken("authenticated-customer")
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	rec := performBookingRequest(
		t,
		r,
		token,
		`{
			"customer_id": "another-customer",
			"room_type_id": "room-123",
			"check_in": "2027-01-15",
			"check_out": "2027-01-16",
			"rooms_count": 1,
			"guests_count": 2
		}`,
	)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s",
			rec.Code, rec.Body.String())
	}

	if service.customerID != "authenticated-customer" {
		t.Errorf("expected authenticated customer ID, got %q",
			service.customerID)
	}
}

func TestCreateBookingInvalidDates(t *testing.T) {
	service := &fakeBookingService{}

	r, tokenManager := newBookingHandlerTestRouter(t, service)

	token, err := tokenManager.GenerateAccessToken("customer-123")
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	tests := []struct {
		name string
		body string
	}{
		{
			name: "invalid check-in format",
			body: `{
				"room_type_id": "room-123",
				"check_in": "15-01-2027",
				"check_out": "2027-01-18",
				"rooms_count": 1,
				"guests_count": 2
			}`,
		},
		{
			name: "invalid check-out format",
			body: `{
				"room_type_id": "room-123",
				"check_in": "2027-01-15",
				"check_out": "18-01-2027",
				"rooms_count": 1,
				"guests_count": 2
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := performBookingRequest(t, r, token, tt.body)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected status 400, got %d: %s",
					rec.Code, rec.Body.String())
			}
		})
	}

	if service.called {
		t.Fatal("booking service must not be called for invalid dates")
	}
}

func TestCreateBookingMapsDomainErrors(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectedStatus int
	}{
		{
			name:           "invalid room type ID",
			err:            booking.ErrInvalidRoomTypeID,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "invalid date range",
			err:            booking.ErrInvalidDateRange,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "check-in date in the past",
			err:            booking.ErrBookingDateInPast,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "guest capacity exceeded",
			err:            booking.ErrGuestCapacityExceeded,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "room type not found",
			err:            booking.ErrRoomTypeNotFound,
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "inventory not configured",
			err:            booking.ErrInventoryNotConfigured,
			expectedStatus: http.StatusConflict,
		},
		{
			name:           "insufficient availability",
			err:            booking.ErrInsufficientAvailability,
			expectedStatus: http.StatusConflict,
		},
		{
			name:           "amount overflow",
			err:            booking.ErrBookingAmountOverflow,
			expectedStatus: http.StatusUnprocessableEntity,
		},
		{
			name:           "unexpected error",
			err:            errors.New("database unavailable"),
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeBookingService{
				err: tt.err,
			}

			r, tokenManager := newBookingHandlerTestRouter(t, service)

			token, err := tokenManager.GenerateAccessToken("customer-123")
			if err != nil {
				t.Fatalf("generate access token: %v", err)
			}

			rec := performBookingRequest(
				t,
				r,
				token,
				`{
					"room_type_id": "room-123",
					"check_in": "2027-01-15",
					"check_out": "2027-01-18",
					"rooms_count": 1,
					"guests_count": 2
				}`,
			)

			if rec.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d: %s",
					tt.expectedStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestModifyBookingSuccess(t *testing.T) {
	now := time.Date(2027, 1, 15, 10, 30, 0, 0, time.UTC)

	service := &fakeBookingService{
		modifiedBooking: &booking.Booking{
			ID:                 "booking-123",
			BookingReference:   "STN-TEST-123",
			CustomerID:         "customer-123",
			RoomTypeID:         "room-123",
			CheckIn:            time.Date(2027, 1, 16, 0, 0, 0, 0, time.UTC),
			CheckOut:           time.Date(2027, 1, 20, 0, 0, 0, 0, time.UTC),
			RoomsCount:         3,
			GuestsCount:        6,
			PricePerNightPaise: 999900,
			TotalAmountPaise:   11998800,
			Status:             booking.StatusConfirmed,
			CreatedAt:          now,
			UpdatedAt:          now,
		},
	}

	r, tokenManager := newBookingHandlerTestRouter(t, service)

	token, err := tokenManager.GenerateAccessToken("customer-123")
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	rec := performModifyBookingRequest(
		t,
		r,
		token,
		"booking-123",
		`{
            "check_in": "2027-01-16",
            "check_out": "2027-01-20",
            "rooms_count": 3,
            "guests_count": 6
        }`,
	)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s",
			rec.Code, rec.Body.String())
	}

	if !service.modifyCalled {
		t.Fatal("expected booking service Modify to be called")
	}

	if service.modifyCustomerID != "customer-123" {
		t.Errorf("expected customer ID customer-123, got %q",
			service.modifyCustomerID)
	}

	if service.modifyBookingID != "booking-123" {
		t.Errorf("expected booking ID booking-123, got %q",
			service.modifyBookingID)
	}

	if service.modifyInput.RoomsCount != 3 {
		t.Errorf("expected 3 rooms, got %d",
			service.modifyInput.RoomsCount)
	}

	if service.modifyInput.GuestsCount != 6 {
		t.Errorf("expected 6 guests, got %d",
			service.modifyInput.GuestsCount)
	}

	expectedCheckIn := time.Date(2027, 1, 16, 0, 0, 0, 0, time.UTC)
	expectedCheckOut := time.Date(2027, 1, 20, 0, 0, 0, 0, time.UTC)

	if !service.modifyInput.CheckIn.Equal(expectedCheckIn) {
		t.Errorf("unexpected check-in: %v", service.modifyInput.CheckIn)
	}

	if !service.modifyInput.CheckOut.Equal(expectedCheckOut) {
		t.Errorf("unexpected check-out: %v", service.modifyInput.CheckOut)
	}

	body := rec.Body.String()

	expectedFields := []string{
		`"id":"booking-123"`,
		`"rooms_count":3`,
		`"guests_count":6`,
		`"price_per_night_paise":999900`,
		`"total_amount_paise":11998800`,
		`"status":"confirmed"`,
	}

	for _, field := range expectedFields {
		if !strings.Contains(body, field) {
			t.Errorf("expected response to contain %s: %s",
				field, body)
		}
	}
}

func TestModifyBookingRequiresAuthentication(t *testing.T) {
	service := &fakeBookingService{}

	r, _ := newBookingHandlerTestRouter(t, service)

	rec := performModifyBookingRequest(
		t,
		r,
		"",
		"booking-123",
		`{
            "check_in": "2027-01-16",
            "check_out": "2027-01-20",
            "rooms_count": 1,
            "guests_count": 2
        }`,
	)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d: %s",
			rec.Code, rec.Body.String())
	}

	if service.modifyCalled {
		t.Fatal("Modify must not be called without authentication")
	}
}

func TestModifyBookingInvalidRequest(t *testing.T) {
	service := &fakeBookingService{}

	r, tokenManager := newBookingHandlerTestRouter(t, service)

	token, err := tokenManager.GenerateAccessToken("customer-123")
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	tests := []struct {
		name string
		body string
	}{
		{
			name: "malformed JSON",
			body: `{"check_in":`,
		},
		{
			name: "invalid check-in format",
			body: `{
                "check_in": "16-01-2027",
                "check_out": "2027-01-20",
                "rooms_count": 1,
                "guests_count": 2
            }`,
		},
		{
			name: "invalid check-out format",
			body: `{
                "check_in": "2027-01-16",
                "check_out": "20-01-2027",
                "rooms_count": 1,
                "guests_count": 2
            }`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := performModifyBookingRequest(
				t,
				r,
				token,
				"booking-123",
				tt.body,
			)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected status 400, got %d: %s",
					rec.Code, rec.Body.String())
			}
		})
	}

	if service.modifyCalled {
		t.Fatal("Modify must not be called for invalid requests")
	}
}

func TestModifyBookingMapsDomainErrors(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectedStatus int
	}{
		{
			name:           "invalid customer ID",
			err:            booking.ErrInvalidCustomerID,
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "invalid booking ID",
			err:            booking.ErrInvalidBookingID,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "invalid check-in",
			err:            booking.ErrInvalidCheckIn,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "invalid check-out",
			err:            booking.ErrInvalidCheckOut,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "invalid date range",
			err:            booking.ErrInvalidDateRange,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "check-in date in the past",
			err:            booking.ErrBookingDateInPast,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "invalid rooms count",
			err:            booking.ErrInvalidRoomsCount,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "invalid guests count",
			err:            booking.ErrInvalidGuestsCount,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "guest capacity exceeded",
			err:            booking.ErrGuestCapacityExceeded,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "booking not found",
			err:            booking.ErrBookingNotFound,
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "booking not modifiable",
			err:            booking.ErrBookingNotModifiable,
			expectedStatus: http.StatusConflict,
		},
		{
			name:           "inventory not configured",
			err:            booking.ErrInventoryNotConfigured,
			expectedStatus: http.StatusConflict,
		},
		{
			name:           "insufficient availability",
			err:            booking.ErrInsufficientAvailability,
			expectedStatus: http.StatusConflict,
		},
		{
			name:           "booking inventory inconsistent",
			err:            booking.ErrBookingInventoryInconsistent,
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:           "booking amount overflow",
			err:            booking.ErrBookingAmountOverflow,
			expectedStatus: http.StatusUnprocessableEntity,
		},
		{
			name:           "unexpected error",
			err:            errors.New("database unavailable"),
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeBookingService{
				modifyErr: tt.err,
			}

			r, tokenManager := newBookingHandlerTestRouter(t, service)

			token, err := tokenManager.GenerateAccessToken("customer-123")
			if err != nil {
				t.Fatalf("generate access token: %v", err)
			}

			rec := performModifyBookingRequest(
				t,
				r,
				token,
				"booking-123",
				`{
                    "check_in": "2027-01-16",
                    "check_out": "2027-01-20",
                    "rooms_count": 2,
                    "guests_count": 4
                }`,
			)

			if rec.Code != tt.expectedStatus {
				t.Errorf(
					"expected status %d, got %d: %s",
					tt.expectedStatus,
					rec.Code,
					rec.Body.String(),
				)
			}

			if !service.modifyCalled {
				t.Error("expected Modify service to be called")
			}
		})
	}
}
