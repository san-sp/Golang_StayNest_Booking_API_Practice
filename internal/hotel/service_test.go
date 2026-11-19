package hotel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type fakeHotelRepository struct {
	created   *Hotel
	createErr error

	found  *Hotel
	getErr error
	gotID  string

	updated     *Hotel
	updateErr   error
	updateID    string
	updateInput Hotel

	listed     *ListResult
	listErr    error
	listParams ListParams
	listCalled bool

	searchResults []SearchResult
	searchErr     error
	searchParams  SearchParams
	searchCalled  bool
}

func (f *fakeHotelRepository) Create(
	ctx context.Context,
	hotel Hotel,
) (*Hotel, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}

	hotel.ID = "test-hotel-id"
	f.created = &hotel

	return &hotel, nil
}

func (f *fakeHotelRepository) GetByID(
	ctx context.Context,
	id string,
) (*Hotel, error) {
	f.gotID = id
	return f.found, f.getErr
}

func (f *fakeHotelRepository) Update(
	ctx context.Context,
	id string,
	hotel Hotel,
) (*Hotel, error) {
	f.updateID = id
	f.updateInput = hotel

	if f.updateErr != nil {
		return nil, f.updateErr
	}

	hotel.ID = id
	f.updated = &hotel

	return &hotel, nil
}

func (f *fakeHotelRepository) List(
	ctx context.Context,
	params ListParams,
) (*ListResult, error) {
	f.listCalled = true
	f.listParams = params

	if f.listErr != nil {
		return nil, f.listErr
	}

	return f.listed, nil
}

func (f *fakeHotelRepository) Search(
	ctx context.Context,
	params SearchParams,
) ([]SearchResult, error) {
	f.searchCalled = true
	f.searchParams = params

	if f.searchErr != nil {
		return nil, f.searchErr
	}

	return f.searchResults, nil
}

func validTestHotel() Hotel {
	return Hotel{
		Name:        "StayNest Hotel",
		Description: "A comfortable hotel",
		Address:     "123 Test Street",
		City:        "Thiruvananthapuram",
		Country:     "India",
		Timezone:    "Asia/Kolkata",
	}
}

func TestServiceCreateHotel(t *testing.T) {
	repo := &fakeHotelRepository{}
	service := NewService(repo)

	input := validTestHotel()
	input.Name = "  StayNest Hotel  "
	input.City = " Thiruvananthapuram "

	created, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if created.ID != "test-hotel-id" {
		t.Errorf("unexpected hotel ID: %q", created.ID)
	}

	if repo.created == nil {
		t.Fatal("expected repository Create() to be called")
	}

	if repo.created.Name != "StayNest Hotel" {
		t.Errorf("expected trimmed name, got %q", repo.created.Name)
	}

	if repo.created.City != "Thiruvananthapuram" {
		t.Errorf("expected trimmed city, got %q", repo.created.City)
	}
}

func TestServiceCreateTrimsTimezone(t *testing.T) {
	repo := &fakeHotelRepository{}
	service := NewService(repo)

	input := validTestHotel()
	input.Timezone = "  Asia/Kolkata  "

	_, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if repo.created == nil {
		t.Fatal("expected repository Create() to be called")
	}

	if repo.created.Timezone != "Asia/Kolkata" {
		t.Errorf(
			"expected trimmed timezone %q, got %q",
			"Asia/Kolkata",
			repo.created.Timezone,
		)
	}
}

func TestServiceCreateRejectsInvalidHotel(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Hotel)
		wantErr error
	}{
		{
			name: "empty name",
			modify: func(h *Hotel) {
				h.Name = " "
			},
			wantErr: ErrInvalidHotelName,
		},
		{
			name: "name too long",
			modify: func(h *Hotel) {
				h.Name = string(make([]byte, 151))
			},
			wantErr: ErrHotelNameTooLong,
		},
		{
			name: "empty address",
			modify: func(h *Hotel) {
				h.Address = " "
			},
			wantErr: ErrInvalidHotelAddress,
		},
		{
			name: "empty city",
			modify: func(h *Hotel) {
				h.City = " "
			},
			wantErr: ErrInvalidHotelCity,
		},
		{
			name: "city too long",
			modify: func(h *Hotel) {
				h.City = string(make([]byte, 101))
			},
			wantErr: ErrHotelCityTooLong,
		},
		{
			name: "empty country",
			modify: func(h *Hotel) {
				h.Country = " "
			},
			wantErr: ErrInvalidHotelCountry,
		},
		{
			name: "country too long",
			modify: func(h *Hotel) {
				h.Country = string(make([]byte, 101))
			},
			wantErr: ErrHotelCountryTooLong,
		},
		{
			name: "empty timezone",
			modify: func(h *Hotel) {
				h.Timezone = " "
			},
			wantErr: ErrInvalidHotelTimezone,
		},
		{
			name: "invalid timezone",
			modify: func(h *Hotel) {
				h.Timezone = "Invalid/Timezone"
			},
			wantErr: ErrInvalidHotelTimezone,
		},
		{
			name: "invalid timezone with surrounding whitespace",
			modify: func(h *Hotel) {
				h.Timezone = " Invalid/Timezone "
			},
			wantErr: ErrInvalidHotelTimezone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeHotelRepository{}
			service := NewService(repo)

			input := validTestHotel()
			tt.modify(&input)

			_, err := service.Create(context.Background(), input)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}

			if repo.created != nil {
				t.Fatal("repository must not be called for invalid input")
			}
		})
	}
}

