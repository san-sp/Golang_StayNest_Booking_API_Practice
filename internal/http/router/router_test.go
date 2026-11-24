package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"staynest/internal/auth"
	"staynest/internal/booking"
	"staynest/internal/customer"
	"staynest/internal/hotel"
	"staynest/internal/inventory"
	"staynest/internal/payment"
	"staynest/internal/room"
)

type fakeActiveCustomerChecker struct{}

type fakeHotelService struct {
	createCalled  bool
	getHotel      *hotel.Hotel
	getErr        error
	getCalled     bool
	getID         string
	listed        *hotel.ListResult
	listErr       error
	listParams    hotel.ListParams
	listCalled    bool
	searchResults []hotel.SearchResult
	searchErr     error
	searchParams  hotel.SearchParams
	searchCalled  bool
}

type fakePaymentService struct{}

type fakeWebhookService struct {
	called    bool
	eventID   string
	signature string
	body      []byte
	err       error
}

type fakeRouterCustomerService struct {
	loginCalls int
}

func (f *fakeRouterCustomerService) Register(
	ctx context.Context,
	name string,
	email string,
	password string,
) (*customer.Customer, error) {
	return nil, nil
}

func (f *fakeRouterCustomerService) Login(
	ctx context.Context,
	email string,
	password string,
) (string, error) {
	f.loginCalls++
	return "test-access-token", nil
}

func (f *fakeRouterCustomerService) GetByID(
	ctx context.Context,
	id string,
) (*customer.Customer, error) {
	return nil, nil
}

func (f *fakeRouterCustomerService) UpdateName(
	ctx context.Context,
	id string,
	name string,
) (*customer.Customer, error) {
	return nil, nil
}

func (f *fakeRouterCustomerService) ChangePassword(
	ctx context.Context,
	id string,
	currentPassword string,
	newPassword string,
) error {
	return nil
}

func (f *fakeRouterCustomerService) Deactivate(
	ctx context.Context,
	id string,
	currentPassword string,
) error {
	return nil
}

func (f *fakeWebhookService) Process(
	ctx context.Context,
	eventID string,
	signature string,
	body []byte,
) error {
	f.called = true
	f.eventID = eventID
	f.signature = signature
	f.body = append([]byte(nil), body...)

	return f.err
}

func (f *fakePaymentService) Initiate(
	context.Context,
	payment.InitiateInput,
) (*payment.CheckoutDetails, error) {
	return nil, nil
}

func (f *fakePaymentService) Verify(
	context.Context,
	payment.VerifyInput,
) (*payment.Payment, error) {
	return nil, nil
}

func (f *fakeActiveCustomerChecker) IsActive(
	ctx context.Context,
	id string,
) (bool, error) {
	return true, nil
}

type fakeAdminChecker struct {
	isAdmin bool
	called  bool
}

func (f *fakeAdminChecker) IsAdmin(
	ctx context.Context,
	id string,
) (bool, error) {
	f.called = true
	return f.isAdmin, nil
}

func (f *fakeHotelService) GetByID(
	ctx context.Context,
	id string,
) (*hotel.Hotel, error) {
	f.getCalled = true
	f.getID = id

	return f.getHotel, f.getErr
}

