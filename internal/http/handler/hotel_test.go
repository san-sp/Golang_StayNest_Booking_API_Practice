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

	"staynest/internal/hotel"
)

type fakeHotelService struct {
	created      *hotel.Hotel
	createErr    error
	createInput  hotel.Hotel
	createCalled bool

	found     *hotel.Hotel
	getErr    error
	getID     string
	getCalled bool

	updated      *hotel.Hotel
	updateErr    error
	updateID     string
	updateInput  hotel.Hotel
	updateCalled bool

	listed        *hotel.ListResult
	listErr       error
	listParams    hotel.ListParams
	listCalled    bool
	searchResults []hotel.SearchResult
	searchErr     error
	searchParams  hotel.SearchParams
	searchCalled  bool
}

func (f *fakeHotelService) GetByID(
	ctx context.Context,
	id string,
) (*hotel.Hotel, error) {
	f.getCalled = true
	f.getID = id

	return f.found, f.getErr
}

func (f *fakeHotelService) Create(
	ctx context.Context,
	input hotel.Hotel,
) (*hotel.Hotel, error) {
	f.createCalled = true
	f.createInput = input

	return f.created, f.createErr
}

func (f *fakeHotelService) Update(
	ctx context.Context,
	id string,
	input hotel.Hotel,
) (*hotel.Hotel, error) {
	f.updateCalled = true
	f.updateID = id
	f.updateInput = input

	return f.updated, f.updateErr
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

func validHotelResponse() *hotel.Hotel {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

	return &hotel.Hotel{
		ID:          "hotel-123",
		Name:        "StayNest Hotel",
		Description: "A comfortable hotel",
		Address:     "123 Test Street",
		City:        "Thiruvananthapuram",
		Country:     "India",
		Timezone:    "Asia/Kolkata",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func TestCreateHotelSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeHotelService{
		created: validHotelResponse(),
	}

	router := gin.New()
	router.POST("/admin/hotels", CreateHotel(service))

	body := `{
		"name": "StayNest Hotel",
		"description": "A comfortable hotel",
		"address": "123 Test Street",
		"city": "Thiruvananthapuram",
		"country": "India",
		"timezone": "Asia/Kolkata"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/hotels",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf(
			"expected status 201, got %d: %s",
			rec.Code,
			rec.Body.String(),
		)
	}

	if !service.createCalled {
		t.Fatal("expected service Create() to be called")
	}

	if service.createInput.Name != "StayNest Hotel" {
		t.Errorf("unexpected hotel name: %q", service.createInput.Name)
	}

	if service.createInput.City != "Thiruvananthapuram" {
		t.Errorf("unexpected city: %q", service.createInput.City)
	}

	if !strings.Contains(rec.Body.String(), `"id":"hotel-123"`) {
		t.Errorf("response should contain hotel ID: %s", rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), `"created_at"`) {
		t.Errorf("response should contain created_at: %s", rec.Body.String())
	}
}

func TestCreateHotelRejectsInvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeHotelService{}

	router := gin.New()
	router.POST("/admin/hotels", CreateHotel(service))

	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/hotels",
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

func TestCreateHotelRejectsInvalidInput(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeHotelService{
		createErr: hotel.ErrInvalidHotelName,
	}

	router := gin.New()
	router.POST("/admin/hotels", CreateHotel(service))

	body := `{
		"name": "",
		"address": "123 Test Street",
		"city": "Kochi",
		"country": "India"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/hotels",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}

	if !service.createCalled {
		t.Fatal("expected service Create() to be called")
	}
}

func TestCreateHotelHidesInternalErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeHotelService{
		createErr: errors.New("database connection failed"),
	}

	router := gin.New()
	router.POST("/admin/hotels", CreateHotel(service))

	body := `{
		"name": "StayNest Hotel",
		"address": "123 Test Street",
		"city": "Kochi",
		"country": "India",
		"timezone": "Asia/Kolkata"
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/hotels",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}

	if strings.Contains(rec.Body.String(), "database connection failed") {
		t.Fatal("internal error details must not be exposed")
	}
}

func TestUpdateHotelSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeHotelService{
		updated: validHotelResponse(),
	}

	router := gin.New()
	router.PATCH("/admin/hotels/:id", UpdateHotel(service))

	body := `{
		"name": "Updated StayNest Hotel",
		"description": "Updated description",
		"address": "456 New Street",
		"city": "Kochi",
		"country": "India"
	}`

	req := httptest.NewRequest(
		http.MethodPatch,
		"/admin/hotels/hotel-123",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(
			"expected status 200, got %d: %s",
			rec.Code,
			rec.Body.String(),
		)
	}

	if !service.updateCalled {
		t.Fatal("expected service Update() to be called")
	}

	if service.updateID != "hotel-123" {
		t.Errorf("expected hotel ID hotel-123, got %q", service.updateID)
	}

	if service.updateInput.Name != "Updated StayNest Hotel" {
		t.Errorf("unexpected updated name: %q", service.updateInput.Name)
	}
}

func TestUpdateHotelNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeHotelService{
		updateErr: hotel.ErrHotelNotFound,
	}

	router := gin.New()
	router.PATCH("/admin/hotels/:id", UpdateHotel(service))

	body := `{
		"name": "StayNest Hotel",
		"address": "123 Test Street",
		"city": "Kochi",
		"country": "India",
		"timezone": "Asia/Kolkata"
	}`

	req := httptest.NewRequest(
		http.MethodPatch,
		"/admin/hotels/missing-hotel",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateHotelRejectsInvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeHotelService{}

	router := gin.New()
	router.PATCH("/admin/hotels/:id", UpdateHotel(service))

	req := httptest.NewRequest(
		http.MethodPatch,
		"/admin/hotels/hotel-123",
		strings.NewReader(`{"name":`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}

	if service.updateCalled {
		t.Fatal("service must not be called for invalid JSON")
	}
}

func TestUpdateHotelHidesInternalErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeHotelService{
		updateErr: errors.New("database connection failed"),
	}

	router := gin.New()
	router.PATCH("/admin/hotels/:id", UpdateHotel(service))

	body := `{
		"name": "StayNest Hotel",
		"address": "123 Test Street",
		"city": "Kochi",
		"country": "India",
		"timezone": "Asia/Kolkata"
	}`

	req := httptest.NewRequest(
		http.MethodPatch,
		"/admin/hotels/hotel-123",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}

	if strings.Contains(rec.Body.String(), "database connection failed") {
		t.Fatal("internal error details must not be exposed")
	}
}

func TestGetHotelSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeHotelService{
		found: validHotelResponse(),
	}

	router := gin.New()
	router.GET("/hotels/:id", GetHotel(service))

	req := httptest.NewRequest(
		http.MethodGet,
		"/hotels/hotel-123",
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

	if service.getID != "hotel-123" {
		t.Errorf("expected hotel ID hotel-123, got %q", service.getID)
	}

	if !strings.Contains(rec.Body.String(), `"name":"StayNest Hotel"`) {
		t.Errorf("response should contain hotel name: %s", rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), `"created_at"`) {
		t.Errorf("response should contain created_at: %s", rec.Body.String())
	}
}

func TestGetHotelNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeHotelService{
		getErr: hotel.ErrHotelNotFound,
	}

	router := gin.New()
	router.GET("/hotels/:id", GetHotel(service))

	req := httptest.NewRequest(
		http.MethodGet,
		"/hotels/missing-hotel",
		nil,
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d: %s", rec.Code, rec.Body.String())
	}

	if !service.getCalled {
		t.Fatal("expected service GetByID() to be called")
	}
}

func TestGetHotelHidesInternalErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeHotelService{
		getErr: errors.New("database connection failed"),
	}

	router := gin.New()
	router.GET("/hotels/:id", GetHotel(service))

	req := httptest.NewRequest(
		http.MethodGet,
		"/hotels/hotel-123",
		nil,
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d: %s", rec.Code, rec.Body.String())
	}

	if strings.Contains(rec.Body.String(), "database connection failed") {
		t.Fatal("internal error details must not be exposed")
	}
}

func TestListHotelsSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeHotelService{
		listed: &hotel.ListResult{
			Hotels: []hotel.Hotel{
				*validHotelResponse(),
			},
			Total:  35,
			Limit:  10,
			Offset: 5,
		},
	}

	router := gin.New()
	router.GET("/hotels", ListHotels(service))

	req := httptest.NewRequest(
		http.MethodGet,
		"/hotels?city=Thiruvananthapuram&limit=10&offset=5",
		nil,
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if !service.listCalled {
		t.Fatal("expected service List() to be called")
	}

	if service.listParams.City != "Thiruvananthapuram" {
		t.Errorf("unexpected city: %q", service.listParams.City)
	}

	if service.listParams.Limit != 10 || service.listParams.Offset != 5 {
		t.Errorf("unexpected pagination params: %+v", service.listParams)
	}

	for _, expected := range []string{
		`"total":35`,
		`"limit":10`,
		`"offset":5`,
		`"name":"StayNest Hotel"`,
	} {
		if !strings.Contains(rec.Body.String(), expected) {
			t.Errorf("response should contain %s: %s", expected, rec.Body.String())
		}
	}
}

func TestListHotelsRejectsInvalidLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeHotelService{}
	router := gin.New()
	router.GET("/hotels", ListHotels(service))

	req := httptest.NewRequest(http.MethodGet, "/hotels?limit=abc", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}

	if service.listCalled {
		t.Fatal("service should not be called for an invalid limit")
	}
}

func TestListHotelsRejectsInvalidOffset(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeHotelService{}
	router := gin.New()
	router.GET("/hotels", ListHotels(service))

	req := httptest.NewRequest(http.MethodGet, "/hotels?offset=-1", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}

	if service.listCalled {
		t.Fatal("service should not be called for an invalid offset")
	}
}

func TestListHotelsHidesInternalErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeHotelService{
		listErr: errors.New("database connection failed"),
	}

	router := gin.New()
	router.GET("/hotels", ListHotels(service))

	req := httptest.NewRequest(http.MethodGet, "/hotels", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}

	if strings.Contains(rec.Body.String(), "database connection failed") {
		t.Fatal("internal error details must not be exposed")
	}
}

func TestListHotelsEmptyResult(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeHotelService{
		listed: &hotel.ListResult{
			Hotels: []hotel.Hotel{},
			Total:  0,
			Limit:  20,
			Offset: 0,
		},
	}

	router := gin.New()
	router.GET("/hotels", ListHotels(service))

	req := httptest.NewRequest(http.MethodGet, "/hotels", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), `"hotels":[]`) {
		t.Errorf("expected empty hotels array, got: %s", rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), `"total":0`) {
		t.Errorf("expected total 0, got: %s", rec.Body.String())
	}
}

func TestListHotelsRejectsNonIntegerOffset(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &fakeHotelService{}
	router := gin.New()
	router.GET("/hotels", ListHotels(service))

	req := httptest.NewRequest(http.MethodGet, "/hotels?offset=abc", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}

	if service.listCalled {
		t.Fatal("service should not be called for an invalid offset")
	}
}