func TestServiceCreatePropagatesRepositoryError(t *testing.T) {
	repositoryErr := errors.New("database unavailable")

	repo := &fakeHotelRepository{
		createErr: repositoryErr,
	}
	service := NewService(repo)

	_, err := service.Create(context.Background(), validTestHotel())

	if !errors.Is(err, repositoryErr) {
		t.Fatalf("expected repository error, got %v", err)
	}
}

func TestServiceGetHotelByID(t *testing.T) {
	repo := &fakeHotelRepository{
		found: &Hotel{
			ID:   "hotel-123",
			Name: "StayNest Hotel",
		},
	}
	service := NewService(repo)

	found, err := service.GetByID(context.Background(), "hotel-123")
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}

	if found.ID != "hotel-123" {
		t.Errorf("unexpected hotel ID: %q", found.ID)
	}

	if repo.gotID != "hotel-123" {
		t.Errorf("expected ID hotel-123, got %q", repo.gotID)
	}
}

func TestServiceGetHotelNotFound(t *testing.T) {
	repo := &fakeHotelRepository{
		getErr: pgx.ErrNoRows,
	}
	service := NewService(repo)

	_, err := service.GetByID(context.Background(), "missing-hotel")

	if !errors.Is(err, ErrHotelNotFound) {
		t.Fatalf("expected ErrHotelNotFound, got %v", err)
	}
}

func TestServiceGetHotelPropagatesRepositoryError(t *testing.T) {
	repositoryErr := errors.New("database unavailable")

	repo := &fakeHotelRepository{
		getErr: repositoryErr,
	}
	service := NewService(repo)

	_, err := service.GetByID(context.Background(), "hotel-123")

	if !errors.Is(err, repositoryErr) {
		t.Fatalf("expected repository error, got %v", err)
	}
}

func TestServiceUpdateHotel(t *testing.T) {
	repo := &fakeHotelRepository{}
	service := NewService(repo)

	input := validTestHotel()
	input.Name = "  Updated StayNest Hotel  "

	updated, err := service.Update(
		context.Background(),
		"hotel-123",
		input,
	)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if updated.ID != "hotel-123" {
		t.Errorf("unexpected hotel ID: %q", updated.ID)
	}

	if repo.updateID != "hotel-123" {
		t.Errorf("expected ID hotel-123, got %q", repo.updateID)
	}

	if repo.updateInput.Name != "Updated StayNest Hotel" {
		t.Errorf("expected trimmed name, got %q", repo.updateInput.Name)
	}
}

func TestServiceUpdateHotelNotFound(t *testing.T) {
	repo := &fakeHotelRepository{
		updateErr: pgx.ErrNoRows,
	}
	service := NewService(repo)

	_, err := service.Update(
		context.Background(),
		"missing-hotel",
		validTestHotel(),
	)

	if !errors.Is(err, ErrHotelNotFound) {
		t.Fatalf("expected ErrHotelNotFound, got %v", err)
	}
}

func TestServiceCreateAcceptsInternationalTimezone(t *testing.T) {
	repo := &fakeHotelRepository{}
	service := NewService(repo)

	input := validTestHotel()
	input.Timezone = "Europe/Zurich"

	_, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if repo.created.Timezone != "Europe/Zurich" {
		t.Errorf(
			"expected timezone %q, got %q",
			"Europe/Zurich",
			repo.created.Timezone,
		)
	}
}

func TestServiceUpdateRejectsInvalidTimezone(t *testing.T) {
	repo := &fakeHotelRepository{}
	service := NewService(repo)

	input := validTestHotel()
	input.Timezone = "Invalid/Timezone"

	_, err := service.Update(
		context.Background(),
		"hotel-123",
		input,
	)

	if !errors.Is(err, ErrInvalidHotelTimezone) {
		t.Fatalf(
			"expected ErrInvalidHotelTimezone, got %v",
			err,
		)
	}

	if repo.updated != nil {
		t.Fatal("repository must not be called for invalid timezone")
	}
}

func TestServiceUpdateHotelRejectsInvalidInput(t *testing.T) {
	repo := &fakeHotelRepository{}
	service := NewService(repo)

	input := validTestHotel()
	input.Name = ""

	_, err := service.Update(
		context.Background(),
		"hotel-123",
		input,
	)

	if !errors.Is(err, ErrInvalidHotelName) {
		t.Fatalf("expected ErrInvalidHotelName, got %v", err)
	}

	if repo.updated != nil {
		t.Fatal("repository must not be called for invalid input")
	}
}