func (f *fakeHotelService) Create(
	ctx context.Context,
	input hotel.Hotel,
) (*hotel.Hotel, error) {
	f.createCalled = true

	now := time.Now().UTC()

	return &hotel.Hotel{
		ID:          "hotel-123",
		Name:        input.Name,
		Description: input.Description,
		Address:     input.Address,
		City:        input.City,
		Country:     input.Country,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func (f *fakeHotelService) Update(
	ctx context.Context,
	id string,
	input hotel.Hotel,
) (*hotel.Hotel, error) {
	return nil, hotel.ErrHotelNotFound
}

func (f *fakeHotelService) List(
	ctx context.Context,
	params hotel.ListParams,
) (*hotel.ListResult, error) {
	f.listCalled = true
	f.listParams = params

	if f.listErr != nil {
		return nil, f.listErr
	}

	return f.listed, nil
}

func (f *fakeHotelService) Search(
	ctx context.Context,
	params hotel.SearchParams,
) ([]hotel.SearchResult, error) {
	f.searchCalled = true
	f.searchParams = params

	if f.searchErr != nil {
		return nil, f.searchErr
	}

	return f.searchResults, nil
}

func newTestRouter(
	t *testing.T,
	isAdmin bool,
) (
	*gin.Engine,
	*auth.TokenManager,
	*fakeAdminChecker,
	*fakeHotelService,
	*fakeRoomService,
) {
	r, tokenManager, adminChecker, hotelService, roomService, _ :=
		newTestRouterWithInventory(t, isAdmin)

	return r, tokenManager, adminChecker, hotelService, roomService
}

func newTestRouterWithInventory(
	t *testing.T,
	isAdmin bool,
) (
	*gin.Engine,
	*auth.TokenManager,
	*fakeAdminChecker,
	*fakeHotelService,
	*fakeRoomService,
	*fakeInventoryService,
) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	tokenManager, err := auth.NewTokenManager(
		"test-secret-with-at-least-32-bytes-long",
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("create token manager: %v", err)
	}

	adminChecker := &fakeAdminChecker{
		isAdmin: isAdmin,
	}

	hotelService := &fakeHotelService{}
	roomService := &fakeRoomService{}
	inventoryService := &fakeInventoryService{}
	bookingService := &fakeBookingService{}

	r := New(
		nil,
		nil,
		tokenManager,
		&fakeActiveCustomerChecker{},
		adminChecker,
		hotelService,
		roomService,
		inventoryService,
		bookingService,
		&fakePaymentService{},
		nil,
	)

	return r,
		tokenManager,
		adminChecker,
		hotelService,
		roomService,
		inventoryService
}

type fakeRoomService struct {
	createCalled bool
	createErr    error

	getCalled bool
	getErr    error

	listCalled bool
	listErr    error

	updateCalled bool
	updateErr    error

	createInput room.RoomType
	getID       string
	listHotelID string
	updateID    string
}

func (f *fakeRoomService) Create(
	ctx context.Context,
	input room.RoomType,
) (*room.RoomType, error) {
	f.createCalled = true
	f.createInput = input // Add this

	if f.createErr != nil {
		return nil, f.createErr
	}

	return &room.RoomType{
		ID:      "room-123",
		HotelID: input.HotelID,
		Name:    input.Name,
	}, nil
}

func (f *fakeRoomService) GetByID(
	ctx context.Context,
	id string,
) (*room.RoomType, error) {
	f.getCalled = true
	f.getID = id

	return &room.RoomType{ID: id}, f.getErr
}

func (f *fakeRoomService) ListByHotelID(
	ctx context.Context,
	hotelID string,
) ([]room.RoomType, error) {
	f.listCalled = true
	f.listHotelID = hotelID

	return []room.RoomType{}, f.listErr
}

func (f *fakeRoomService) Update(
	ctx context.Context,
	id string,
	input room.RoomType,
) (*room.RoomType, error) {
	f.updateCalled = true
	f.updateID = id

	return &room.RoomType{ID: id}, f.updateErr
}

type fakeInventoryService struct {
	setCalled  bool
	listCalled bool

	setErr  error
	listErr error

	setRoomTypeID string
	setStayDate   time.Time
	setTotalRooms int

	listRoomTypeID string
	listStartDate  time.Time
	listEndDate    time.Time
}

func (f *fakeInventoryService) SetDailyCapacity(
	ctx context.Context,
	roomTypeID string,
	stayDate time.Time,
	totalRooms int,
) (*inventory.DailyInventory, error) {
	f.setCalled = true
	f.setRoomTypeID = roomTypeID
	f.setStayDate = stayDate
	f.setTotalRooms = totalRooms

	if f.setErr != nil {
		return nil, f.setErr
	}

	return &inventory.DailyInventory{
		RoomTypeID:     roomTypeID,
		StayDate:       stayDate,
		TotalRooms:     totalRooms,
		AvailableRooms: totalRooms,
		UpdatedAt:      time.Now().UTC(),
	}, nil
}

func (f *fakeInventoryService) ListByRoomTypeAndDateRange(
	ctx context.Context,
	roomTypeID string,
	startDate time.Time,
	endDate time.Time,
) ([]inventory.DailyInventory, error) {
	f.listCalled = true
	f.listRoomTypeID = roomTypeID
	f.listStartDate = startDate
	f.listEndDate = endDate

	if f.listErr != nil {
		return nil, f.listErr
	}

	return []inventory.DailyInventory{}, nil
}

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
	modifyInput      booking.ModifyInput
	modifyCustomerID string
	modifyBookingID  string
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

func newTestRouterWithBooking(
	t *testing.T,
) (*gin.Engine, *auth.TokenManager, *fakeBookingService) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	tokenManager, err := auth.NewTokenManager(
		"test-secret-with-at-least-32-bytes-long",
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("create token manager: %v", err)
	}

	bookingService := &fakeBookingService{}

	r := New(
		nil,
		nil,
		tokenManager,
		&fakeActiveCustomerChecker{},
		&fakeAdminChecker{},
		&fakeHotelService{},
		&fakeRoomService{},
		&fakeInventoryService{},
		bookingService,
		&fakePaymentService{},
		nil,
	)

	return r, tokenManager, bookingService
}

