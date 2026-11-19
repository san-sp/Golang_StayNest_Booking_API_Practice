package customer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"staynest/internal/auth"

	"github.com/jackc/pgx/v5"
)

func newTestTokenManager(t *testing.T) *auth.TokenManager {
	t.Helper()

	manager, err := auth.NewTokenManager(
		"staynest-test-secret-at-least-32-bytes-long",
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	return manager
}

type fakeCustomerRepository struct {
	created   *Customer
	createErr error

	customer *Customer
	getErr   error

	customerByID *Customer
	getByIDErr   error

	updated   *Customer
	updateErr error

	updatedPasswordHash string
	updatePasswordErr   error

	deactivatedID string
	deactivateErr error
}

func (f *fakeCustomerRepository) Create(
	ctx context.Context,
	customer Customer,
) (*Customer, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}

	customer.ID = "test-customer-id"
	f.created = &customer

	return &customer, nil
}

func (f *fakeCustomerRepository) GetByEmail(
	ctx context.Context,
	email string,
) (*Customer, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}

	return f.customer, nil
}

func (f *fakeCustomerRepository) UpdateName(
	ctx context.Context,
	id string,
	name string,
) (*Customer, error) {
	if f.updateErr != nil {
		return nil, f.updateErr
	}

	if f.customerByID == nil {
		return nil, pgx.ErrNoRows
	}

	updated := *f.customerByID
	updated.ID = id
	updated.Name = name

	f.updated = &updated

	return &updated, nil
}

func (f *fakeCustomerRepository) UpdatePasswordHash(
	ctx context.Context,
	id string,
	passwordHash string,
) error {
	if f.updatePasswordErr != nil {
		return f.updatePasswordErr
	}

	if f.customerByID == nil {
		return pgx.ErrNoRows
	}

	f.updatedPasswordHash = passwordHash

	return nil
}

func (f *fakeCustomerRepository) Deactivate(
	ctx context.Context,
	id string,
) error {
	if f.deactivateErr != nil {
		return f.deactivateErr
	}

	if f.customerByID == nil {
		return pgx.ErrNoRows
	}

	f.deactivatedID = id

	return nil
}

func TestServiceRegister(t *testing.T) {
	repo := &fakeCustomerRepository{}
	service := NewService(repo, newTestTokenManager(t))

	created, err := service.Register(
		context.Background(),
		"  Alex Smith  ",
		"  ALEX@EXAMPLE.COM  ",
		"MySecretPassword123",
	)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if created.Name != "Alex Smith" {
		t.Errorf("expected trimmed name, got %q", created.Name)
	}

	if created.Email != "alex@example.com" {
		t.Errorf("expected normalized email, got %q", created.Email)
	}

	if created.PasswordHash == "MySecretPassword123" {
		t.Fatal("password must not be stored as plain text")
	}

	match, err := auth.VerifyPassword(
		"MySecretPassword123",
		created.PasswordHash,
	)
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}

	if !match {
		t.Fatal("stored password hash does not match original password")
	}

	if repo.created == nil {
		t.Fatal("expected repository Create() to be called")
	}
}

func TestServiceRegisterRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name         string
		customerName string
		email        string
		password     string
		wantErr      error
	}{
		{
			name:         "empty name",
			customerName: "   ",
			email:        "alex@example.com",
			password:     "Password123",
			wantErr:      ErrInvalidName,
		},
		{
			name:         "invalid email",
			customerName: "Alex",
			email:        "invalid-email",
			password:     "Password123",
			wantErr:      ErrInvalidEmail,
		},
		{
			name:         "short password",
			customerName: "Alex",
			email:        "alex@example.com",
			password:     "short",
			wantErr:      ErrPasswordTooShort,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeCustomerRepository{}
			service := NewService(repo, newTestTokenManager(t))

			_, err := service.Register(
				context.Background(),
				tt.customerName,
				tt.email,
				tt.password,
			)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}

			if repo.created != nil {
				t.Fatal("repository should not be called for invalid input")
			}
		})
	}
}

