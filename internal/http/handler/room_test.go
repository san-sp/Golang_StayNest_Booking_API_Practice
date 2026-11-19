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

	"staynest/internal/room"
)

type fakeRoomService struct {
	created      *room.RoomType
	createErr    error
	createInput  room.RoomType
	createCalled bool

	found     *room.RoomType
	getErr    error
	getID     string
	getCalled bool

	updated      *room.RoomType
	updateErr    error
	updateID     string
	updateInput  room.RoomType
	updateCalled bool

	listed      []room.RoomType
	listErr     error
	listHotelID string
	listCalled  bool
}

func (f *fakeRoomService) Create(
	ctx context.Context,
	input room.RoomType,
) (*room.RoomType, error) {
	f.createCalled = true
	f.createInput = input
	return f.created, f.createErr
}

func (f *fakeRoomService) GetByID(
	ctx context.Context,
	id string,
) (*room.RoomType, error) {
	f.getCalled = true
	f.getID = id
	return f.found, f.getErr
}

func (f *fakeRoomService) ListByHotelID(
	ctx context.Context,
	hotelID string,
) ([]room.RoomType, error) {
	f.listCalled = true
	f.listHotelID = hotelID
	return f.listed, f.listErr
}

func (f *fakeRoomService) Update(
	ctx context.Context,
	id string,
	input room.RoomType,
) (*room.RoomType, error) {
	f.updateCalled = true
	f.updateID = id
	f.updateInput = input
	return f.updated, f.updateErr
}