func TestAdminCreateHotelRequiresAuthentication(t *testing.T) {
	r, _, adminChecker, hotelService, _ := newTestRouter(t, true)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/hotels",
		strings.NewReader(`{"name":"Test Hotel","address":"Test Address","city":"Kochi","country":"India"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d: %s",
			http.StatusUnauthorized,
			rec.Code,
			rec.Body.String(),
		)
	}

	if adminChecker.called {
		t.Fatal("admin checker should not be called without authentication")
	}

	if hotelService.createCalled {
		t.Fatal("hotel service should not be called without authentication")
	}
}

func TestAdminCreateHotelRejectsCustomer(t *testing.T) {
	r, tokenManager, adminChecker, hotelService, _ := newTestRouter(t, false)

	token, err := tokenManager.GenerateAccessToken("customer-123")
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/hotels",
		strings.NewReader(`{"name":"Test Hotel","address":"Test Address","city":"Kochi","country":"India"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d: %s",
			http.StatusForbidden,
			rec.Code,
			rec.Body.String(),
		)
	}

	if !adminChecker.called {
		t.Fatal("admin checker should be called for an authenticated customer")
	}

	if hotelService.createCalled {
		t.Fatal("hotel service should not be called for a non-admin customer")
	}
}

func TestAdminCreateHotelAllowsAdmin(t *testing.T) {
	r, tokenManager, adminChecker, hotelService, _ := newTestRouter(t, true)

	token, err := tokenManager.GenerateAccessToken("admin-123")
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/hotels",
		strings.NewReader(`{"name":"Test Hotel","address":"Test Address","city":"Kochi","country":"India"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s",
			http.StatusCreated,
			rec.Code,
			rec.Body.String(),
		)
	}

	if !adminChecker.called {
		t.Fatal("admin checker should be called")
	}

	if !hotelService.createCalled {
		t.Fatal("hotel service should be called for an admin")
	}
}

func TestGetHotelIsPublic(t *testing.T) {
	r, _, _, hotelService, _ := newTestRouter(t, false)

	hotelService.getHotel = &hotel.Hotel{
		ID:          "hotel-123",
		Name:        "StayNest Hotel",
		Description: "A comfortable hotel",
		Address:     "123 Test Street",
		City:        "Kochi",
		Country:     "India",
		Timezone:    "Asia/Kolkata",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/hotels/hotel-123",
		nil,
	)

	// Intentionally no Authorization header.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(
			"expected status 200, got %d: %s",
			rec.Code,
			rec.Body.String(),
		)
	}

	if !hotelService.getCalled {
		t.Fatal("expected public route to call hotel service")
	}

	if hotelService.getID != "hotel-123" {
		t.Errorf("expected hotel ID hotel-123, got %q", hotelService.getID)
	}
}

func TestListHotelsIsPublic(t *testing.T) {
	r, _, _, hotelService, _ := newTestRouter(t, false)

	hotelService.listed = &hotel.ListResult{
		Hotels: []hotel.Hotel{
			{
				ID:       "hotel-123",
				Name:     "StayNest Hotel",
				Address:  "123 Test Street",
				City:     "Kochi",
				Country:  "India",
				Timezone: "Asia/Kolkata",
			},
		},
		Total:  1,
		Limit:  5,
		Offset: 2,
	}

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/hotels?city=Kochi&limit=5&offset=2",
		nil,
	)

	// Intentionally no Authorization header.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(
			"expected status 200, got %d: %s",
			rec.Code,
			rec.Body.String(),
		)
	}

	if !hotelService.listCalled {
		t.Fatal("expected public route to call hotel service")
	}

	if hotelService.listParams.City != "Kochi" {
		t.Errorf("expected city Kochi, got %q", hotelService.listParams.City)
	}

	if hotelService.listParams.Limit != 5 {
		t.Errorf("expected limit 5, got %d", hotelService.listParams.Limit)
	}

	if hotelService.listParams.Offset != 2 {
		t.Errorf("expected offset 2, got %d", hotelService.listParams.Offset)
	}

	if !strings.Contains(rec.Body.String(), `"name":"StayNest Hotel"`) {
		t.Errorf("expected hotel in response: %s", rec.Body.String())
	}
}

func TestAdminCreateRoomTypeRequiresAuthentication(t *testing.T) {
	r, _, adminChecker, _, roomService := newTestRouter(t, true)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/hotels/hotel-123/rooms",
		strings.NewReader(`{
			"name": "Deluxe Room",
			"max_occupancy": 3,
			"price_per_night_paise": 899900
		}`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}

	if adminChecker.called {
		t.Fatal("admin checker must not run without authentication")
	}

	if roomService.createCalled {
		t.Fatal("room service must not be called without authentication")
	}
}

func TestAdminCreateRoomTypeRejectsCustomer(t *testing.T) {
	r, tokenManager, adminChecker, _, roomService := newTestRouter(t, false)

	token, err := tokenManager.GenerateAccessToken("customer-123")
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/hotels/hotel-123/rooms",
		strings.NewReader(`{
			"name": "Deluxe Room",
			"max_occupancy": 3,
			"price_per_night_paise": 899900
		}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	if !adminChecker.called {
		t.Fatal("admin checker should be called")
	}

	if roomService.createCalled {
		t.Fatal("room service must not be called for a customer")
	}
}

func TestAdminCreateRoomTypeAllowsAdmin(t *testing.T) {
	r, tokenManager, adminChecker, _, roomService := newTestRouter(t, true)

	token, err := tokenManager.GenerateAccessToken("admin-123")
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/hotels/hotel-123/rooms",
		strings.NewReader(`{
			"name": "Deluxe Room",
			"max_occupancy": 3,
			"price_per_night_paise": 899900
		}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	if !adminChecker.called {
		t.Fatal("admin checker should be called")
	}

	if !roomService.createCalled {
		t.Fatal("room service should be called for an admin")
	}

	if roomService.createInput.HotelID != "hotel-123" {
		t.Errorf("expected hotel ID hotel-123, got %q",
			roomService.createInput.HotelID)
	}
}

func TestAdminUpdateRoomTypeAllowsAdmin(t *testing.T) {
	r, tokenManager, _, _, roomService := newTestRouter(t, true)

	token, err := tokenManager.GenerateAccessToken("admin-123")
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/admin/rooms/room-123",
		strings.NewReader(`{
			"name": "Premium Room",
			"description": "Updated room",
			"max_occupancy": 4,
			"price_per_night_paise": 1099900
		}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if !roomService.updateCalled {
		t.Fatal("room service should be called for an admin")
	}

	if roomService.updateID != "room-123" {
		t.Errorf("expected room ID room-123, got %q", roomService.updateID)
	}
}

func TestListRoomTypesIsPublic(t *testing.T) {
	r, _, adminChecker, _, roomService := newTestRouter(t, false)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/hotels/hotel-123/rooms",
		nil,
	)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if !roomService.listCalled {
		t.Fatal("room service should be called")
	}

	if roomService.listHotelID != "hotel-123" {
		t.Errorf("expected hotel ID hotel-123, got %q", roomService.listHotelID)
	}

	if adminChecker.called {
		t.Fatal("public route must not invoke admin checker")
	}
}

func TestGetRoomTypeIsPublic(t *testing.T) {
	r, _, adminChecker, _, roomService := newTestRouter(t, false)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/rooms/room-123",
		nil,
	)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if !roomService.getCalled {
		t.Fatal("room service should be called")
	}

	if roomService.getID != "room-123" {
		t.Errorf("expected room ID room-123, got %q", roomService.getID)
	}

	if adminChecker.called {
		t.Fatal("public route must not invoke admin checker")
	}
}

