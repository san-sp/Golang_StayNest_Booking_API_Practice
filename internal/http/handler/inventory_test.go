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

	"staynest/internal/inventory"
)

type fakeInventoryService struct {
	setResult *inventory.DailyInventory
	setErr    error
	setCalled bool
	setID     string
	setDate   time.Time
	setTotal  int

	listResult []inventory.DailyInventory
	listErr    error
	listCalled bool
	listID     string
	listStart  time.Time
	listEnd    time.Time
}

func (f *fakeInventoryService) SetDailyCapacity(
	ctx context.Context,
	roomTypeID string,
	stayDate time.Time,
	totalRooms int,
) (*inventory.DailyInventory, error) {
	f.setCalled = true
	f.setID = roomTypeID
	f.setDate = stayDate
	f.setTotal = totalRooms

	return f.setResult, f.setErr
}

func (f *fakeInventoryService) ListByRoomTypeAndDateRange(
	ctx context.Context,
	roomTypeID string,
	startDate time.Time,
	endDate time.Time,
) ([]inventory.DailyInventory, error) {
	f.listCalled = true
	f.listID = roomTypeID
	f.listStart = startDate
	f.listEnd = endDate

	return f.listResult, f.listErr
}

func validDailyInventory() *inventory.DailyInventory {
	return &inventory.DailyInventory{
		RoomTypeID:     "room-123",
		StayDate:       time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
		TotalRooms:     10,
		AvailableRooms: 7,
		UpdatedAt:      time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC),
	}
}

func TestSetDailyInventorySuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeInventoryService{
		setResult: validDailyInventory(),
	}

	router := gin.New()
	router.PUT(
		"/admin/rooms/:id/inventory/:date",
		SetDailyInventory(service),
	)

	req := httptest.NewRequest(
		http.MethodPut,
		"/admin/rooms/room-123/inventory/2026-11-15",
		strings.NewReader(`{"total_rooms":10}`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if !service.setCalled {
		t.Fatal("expected SetDailyCapacity() to be called")
	}

	if service.setID != "room-123" {
		t.Errorf("expected room ID room-123, got %q", service.setID)
	}

	if service.setTotal != 10 {
		t.Errorf("expected total rooms 10, got %d", service.setTotal)
	}

	wantDate := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	if !service.setDate.Equal(wantDate) {
		t.Errorf("expected date %v, got %v", wantDate, service.setDate)
	}

	if !strings.Contains(rec.Body.String(), `"available_rooms":7`) {
		t.Errorf("response should contain available rooms: %s", rec.Body.String())
	}
}

func TestSetDailyInventoryRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name       string
		pathDate   string
		body       string
		wantStatus int
	}{
		{
			name:       "invalid date",
			pathDate:   "15-11-2026",
			body:       `{"total_rooms":10}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid JSON",
			pathDate:   "2026-11-15",
			body:       `{"total_rooms":`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "negative capacity",
			pathDate:   "2026-11-15",
			body:       `{"total_rooms":-1}`,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeInventoryService{
				setErr: inventory.ErrInvalidTotalRooms,
			}

			router := gin.New()
			router.PUT(
				"/admin/rooms/:id/inventory/:date",
				SetDailyInventory(service),
			)

			req := httptest.NewRequest(
				http.MethodPut,
				"/admin/rooms/room-123/inventory/"+tt.pathDate,
				strings.NewReader(tt.body),
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

			if tt.name != "negative capacity" && service.setCalled {
				t.Fatal("service must not be called for malformed input")
			}
		})
	}
}

func TestSetDailyInventoryMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
	}{
		{
			name:       "room type not found",
			serviceErr: inventory.ErrRoomTypeNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "capacity below booked rooms",
			serviceErr: inventory.ErrCapacityBelowBooked,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "invalid room type ID",
			serviceErr: inventory.ErrInvalidRoomTypeID,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "internal error",
			serviceErr: errors.New("database unavailable"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeInventoryService{
				setErr: tt.serviceErr,
			}

			router := gin.New()
			router.PUT(
				"/admin/rooms/:id/inventory/:date",
				SetDailyInventory(service),
			)

			req := httptest.NewRequest(
				http.MethodPut,
				"/admin/rooms/room-123/inventory/2026-11-15",
				strings.NewReader(`{"total_rooms":10}`),
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

func TestListRoomInventorySuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeInventoryService{
		listResult: []inventory.DailyInventory{
			*validDailyInventory(),
		},
	}

	router := gin.New()
	router.GET(
		"/rooms/:id/inventory",
		ListRoomInventory(service),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/rooms/room-123/inventory?start_date=2026-11-15&end_date=2026-11-20",
		nil,
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if !service.listCalled {
		t.Fatal("expected ListByRoomTypeAndDateRange() to be called")
	}

	if service.listID != "room-123" {
		t.Errorf("expected room ID room-123, got %q", service.listID)
	}

	if service.listStart.Format("2006-01-02") != "2026-11-15" {
		t.Errorf("unexpected start date: %v", service.listStart)
	}

	if service.listEnd.Format("2006-01-02") != "2026-11-20" {
		t.Errorf("unexpected end date: %v", service.listEnd)
	}

	if !strings.Contains(rec.Body.String(), `"inventory":[`) {
		t.Errorf("response should contain inventory array: %s", rec.Body.String())
	}
}

func TestListRoomInventoryRejectsInvalidDateRange(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{
			name:  "missing start date",
			query: "?end_date=2026-11-20",
		},
		{
			name:  "invalid start date",
			query: "?start_date=15-11-2026&end_date=2026-11-20",
		},
		{
			name:  "invalid end date",
			query: "?start_date=2026-11-15&end_date=20-11-2026",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeInventoryService{}

			router := gin.New()
			router.GET(
				"/rooms/:id/inventory",
				ListRoomInventory(service),
			)

			req := httptest.NewRequest(
				http.MethodGet,
				"/rooms/room-123/inventory"+tt.query,
				nil,
			)

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d", rec.Code)
			}

			if service.listCalled {
				t.Fatal("service must not be called for invalid dates")
			}
		})
	}
}

func TestListRoomInventoryEmptyResult(t *testing.T) {
	service := &fakeInventoryService{
		listResult: make([]inventory.DailyInventory, 0),
	}

	router := gin.New()
	router.GET(
		"/rooms/:id/inventory",
		ListRoomInventory(service),
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/rooms/room-123/inventory?start_date=2026-11-15&end_date=2026-11-20",
		nil,
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), `"inventory":[]`) {
		t.Errorf("expected empty inventory array, got %s", rec.Body.String())
	}
}

func TestListRoomInventoryMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
	}{
		{
			name:       "room type not found",
			serviceErr: inventory.ErrRoomTypeNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "invalid date range",
			serviceErr: inventory.ErrInvalidDateRange,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "internal error",
			serviceErr: errors.New("database unavailable"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeInventoryService{
				listErr: tt.serviceErr,
			}

			router := gin.New()
			router.GET(
				"/rooms/:id/inventory",
				ListRoomInventory(service),
			)

			req := httptest.NewRequest(
				http.MethodGet,
				"/rooms/room-123/inventory?start_date=2026-11-15&end_date=2026-11-20",
				nil,
			)

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