func TestServiceRegisterPropagatesDuplicateEmailError(t *testing.T) {
	repo := &fakeCustomerRepository{
		createErr: ErrEmailAlreadyExists,
	}
	service := NewService(repo, newTestTokenManager(t))

	_, err := service.Register(
		context.Background(),
		"Alex",
		"alex@example.com",
		"Password123",
	)

	if !errors.Is(err, ErrEmailAlreadyExists) {
		t.Fatalf("expected ErrEmailAlreadyExists, got %v", err)
	}
}

func TestServiceLoginSuccess(t *testing.T) {
	password := "CorrectPassword123"

	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	repo := &fakeCustomerRepository{
		customer: &Customer{
			ID:           "customer-123",
			Name:         "Alex Smith",
			Email:        "alex@example.com",
			PasswordHash: passwordHash,
		},
	}

	service := NewService(repo, newTestTokenManager(t))

	tokenManager, err := auth.NewTokenManager(
		"staynest-test-secret-at-least-32-bytes-long",
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	token, err := service.Login(
		context.Background(),
		"alex@example.com",
		password,
	)

	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	if token == "" {
		t.Fatal("expected a non-empty access token")
	}

	customerID, err := tokenManager.VerifyAccessToken(token)
	if err != nil {
		t.Fatalf("VerifyAccessToken() error = %v", err)
	}

	if customerID != "customer-123" {
		t.Fatalf("expected customer ID customer-123, got %q", customerID)
	}
}

func TestServiceLoginRejectsWrongPassword(t *testing.T) {
	passwordHash, err := auth.HashPassword("CorrectPassword123")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	repo := &fakeCustomerRepository{
		customer: &Customer{
			ID:           "customer-123",
			Email:        "alex@example.com",
			PasswordHash: passwordHash,
		},
	}

	service := NewService(repo, newTestTokenManager(t))

	_, err = auth.NewTokenManager(
		"staynest-test-secret-at-least-32-bytes-long",
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	_, err = service.Login(
		context.Background(),
		"alex@example.com",
		"WrongPassword123",
	)

	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestServiceLoginRejectsUnknownCustomer(t *testing.T) {
	repo := &fakeCustomerRepository{
		getErr: pgx.ErrNoRows,
	}

	service := NewService(repo, newTestTokenManager(t))

	_, err := auth.NewTokenManager(
		"staynest-test-secret-at-least-32-bytes-long",
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	_, err = service.Login(
		context.Background(),
		"unknown@example.com",
		"Password123",
	)

	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestServiceLoginRejectsEmptyCredentials(t *testing.T) {
	tests := []struct {
		name     string
		email    string
		password string
	}{
		{
			name:     "empty email",
			email:    "",
			password: "Password123",
		},
		{
			name:     "empty password",
			email:    "alex@example.com",
			password: "",
		},
		{
			name:     "both empty",
			email:    "",
			password: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeCustomerRepository{}
			service := NewService(repo, newTestTokenManager(t))

			_, err := auth.NewTokenManager(
				"staynest-test-secret-at-least-32-bytes-long",
				15*time.Minute,
			)
			if err != nil {
				t.Fatalf("NewTokenManager() error = %v", err)
			}

			_, err = service.Login(
				context.Background(),
				tt.email,
				tt.password,
			)

			if !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("expected ErrInvalidCredentials, got %v", err)
			}
		})
	}
}

func TestServiceLoginPropagatesDatabaseError(t *testing.T) {
	databaseErr := errors.New("database connection failed")

	repo := &fakeCustomerRepository{
		getErr: databaseErr,
	}

	service := NewService(repo, newTestTokenManager(t))

	_, err := auth.NewTokenManager(
		"staynest-test-secret-at-least-32-bytes-long",
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	_, err = service.Login(
		context.Background(),
		"alex@example.com",
		"Password123",
	)

	if err == nil {
		t.Fatal("expected database error")
	}

	if errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("database error must not be reported as invalid credentials")
	}

	if !errors.Is(err, databaseErr) {
		t.Fatalf("expected wrapped database error, got %v", err)
	}
}

func (f *fakeCustomerRepository) GetByID(
	ctx context.Context,
	id string,
) (*Customer, error) {
	if f.getByIDErr != nil {
		return nil, f.getByIDErr
	}

	return f.customerByID, nil
}

func TestServiceGetByIDSuccess(t *testing.T) {
	repo := &fakeCustomerRepository{
		customerByID: &Customer{
			ID:    "customer-123",
			Name:  "Alex Smith",
			Email: "alex@example.com",
		},
	}

	service := NewService(repo, newTestTokenManager(t))

	result, err := service.GetByID(context.Background(), "customer-123")
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}

	if result.ID != "customer-123" {
		t.Fatalf("expected customer-123, got %q", result.ID)
	}
}

func TestServiceGetByIDNotFound(t *testing.T) {
	repo := &fakeCustomerRepository{
		getByIDErr: pgx.ErrNoRows,
	}

	service := NewService(repo, newTestTokenManager(t))

	_, err := service.GetByID(context.Background(), "missing-customer")

	if !errors.Is(err, ErrCustomerNotFound) {
		t.Fatalf("expected ErrCustomerNotFound, got %v", err)
	}
}

func TestServiceGetByIDPropagatesDatabaseError(t *testing.T) {
	databaseErr := errors.New("database connection failed")

	repo := &fakeCustomerRepository{
		getByIDErr: databaseErr,
	}

	service := NewService(repo, newTestTokenManager(t))

	_, err := service.GetByID(context.Background(), "customer-123")

	if !errors.Is(err, databaseErr) {
		t.Fatalf("expected wrapped database error, got %v", err)
	}
}

func TestServiceUpdateNameSuccess(t *testing.T) {
	repo := &fakeCustomerRepository{
		customerByID: &Customer{
			ID:    "customer-123",
			Name:  "Alex Smith",
			Email: "alex@example.com",
		},
	}

	service := NewService(repo, newTestTokenManager(t))

	updated, err := service.UpdateName(
		context.Background(),
		"customer-123",
		"  Alex Johnson  ",
	)
	if err != nil {
		t.Fatalf("UpdateName() error = %v", err)
	}

	if updated.Name != "Alex Johnson" {
		t.Errorf("expected trimmed name, got %q", updated.Name)
	}

	if repo.updated == nil {
		t.Fatal("expected repository UpdateName() to be called")
	}
}

func TestServiceUpdateNameRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{
			name:    "empty name",
			input:   "   ",
			wantErr: ErrInvalidName,
		},
		{
			name:    "name exceeds 120 characters",
			input:   strings.Repeat("a", 121),
			wantErr: ErrInvalidNameLength,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeCustomerRepository{
				customerByID: &Customer{
					ID: "customer-123",
				},
			}

			service := NewService(repo, newTestTokenManager(t))

			_, err := service.UpdateName(
				context.Background(),
				"customer-123",
				tt.input,
			)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}

			if repo.updated != nil {
				t.Fatal("repository should not be called for invalid input")
			}
		})
	}
}