func TestListRoomInventoryIsPublic(t *testing.T) {
	r, _, _, _, _, inventoryService :=
		newTestRouterWithInventory(t, false)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/rooms/room-123/inventory?start_date=2027-01-01&end_date=2027-01-05",
		nil,
	)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(
			"expected status %d, got %d: %s",
			http.StatusOK,
			rec.Code,
			rec.Body.String(),
		)
	}

	if !inventoryService.listCalled {
		t.Fatal("expected public route to call inventory service")
	}

	if inventoryService.listRoomTypeID != "room-123" {
		t.Errorf(
			"expected room type ID room-123, got %q",
			inventoryService.listRoomTypeID,
		)
	}
}

func TestSetDailyInventoryRequiresAuthentication(t *testing.T) {
	r, _, adminChecker, _, _, inventoryService :=
		newTestRouterWithInventory(t, true)

	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/admin/rooms/room-123/inventory/2027-01-01",
		strings.NewReader(`{"total_rooms":10}`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf(
			"expected status %d, got %d: %s",
			http.StatusUnauthorized,
			rec.Code,
			rec.Body.String(),
		)
	}

	if adminChecker.called {
		t.Fatal("admin checker should not be called without authentication")
	}

	if inventoryService.setCalled {
		t.Fatal("inventory service should not be called without authentication")
	}
}