func TestServiceUpdateHotelPropagatesRepositoryError(t *testing.T) {
	repositoryErr := errors.New("database unavailable")

	repo := &fakeHotelRepository{
		updateErr: repositoryErr,
	}
	service := NewService(repo)

	_, err := service.Update(
		context.Background(),
		"hotel-123",
		validTestHotel(),
	)

	if !errors.Is(err, repositoryErr) {
		t.Fatalf("expected repository error, got %v", err)
	}
}

func TestServiceListHotels(t *testing.T) {
	tests := []struct {
		name           string
		params         ListParams
		expectedLimit  int
		expectedOffset int
		expectedCity   string
	}{
		{
			name:           "default pagination",
			params:         ListParams{},
			expectedLimit:  defaultHotelListLimit,
			expectedOffset: 0,
		},
		{
			name: "custom pagination and trimmed city",
			params: ListParams{
				City:   "  Kochi  ",
				Limit:  10,
				Offset: 5,
			},
			expectedLimit:  10,
			expectedOffset: 5,
			expectedCity:   "Kochi",
		},
		{
			name: "limit capped at maximum",
			params: ListParams{
				Limit:  500,
				Offset: 10,
			},
			expectedLimit:  maxHotelListLimit,
			expectedOffset: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeHotelRepository{
				listed: &ListResult{
					Hotels: []Hotel{},
					Total:  25,
				},
			}

			service := NewService(repo)

			result, err := service.List(context.Background(), tt.params)
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}

			if !repo.listCalled {
				t.Fatal("expected repository List() to be called")
			}

			if repo.listParams.Limit != tt.expectedLimit {
				t.Errorf(
					"expected limit %d, got %d",
					tt.expectedLimit,
					repo.listParams.Limit,
				)
			}

			if repo.listParams.Offset != tt.expectedOffset {
				t.Errorf(
					"expected offset %d, got %d",
					tt.expectedOffset,
					repo.listParams.Offset,
				)
			}

			if repo.listParams.City != tt.expectedCity {
				t.Errorf(
					"expected city %q, got %q",
					tt.expectedCity,
					repo.listParams.City,
				)
			}

			if result.Limit != tt.expectedLimit {
				t.Errorf("expected result limit %d, got %d",
					tt.expectedLimit, result.Limit)
			}

			if result.Offset != tt.expectedOffset {
				t.Errorf("expected result offset %d, got %d",
					tt.expectedOffset, result.Offset)
			}
		})
	}
}

func TestServiceListRejectsInvalidLimit(t *testing.T) {
	repo := &fakeHotelRepository{}
	service := NewService(repo)

	_, err := service.List(context.Background(), ListParams{
		Limit: -1,
	})

	if !errors.Is(err, ErrInvalidHotelListLimit) {
		t.Fatalf("expected ErrInvalidHotelListLimit, got %v", err)
	}

	if repo.listCalled {
		t.Fatal("repository should not be called for invalid limit")
	}
}

func TestServiceListRejectsInvalidOffset(t *testing.T) {
	repo := &fakeHotelRepository{}
	service := NewService(repo)

	_, err := service.List(context.Background(), ListParams{
		Limit:  20,
		Offset: -1,
	})

	if !errors.Is(err, ErrInvalidHotelListOffset) {
		t.Fatalf("expected ErrInvalidHotelListOffset, got %v", err)
	}

	if repo.listCalled {
		t.Fatal("repository should not be called for invalid offset")
	}
}

func TestServiceListPropagatesRepositoryError(t *testing.T) {
	repoErr := errors.New("database connection failed")

	repo := &fakeHotelRepository{
		listErr: repoErr,
	}

	service := NewService(repo)

	_, err := service.List(context.Background(), ListParams{
		Limit: 20,
	})

	if !errors.Is(err, repoErr) {
		t.Fatalf("expected repository error, got %v", err)
	}
}

func TestServiceSearchNormalizesISTDates(t *testing.T) {
	repo := &fakeHotelRepository{}
	service := NewService(repo)

	ist := time.FixedZone("IST", 5*60*60+30*60)

	checkIn := time.Date(
		2026, time.November, 14,
		15, 30, 0, 0,
		ist,
	)

	checkOut := time.Date(
		2026, time.November, 17,
		11, 45, 0, 0,
		ist,
	)

	_, err := service.Search(
		context.Background(),
		SearchParams{
			City:     "Thiruvananthapuram",
			CheckIn:  checkIn,
			CheckOut: checkOut,
			Guests:   2,
			Rooms:    1,
		},
	)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if !repo.searchCalled {
		t.Fatal("expected repository Search() to be called")
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

	if !repo.searchParams.CheckIn.Equal(expectedCheckIn) {
		t.Errorf(
			"expected check-in %v, got %v",
			expectedCheckIn,
			repo.searchParams.CheckIn,
		)
	}

	if !repo.searchParams.CheckOut.Equal(expectedCheckOut) {
		t.Errorf(
			"expected check-out %v, got %v",
			expectedCheckOut,
			repo.searchParams.CheckOut,
		)
	}

	if repo.searchParams.CheckIn.Location() != time.UTC {
		t.Error("expected check-in location to be UTC")
	}

	if repo.searchParams.CheckOut.Location() != time.UTC {
		t.Error("expected check-out location to be UTC")
	}
}