func validRoomTypeResponse() *room.RoomType {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

	return &room.RoomType{
		ID:                 "room-123",
		HotelID:            "hotel-123",
		Name:               "Deluxe Room",
		Description:        "A comfortable deluxe room",
		MaxOccupancy:       3,
		PricePerNightPaise: 899900,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func TestCreateRoomTypeSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeRoomService{
		created: validRoomTypeResponse(),
	}

	router := gin.New()
	router.POST(
		"/admin/hotels/:id/rooms",
		CreateRoomType(service),
	)

	body := `{
		"name": "Deluxe Room",
		"description": "A comfortable deluxe room",
		"max_occupancy": 3,
		"price_per_night_paise": 899900
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/hotels/hotel-123/rooms",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	if !service.createCalled {
		t.Fatal("expected service Create() to be called")
	}

	if service.createInput.HotelID != "hotel-123" {
		t.Errorf("expected hotel ID hotel-123, got %q", service.createInput.HotelID)
	}

	if service.createInput.Name != "Deluxe Room" {
		t.Errorf("unexpected room name: %q", service.createInput.Name)
	}

	if !strings.Contains(rec.Body.String(), `"id":"room-123"`) {
		t.Errorf("response should contain room ID: %s", rec.Body.String())
	}
}

func TestCreateRoomTypeRejectsInvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeRoomService{}

	router := gin.New()
	router.POST(
		"/admin/hotels/:id/rooms",
		CreateRoomType(service),
	)

	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/hotels/hotel-123/rooms",
		strings.NewReader(`{"name":`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}

	if service.createCalled {
		t.Fatal("service must not be called for invalid JSON")
	}
}

func TestCreateRoomTypeMapsErrors(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
	}{
		{
			name:       "invalid room name",
			serviceErr: room.ErrInvalidRoomName,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "hotel not found",
			serviceErr: room.ErrHotelNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "duplicate room type",
			serviceErr: room.ErrRoomTypeAlreadyExists,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "internal error",
			serviceErr: errors.New("database unavailable"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeRoomService{
				createErr: tt.serviceErr,
			}

			router := gin.New()
			router.POST(
				"/admin/hotels/:id/rooms",
				CreateRoomType(service),
			)

			req := httptest.NewRequest(
				http.MethodPost,
				"/admin/hotels/hotel-123/rooms",
				strings.NewReader(`{
					"name": "Deluxe Room",
					"max_occupancy": 3,
					"price_per_night_paise": 899900
				}`),
			)
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf(
					"expected status %d, got %d: %s",
					tt.wantStatus,
					rec.Code,
					rec.Body.String(),
				)
			}
		})
	}
}

func TestUpdateRoomTypeSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeRoomService{
		updated: validRoomTypeResponse(),
	}

	router := gin.New()
	router.PATCH("/admin/rooms/:id", UpdateRoomType(service))

	body := `{
		"name": "Premium Room",
		"description": "Updated description",
		"max_occupancy": 4,
		"price_per_night_paise": 1099900
	}`

	req := httptest.NewRequest(
		http.MethodPatch,
		"/admin/rooms/room-123",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if !service.updateCalled {
		t.Fatal("expected service Update() to be called")
	}

	if service.updateID != "room-123" {
		t.Errorf("expected room ID room-123, got %q", service.updateID)
	}

	if service.updateInput.Name != "Premium Room" {
		t.Errorf("unexpected room name: %q", service.updateInput.Name)
	}

	if service.updateInput.HotelID != "" {
		t.Errorf("update must not change hotel ID, got %q", service.updateInput.HotelID)
	}
}

func TestUpdateRoomTypeMapsErrors(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
	}{
		{
			name:       "room type not found",
			serviceErr: room.ErrRoomTypeNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "invalid occupancy",
			serviceErr: room.ErrInvalidMaxOccupancy,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "duplicate room type",
			serviceErr: room.ErrRoomTypeAlreadyExists,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "internal error",
			serviceErr: errors.New("database unavailable"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeRoomService{
				updateErr: tt.serviceErr,
			}

			router := gin.New()
			router.PATCH("/admin/rooms/:id", UpdateRoomType(service))

			req := httptest.NewRequest(
				http.MethodPatch,
				"/admin/rooms/room-123",
				strings.NewReader(`{
					"name": "Deluxe Room",
					"max_occupancy": 3,
					"price_per_night_paise": 899900
				}`),
			)
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf(
					"expected status %d, got %d: %s",
					tt.wantStatus,
					rec.Code,
					rec.Body.String(),
				)
			}
		})
	}
}

func TestGetRoomTypeSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeRoomService{
		found: validRoomTypeResponse(),
	}

	router := gin.New()
	router.GET("/rooms/:id", GetRoomType(service))

	req := httptest.NewRequest(
		http.MethodGet,
		"/rooms/room-123",
		nil,
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if !service.getCalled {
		t.Fatal("expected service GetByID() to be called")
	}

	if service.getID != "room-123" {
		t.Errorf("expected room ID room-123, got %q", service.getID)
	}
}

func TestGetRoomTypeNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeRoomService{
		getErr: room.ErrRoomTypeNotFound,
	}

	router := gin.New()
	router.GET("/rooms/:id", GetRoomType(service))

	req := httptest.NewRequest(
		http.MethodGet,
		"/rooms/room-123",
		nil,
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}
}

func TestListRoomTypesSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeRoomService{
		listed: []room.RoomType{
			*validRoomTypeResponse(),
		},
	}

	router := gin.New()
	router.GET(
		"/hotels/:id/rooms",
		ListRoomTypesByHotelID(service),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/hotels/hotel-123/rooms",
		nil,
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if !service.listCalled {
		t.Fatal("expected service ListByHotelID() to be called")
	}

	if service.listHotelID != "hotel-123" {
		t.Errorf("expected hotel ID hotel-123, got %q", service.listHotelID)
	}

	if !strings.Contains(rec.Body.String(), `"name":"Deluxe Room"`) {
		t.Errorf("expected room in response: %s", rec.Body.String())
	}
}

func TestListRoomTypesEmptyResult(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeRoomService{
		listed: []room.RoomType{},
	}

	router := gin.New()
	router.GET(
		"/hotels/:id/rooms",
		ListRoomTypesByHotelID(service),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/hotels/hotel-123/rooms",
		nil,
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), `"rooms":[]`) {
		t.Errorf("expected empty rooms array: %s", rec.Body.String())
	}
}

func TestListRoomTypesHotelNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeRoomService{
		listErr: room.ErrHotelNotFound,
	}

	router := gin.New()
	router.GET(
		"/hotels/:id/rooms",
		ListRoomTypesByHotelID(service),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/hotels/hotel-123/rooms",
		nil,
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}
}