func TestSetDailyInventoryRejectsCustomer(t *testing.T) {
	r, tokenManager, adminChecker, _, _, inventoryService :=
		newTestRouterWithInventory(t, false)

	token, err := tokenManager.GenerateAccessToken("customer-123")
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/admin/rooms/room-123/inventory/2027-01-01",
		strings.NewReader(`{"total_rooms":10}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf(
			"expected status %d, got %d: %s",
			http.StatusForbidden,
			rec.Code,
			rec.Body.String(),
		)
	}

	if !adminChecker.called {
		t.Fatal("admin checker should be called for authenticated customer")
	}

	if inventoryService.setCalled {
		t.Fatal("inventory service should not be called for a non-admin")
	}
}

func TestSetDailyInventoryAllowsAdmin(t *testing.T) {
	r, tokenManager, adminChecker, _, _, inventoryService :=
		newTestRouterWithInventory(t, true)

	token, err := tokenManager.GenerateAccessToken("admin-123")
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/admin/rooms/room-123/inventory/2027-01-01",
		strings.NewReader(`{"total_rooms":10}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(
			"expected status %d, got %d: %s",
			http.StatusOK,
			rec.Code,
			rec.Body.String(),
		)
	}

	if !adminChecker.called {
		t.Fatal("admin checker should be called")
	}

	if !inventoryService.setCalled {
		t.Fatal("inventory service should be called for an admin")
	}

	if inventoryService.setRoomTypeID != "room-123" {
		t.Errorf(
			"expected room type ID room-123, got %q",
			inventoryService.setRoomTypeID,
		)
	}

	if inventoryService.setTotalRooms != 10 {
		t.Errorf(
			"expected total rooms 10, got %d",
			inventoryService.setTotalRooms,
		)
	}
}