func TestServiceUpdateNameNotFound(t *testing.T) {
	repo := &fakeCustomerRepository{}

	service := NewService(repo, newTestTokenManager(t))

	_, err := service.UpdateName(
		context.Background(),
		"missing-customer",
		"Alex Johnson",
	)

	if !errors.Is(err, ErrCustomerNotFound) {
		t.Fatalf("expected ErrCustomerNotFound, got %v", err)
	}
}

func TestServiceUpdateNamePropagatesDatabaseError(t *testing.T) {
	databaseErr := errors.New("database unavailable")

	repo := &fakeCustomerRepository{
		updateErr: databaseErr,
	}

	service := NewService(repo, newTestTokenManager(t))

	_, err := service.UpdateName(
		context.Background(),
		"customer-123",
		"Alex Johnson",
	)

	if !errors.Is(err, databaseErr) {
		t.Fatalf("expected database error to be wrapped, got %v", err)
	}
}

func TestServiceChangePasswordSuccess(t *testing.T) {
	oldHash, err := auth.HashPassword("OldPassword123")
	if err != nil {
		t.Fatal(err)
	}

	repo := &fakeCustomerRepository{
		customerByID: &Customer{
			ID:           "customer-123",
			PasswordHash: oldHash,
		},
	}

	service := NewService(repo, newTestTokenManager(t))

	err = service.ChangePassword(
		context.Background(),
		"customer-123",
		"OldPassword123",
		"NewPassword456",
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if repo.updatedPasswordHash == "" {
		t.Fatal("expected new password hash to be saved")
	}

	if repo.updatedPasswordHash == oldHash {
		t.Fatal("expected a newly generated password hash")
	}

	matches, err := auth.VerifyPassword(
		"NewPassword456",
		repo.updatedPasswordHash,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !matches {
		t.Fatal("expected new password to match saved hash")
	}

	matches, err = auth.VerifyPassword(
		"OldPassword123",
		repo.updatedPasswordHash,
	)
	if err != nil {
		t.Fatal(err)
	}
	if matches {
		t.Fatal("old password should no longer match")
	}
}

func TestServiceChangePasswordRejectsWrongCurrentPassword(t *testing.T) {
	oldHash, err := auth.HashPassword("OldPassword123")
	if err != nil {
		t.Fatal(err)
	}

	repo := &fakeCustomerRepository{
		customerByID: &Customer{
			ID:           "customer-123",
			PasswordHash: oldHash,
		},
	}

	service := NewService(repo, newTestTokenManager(t))

	err = service.ChangePassword(
		context.Background(),
		"customer-123",
		"WrongPassword123",
		"NewPassword456",
	)

	if !errors.Is(err, ErrInvalidCurrentPassword) {
		t.Fatalf("expected ErrInvalidCurrentPassword, got %v", err)
	}

	if repo.updatedPasswordHash != "" {
		t.Fatal("password should not be updated")
	}
}

func TestServiceChangePasswordRejectsShortNewPassword(t *testing.T) {
	repo := &fakeCustomerRepository{
		customerByID: &Customer{
			ID: "customer-123",
		},
	}

	service := NewService(repo, newTestTokenManager(t))

	err := service.ChangePassword(
		context.Background(),
		"customer-123",
		"OldPassword123",
		"short",
	)

	if !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("expected ErrPasswordTooShort, got %v", err)
	}

	if repo.updatedPasswordHash != "" {
		t.Fatal("password should not be updated")
	}
}

func TestServiceChangePasswordCustomerNotFound(t *testing.T) {
	repo := &fakeCustomerRepository{
		getByIDErr: pgx.ErrNoRows,
	}

	service := NewService(repo, newTestTokenManager(t))

	err := service.ChangePassword(
		context.Background(),
		"customer-123",
		"OldPassword123",
		"NewPassword456",
	)

	if !errors.Is(err, ErrCustomerNotFound) {
		t.Fatalf("expected ErrCustomerNotFound, got %v", err)
	}
}

func TestServiceChangePasswordPropagatesDatabaseError(t *testing.T) {
	databaseErr := errors.New("database unavailable")

	repo := &fakeCustomerRepository{
		getByIDErr: databaseErr,
	}

	service := NewService(repo, newTestTokenManager(t))

	err := service.ChangePassword(
		context.Background(),
		"customer-123",
		"OldPassword123",
		"NewPassword456",
	)

	if !errors.Is(err, databaseErr) {
		t.Fatalf("expected database error, got %v", err)
	}
}

func TestServiceDeactivateSuccess(t *testing.T) {
	passwordHash, err := auth.HashPassword("CurrentPassword123")
	if err != nil {
		t.Fatal(err)
	}

	repo := &fakeCustomerRepository{
		customerByID: &Customer{
			ID:           "customer-123",
			PasswordHash: passwordHash,
		},
	}

	service := NewService(repo, newTestTokenManager(t))

	err = service.Deactivate(
		context.Background(),
		"customer-123",
		"CurrentPassword123",
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if repo.deactivatedID != "customer-123" {
		t.Fatalf("expected customer-123 to be deactivated, got %q", repo.deactivatedID)
	}
}

func TestServiceDeactivateRejectsWrongCurrentPassword(t *testing.T) {
	passwordHash, err := auth.HashPassword("CurrentPassword123")
	if err != nil {
		t.Fatal(err)
	}

	repo := &fakeCustomerRepository{
		customerByID: &Customer{
			ID:           "customer-123",
			PasswordHash: passwordHash,
		},
	}

	service := NewService(repo, newTestTokenManager(t))

	err = service.Deactivate(
		context.Background(),
		"customer-123",
		"WrongPassword123",
	)

	if !errors.Is(err, ErrInvalidCurrentPassword) {
		t.Fatalf("expected ErrInvalidCurrentPassword, got %v", err)
	}

	if repo.deactivatedID != "" {
		t.Fatal("customer should not be deactivated with an incorrect password")
	}
}

func TestServiceDeactivateRejectsEmptyCurrentPassword(t *testing.T) {
	repo := &fakeCustomerRepository{
		customerByID: &Customer{
			ID: "customer-123",
		},
	}

	service := NewService(repo, newTestTokenManager(t))

	err := service.Deactivate(
		context.Background(),
		"customer-123",
		"",
	)

	if !errors.Is(err, ErrInvalidCurrentPassword) {
		t.Fatalf("expected ErrInvalidCurrentPassword, got %v", err)
	}

	if repo.deactivatedID != "" {
		t.Fatal("customer should not be deactivated without password confirmation")
	}
}

func TestServiceDeactivateCustomerNotFound(t *testing.T) {
	repo := &fakeCustomerRepository{
		getByIDErr: pgx.ErrNoRows,
	}

	service := NewService(repo, newTestTokenManager(t))

	err := service.Deactivate(
		context.Background(),
		"customer-123",
		"CurrentPassword123",
	)

	if !errors.Is(err, ErrCustomerNotFound) {
		t.Fatalf("expected ErrCustomerNotFound, got %v", err)
	}
}

func TestServiceDeactivatePropagatesDatabaseError(t *testing.T) {
	databaseErr := errors.New("database unavailable")

	repo := &fakeCustomerRepository{
		getByIDErr: databaseErr,
	}

	service := NewService(repo, newTestTokenManager(t))

	err := service.Deactivate(
		context.Background(),
		"customer-123",
		"CurrentPassword123",
	)

	if !errors.Is(err, databaseErr) {
		t.Fatalf("expected database error, got %v", err)
	}
}

func TestServiceDeactivateRepositoryError(t *testing.T) {
	passwordHash, err := auth.HashPassword("CurrentPassword123")
	if err != nil {
		t.Fatal(err)
	}

	databaseErr := errors.New("database unavailable")

	repo := &fakeCustomerRepository{
		customerByID: &Customer{
			ID:           "customer-123",
			PasswordHash: passwordHash,
		},
		deactivateErr: databaseErr,
	}

	service := NewService(repo, newTestTokenManager(t))

	err = service.Deactivate(
		context.Background(),
		"customer-123",
		"CurrentPassword123",
	)

	if !errors.Is(err, databaseErr) {
		t.Fatalf("expected database error, got %v", err)
	}

	if repo.deactivatedID != "" {
		t.Fatal("customer should not be marked as deactivated after repository failure")
	}
}
