package customer

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"

	"staynest/internal/auth"

	"github.com/jackc/pgx/v5"
)

var (
	ErrInvalidName            = errors.New("name is required")
	ErrInvalidEmail           = errors.New("valid email is required")
	ErrPasswordTooShort       = errors.New("password must be at least 8 characters")
	ErrInvalidCredentials     = errors.New("invalid email or password")
	ErrCustomerNotFound       = errors.New("customer not found")
	ErrInvalidNameLength      = errors.New("name must not exceed 120 characters")
	ErrInvalidCurrentPassword = errors.New("current password is incorrect")
)

type CustomerRepository interface {
	Create(ctx context.Context, customer Customer) (*Customer, error)
	GetByEmail(ctx context.Context, email string) (*Customer, error)
	GetByID(ctx context.Context, id string) (*Customer, error)
	UpdateName(ctx context.Context, id string, name string) (*Customer, error)
	UpdatePasswordHash(ctx context.Context, id string, passwordHash string) error
	Deactivate(ctx context.Context, id string) error
}

type Service struct {
	repo         CustomerRepository
	tokenManager *auth.TokenManager
}

func NewService(
	repo CustomerRepository,
	tokenManager *auth.TokenManager,
) *Service {
	return &Service{
		repo:         repo,
		tokenManager: tokenManager,
	}
}

// Register validates customer input, hashes the password,
// and creates the customer through the repository.
func (s *Service) Register(
	ctx context.Context,
	name string,
	email string,
	password string,
) (*Customer, error) {
	// Normalize user input.
	name = strings.TrimSpace(name)
	email = strings.ToLower(strings.TrimSpace(email))

	// Validate name.
	if name == "" {
		return nil, ErrInvalidName
	}

	// Validate email.
	parsedEmail, err := mail.ParseAddress(email)
	if err != nil || parsedEmail.Address != email {
		return nil, ErrInvalidEmail
	}

	// Validate password.
	if utf8.RuneCountInString(password) < 8 {
		return nil, ErrPasswordTooShort
	}

	// Hash the password before storing it.
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}

	customer := Customer{
		Name:         name,
		Email:        email,
		PasswordHash: passwordHash,
	}

	// Save the customer in PostgreSQL.
	created, err := s.repo.Create(ctx, customer)
	if err != nil {
		return nil, err
	}

	return created, nil
}

func (s *Service) Login(
	ctx context.Context,
	email string,
	password string,
) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	if email == "" || password == "" {
		return "", ErrInvalidCredentials
	}

	customer, err := s.repo.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrInvalidCredentials
		}

		return "", fmt.Errorf("get customer for login: %w", err)
	}

	match, err := auth.VerifyPassword(password, customer.PasswordHash)
	if err != nil {
		return "", fmt.Errorf("verify customer password: %w", err)
	}

	if !match {
		return "", ErrInvalidCredentials
	}

	accessToken, err := s.tokenManager.GenerateAccessToken(customer.ID)
	if err != nil {
		return "", fmt.Errorf("generate access token: %w", err)
	}

	return accessToken, nil
}

// GetByID retrieves a customer by ID.
func (s *Service) GetByID(
	ctx context.Context,
	id string,
) (*Customer, error) {
	customer, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}

		return nil, fmt.Errorf("get customer by ID: %w", err)
	}

	return customer, nil
}

// UpdateName validates and updates the authenticated customer's name.
func (s *Service) UpdateName(
	ctx context.Context,
	id string,
	name string,
) (*Customer, error) {
	name = strings.TrimSpace(name)

	if name == "" {
		return nil, ErrInvalidName
	}

	if utf8.RuneCountInString(name) > 120 {
		return nil, ErrInvalidNameLength
	}

	updated, err := s.repo.UpdateName(ctx, id, name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}

		return nil, fmt.Errorf("update customer name: %w", err)
	}

	return updated, nil
}

func (s *Service) ChangePassword(
	ctx context.Context,
	id string,
	currentPassword string,
	newPassword string,
) error {
	if utf8.RuneCountInString(newPassword) < 8 {
		return ErrPasswordTooShort
	}

	if currentPassword == "" {
		return ErrInvalidCurrentPassword
	}

	existingCustomer, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCustomerNotFound
		}

		return fmt.Errorf("get customer for password change: %w", err)
	}

	passwordMatches, err := auth.VerifyPassword(
		currentPassword,
		existingCustomer.PasswordHash,
	)
	if err != nil {
		return fmt.Errorf("verify current customer password: %w", err)
	}

	if !passwordMatches {
		return ErrInvalidCurrentPassword
	}

	newPasswordHash, err := auth.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash new customer password: %w", err)
	}

	if err := s.repo.UpdatePasswordHash(ctx, id, newPasswordHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCustomerNotFound
		}

		return fmt.Errorf("save new customer password: %w", err)
	}

	return nil
}

func (s *Service) Deactivate(
	ctx context.Context,
	id string,
	currentPassword string,
) error {
	if currentPassword == "" {
		return ErrInvalidCurrentPassword
	}

	// 1. Retrieve the active customer.
	existingCustomer, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCustomerNotFound
		}

		return fmt.Errorf("get customer for account deactivation: %w", err)
	}

	// 2. Verify the customer's current password.
	passwordMatches, err := auth.VerifyPassword(
		currentPassword,
		existingCustomer.PasswordHash,
	)
	if err != nil {
		return fmt.Errorf("verify current customer password: %w", err)
	}

	if !passwordMatches {
		return ErrInvalidCurrentPassword
	}

	// 3. Soft-delete the customer account.
	if err := s.repo.Deactivate(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCustomerNotFound
		}

		return fmt.Errorf("deactivate customer account: %w", err)
	}

	return nil
}