func TestCreateBookingRequiresAuthentication(t *testing.T) {
	r, _, bookingService := newTestRouterWithBooking(t)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/bookings",
		strings.NewReader(`{
			"room_type_id": "room-123",
			"check_in": "2026-12-01",
			"check_out": "2026-12-03",
			"rooms_count": 1,
			"guests_count": 2
		}`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf(
			"expected status %d, got %d: %s",
			http.StatusUnauthorized,
			rec.Code,
			rec.Body.String(),
		)
	}

	if bookingService.called {
		t.Fatal("booking service should not be called without authentication")
	}
}

func TestCreateBookingAuthenticatedCustomer(t *testing.T) {
	r, tokenManager, bookingService := newTestRouterWithBooking(t)

	token, err := tokenManager.GenerateAccessToken("customer-123")
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	now := time.Now().UTC()

	bookingService.result = &booking.Booking{
		ID:                 "booking-123",
		BookingReference:   "STN-12345678901234567890",
		CustomerID:         "customer-123",
		RoomTypeID:         "room-123",
		CheckIn:            time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		CheckOut:           time.Date(2026, 12, 3, 0, 0, 0, 0, time.UTC),
		RoomsCount:         1,
		GuestsCount:        2,
		PricePerNightPaise: 250000,
		TotalAmountPaise:   500000,
		Status:             booking.StatusConfirmed,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/bookings",
		strings.NewReader(`{
			"room_type_id": "room-123",
			"check_in": "2026-12-01",
			"check_out": "2026-12-03",
			"rooms_count": 1,
			"guests_count": 2
		}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf(
			"expected status %d, got %d: %s",
			http.StatusCreated,
			rec.Code,
			rec.Body.String(),
		)
	}

	if !bookingService.called {
		t.Fatal("expected booking service to be called")
	}

	if bookingService.customerID != "customer-123" {
		t.Errorf(
			"expected customer ID customer-123, got %q",
			bookingService.customerID,
		)
	}

	if bookingService.input.RoomTypeID != "room-123" {
		t.Errorf(
			"expected room type ID room-123, got %q",
			bookingService.input.RoomTypeID,
		)
	}

	if bookingService.input.RoomsCount != 1 {
		t.Errorf(
			"expected rooms count 1, got %d",
			bookingService.input.RoomsCount,
		)
	}
}

func TestInitiatePaymentRequiresAuthentication(t *testing.T) {
	r, _, _, _, _ := newTestRouter(t, true)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/bookings/booking-123/payments",
		nil,
	)
	req.Header.Set("Idempotency-Key", "payment-request-123")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf(
			"expected status %d, got %d: %s",
			http.StatusUnauthorized,
			rec.Code,
			rec.Body.String(),
		)
	}
}

func TestRazorpayWebhookIsPublic(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tokenManager, err := auth.NewTokenManager(
		"test-secret-with-at-least-32-bytes-long",
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("create token manager: %v", err)
	}

	webhookService := &fakeWebhookService{}

	r := New(
		nil,
		nil,
		tokenManager,
		&fakeActiveCustomerChecker{},
		&fakeAdminChecker{},
		&fakeHotelService{},
		&fakeRoomService{},
		&fakeInventoryService{},
		&fakeBookingService{},
		&fakePaymentService{},
		webhookService,
	)

	body := `{"event":"payment.captured"}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/webhooks/razorpay",
		strings.NewReader(body),
	)

	// Deliberately do not provide an Authorization header.
	req.Header.Set("x-razorpay-event-id", "evt_public_router_test")
	req.Header.Set("X-Razorpay-Signature", "test-signature")

	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(
			"expected status %d, got %d: %s",
			http.StatusOK,
			rec.Code,
			rec.Body.String(),
		)
	}

	if !webhookService.called {
		t.Fatal("expected webhook service to be called")
	}

	if webhookService.eventID != "evt_public_router_test" {
		t.Errorf(
			"expected event ID %q, got %q",
			"evt_public_router_test",
			webhookService.eventID,
		)
	}

	if webhookService.signature != "test-signature" {
		t.Errorf(
			"expected signature %q, got %q",
			"test-signature",
			webhookService.signature,
		)
	}

	if string(webhookService.body) != body {
		t.Errorf(
			"expected body %q, got %q",
			body,
			string(webhookService.body),
		)
	}
}

func TestLoginRouteEnforcesIPRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	customerService := &fakeRouterCustomerService{}

	tokenManager, err := auth.NewTokenManager(
		"test-secret-with-at-least-32-bytes-long",
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("create token manager: %v", err)
	}

	r := New(
		nil,
		customerService,
		tokenManager,
		&fakeActiveCustomerChecker{},
		&fakeAdminChecker{},
		&fakeHotelService{},
		&fakeRoomService{},
		&fakeInventoryService{},
		&fakeBookingService{},
		&fakePaymentService{},
		nil,
	)

	makeLoginRequest := func(remoteAddr, forwardedFor string) *httptest.ResponseRecorder {
		body := `{
			"email": "alex@example.com",
			"password": "MySecretPassword123"
		}`

		req := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/auth/login",
			strings.NewReader(body),
		)

		req.RemoteAddr = remoteAddr
		req.Header.Set("Content-Type", "application/json")

		if forwardedFor != "" {
			req.Header.Set("X-Forwarded-For", forwardedFor)
		}

		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		return rec
	}

	const clientOne = "192.0.2.1:1234"

	// The first ten requests from one IP should reach the login handler.
	for i := 1; i <= 10; i++ {
		rec := makeLoginRequest(clientOne, "")

		if rec.Code != http.StatusOK {
			t.Fatalf(
				"request %d: expected 200, got %d: %s",
				i,
				rec.Code,
				rec.Body.String(),
			)
		}
	}

	if customerService.loginCalls != 10 {
		t.Fatalf(
			"expected 10 login calls, got %d",
			customerService.loginCalls,
		)
	}

	// The eleventh request should be rejected.
	rec := makeLoginRequest(clientOne, "")

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf(
			"expected 429, got %d: %s",
			rec.Code,
			rec.Body.String(),
		)
	}

	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header")
	}

	if customerService.loginCalls != 10 {
		t.Fatalf(
			"blocked request must not reach login handler; got %d calls",
			customerService.loginCalls,
		)
	}

	// A different IP should have an independent limit.
	rec = makeLoginRequest("192.0.2.2:1234", "")

	if rec.Code != http.StatusOK {
		t.Fatalf(
			"expected different IP to be allowed, got %d",
			rec.Code,
		)
	}

	// Changing X-Forwarded-For must not bypass the original IP limit.
	rec = makeLoginRequest(clientOne, "203.0.113.50")

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf(
			"expected spoofed forwarded IP to be rejected, got %d",
			rec.Code,
		)
	}

	if customerService.loginCalls != 11 {
		t.Fatalf(
			"expected 11 actual login calls, got %d",
			customerService.loginCalls,
		)
	}
}
