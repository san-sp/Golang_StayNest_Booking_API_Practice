package customer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"staynest/internal/database"
)

func setupCustomerRepository(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()

	databaseURL := os.Getenv("STAYNEST_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("STAYNEST_TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := database.NewPool(ctx, databaseURL)
	if err != nil {
		t.Fatalf("failed to connect to database: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
	})

	return NewRepository(pool), pool
}

func TestRepositoryCreateAndGetCustomer(t *testing.T) {
	repo, pool := setupCustomerRepository(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	email := fmt.Sprintf(
		"staynest-test-%d@example.com",
		time.Now().UnixNano(),
	)

	customer := Customer{
		Name:         "Test Customer",
		Email:        email,
		PasswordHash: "test-password-hash",
	}

	created, err := repo.Create(ctx, customer)
	if err != nil {
		t.Fatalf("Create() returned unexpected error: %v", err)
	}

	if created.ID == "" {
		t.Fatal("expected customer ID to be generated")
	}

	if created.Email != email {
		t.Errorf("expected email %q, got %q", email, created.Email)
	}

	t.Cleanup(func() {
		_, err := pool.Exec(
			context.Background(),
			"DELETE FROM customers WHERE id = $1",
			created.ID,
		)
		if err != nil {
			t.Errorf("failed to clean up test customer: %v", err)
		}
	})

	// Retrieve by email using different letter casing.
	foundByEmail, err := repo.GetByEmail(ctx, strings.ToUpper(email))
	if err != nil {
		t.Fatalf("GetByEmail() returned unexpected error: %v", err)
	}

	if foundByEmail.ID != created.ID {
		t.Errorf(
			"expected customer ID %q, got %q",
			created.ID,
			foundByEmail.ID,
		)
	}

	// Retrieve by ID.
	foundByID, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() returned unexpected error: %v", err)
	}

	if foundByID.Email != email {
		t.Errorf(
			"expected email %q, got %q",
			email,
			foundByID.Email,
		)
	}
}

func TestRepositoryRejectsDuplicateEmail(t *testing.T) {
	repo, pool := setupCustomerRepository(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	email := fmt.Sprintf(
		"staynest-duplicate-%d@example.com",
		time.Now().UnixNano(),
	)

	first, err := repo.Create(ctx, Customer{
		Name:         "First Customer",
		Email:        email,
		PasswordHash: "test-hash",
	})
	if err != nil {
		t.Fatalf("failed to create first customer: %v", err)
	}

	t.Cleanup(func() {
		_, err := pool.Exec(
			context.Background(),
			"DELETE FROM customers WHERE id = $1",
			first.ID,
		)
		if err != nil {
			t.Errorf("failed to clean up test customer: %v", err)
		}
	})

	// Same email with different casing must be rejected.
	_, err = repo.Create(ctx, Customer{
		Name:         "Second Customer",
		Email:        strings.ToUpper(email),
		PasswordHash: "test-hash",
	})

	if !errors.Is(err, ErrEmailAlreadyExists) {
		t.Fatalf(
			"expected ErrEmailAlreadyExists, got %v",
			err,
		)
	}
}

func TestRepositoryGetByEmailNotFound(t *testing.T) {
	repo, _ := setupCustomerRepository(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := repo.GetByEmail(
		ctx,
		fmt.Sprintf("missing-%d@example.com", time.Now().UnixNano()),
	)

	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows, got %v", err)
	}
}

func TestRepositoryUpdateCustomerName(t *testing.T) {
	repo, pool := setupCustomerRepository(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	email := fmt.Sprintf(
		"staynest-update-%d@example.com",
		time.Now().UnixNano(),
	)

	created, err := repo.Create(ctx, Customer{
		Name:         "Original Name",
		Email:        email,
		PasswordHash: "test-password-hash",
	})
	if err != nil {
		t.Fatalf("Create() returned unexpected error: %v", err)
	}

	t.Cleanup(func() {
		_, err := pool.Exec(
			context.Background(),
			"DELETE FROM customers WHERE id = $1",
			created.ID,
		)
		if err != nil {
			t.Errorf("failed to clean up test customer: %v", err)
		}
	})

	// Move updated_at into the past so we can verify the update refreshes it.
	_, err = pool.Exec(
		ctx,
		`UPDATE customers
         SET updated_at = NOW() - INTERVAL '1 minute'
         WHERE id = $1`,
		created.ID,
	)
	if err != nil {
		t.Fatalf("failed to prepare customer timestamp: %v", err)
	}

	beforeUpdate, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() returned unexpected error: %v", err)
	}

	updated, err := repo.UpdateName(ctx, created.ID, "Updated Name")
	if err != nil {
		t.Fatalf("UpdateName() returned unexpected error: %v", err)
	}

	if updated.ID != created.ID {
		t.Errorf("expected ID %q, got %q", created.ID, updated.ID)
	}

	if updated.Name != "Updated Name" {
		t.Errorf("expected name %q, got %q", "Updated Name", updated.Name)
	}

	if updated.Email != email {
		t.Errorf("expected email %q, got %q", email, updated.Email)
	}

	if !updated.UpdatedAt.After(beforeUpdate.UpdatedAt) {
		t.Errorf(
			"expected UpdatedAt to advance: before=%v, after=%v",
			beforeUpdate.UpdatedAt,
			updated.UpdatedAt,
		)
	}
}

func TestRepositoryUpdateCustomerNameNotFound(t *testing.T) {
	repo, _ := setupCustomerRepository(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := repo.UpdateName(
		ctx,
		"00000000-0000-4000-8000-000000000000",
		"Updated Name",
	)

	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows, got %v", err)
	}
}

func TestRepositoryUpdateCustomerNameDatabaseError(t *testing.T) {
	repo, pool := setupCustomerRepository(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	email := fmt.Sprintf(
		"staynest-update-error-%d@example.com",
		time.Now().UnixNano(),
	)

	created, err := repo.Create(ctx, Customer{
		Name:         "Original Name",
		Email:        email,
		PasswordHash: "test-password-hash",
	})
	if err != nil {
		t.Fatalf("Create() returned unexpected error: %v", err)
	}

	t.Cleanup(func() {
		_, err := pool.Exec(
			context.Background(),
			"DELETE FROM customers WHERE id = $1",
			created.ID,
		)
		if err != nil {
			t.Errorf("failed to clean up test customer: %v", err)
		}
	})

	cancelledCtx, cancelOperation := context.WithCancel(ctx)
	cancelOperation()

	_, err = repo.UpdateName(
		cancelledCtx,
		created.ID,
		"Updated Name",
	)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRepositoryUpdatePasswordHash(t *testing.T) {
	repo, pool := setupCustomerRepository(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	email := fmt.Sprintf(
		"staynest-password-%d@example.com",
		time.Now().UnixNano(),
	)

	created, err := repo.Create(ctx, Customer{
		Name:         "Password Test",
		Email:        email,
		PasswordHash: "old-test-hash",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	t.Cleanup(func() {
		_, err := pool.Exec(
			context.Background(),
			"DELETE FROM customers WHERE id = $1",
			created.ID,
		)
		if err != nil {
			t.Errorf("cleanup failed: %v", err)
		}
	})

	newHash := "new-test-hash"

	err = repo.UpdatePasswordHash(ctx, created.ID, newHash)
	if err != nil {
		t.Fatalf("UpdatePasswordHash() error = %v", err)
	}

	updated, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}

	if updated.PasswordHash != newHash {
		t.Fatalf(
			"expected password hash %q, got %q",
			newHash,
			updated.PasswordHash,
		)
	}
}

func TestRepositoryUpdatePasswordHashNotFound(t *testing.T) {
	repo, _ := setupCustomerRepository(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := repo.UpdatePasswordHash(
		ctx,
		"00000000-0000-4000-8000-000000000000",
		"new-test-hash",
	)

	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows, got %v", err)
	}
}

func TestRepositoryUpdatePasswordHashDatabaseError(t *testing.T) {
	repo, _ := setupCustomerRepository(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cancelledCtx, cancelOperation := context.WithCancel(ctx)
	cancelOperation()

	err := repo.UpdatePasswordHash(
		cancelledCtx,
		"00000000-0000-4000-8000-000000000000",
		"new-test-hash",
	)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRepositoryDeactivateCustomer(t *testing.T) {
	repo, pool := setupCustomerRepository(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	email := fmt.Sprintf(
		"staynest-deactivate-%d@example.com",
		time.Now().UnixNano(),
	)

	created, err := repo.Create(ctx, Customer{
		Name:         "Deactivate Test",
		Email:        email,
		PasswordHash: "test-password-hash",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	t.Cleanup(func() {
		_, err := pool.Exec(
			context.Background(),
			"DELETE FROM customers WHERE id = $1",
			created.ID,
		)
		if err != nil {
			t.Errorf("cleanup failed: %v", err)
		}
	})

	err = repo.Deactivate(ctx, created.ID)
	if err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}

	// A deactivated customer must no longer be returned by active lookups.
	_, err = repo.GetByID(ctx, created.ID)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows from GetByID(), got %v", err)
	}

	_, err = repo.GetByEmail(ctx, email)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows from GetByEmail(), got %v", err)
	}

	// Verify that the original customer record still exists.
	var deletedAt *time.Time

	err = pool.QueryRow(
		ctx,
		"SELECT deleted_at FROM customers WHERE id = $1",
		created.ID,
	).Scan(&deletedAt)

	if err != nil {
		t.Fatalf("query deleted_at: %v", err)
	}

	if deletedAt == nil {
		t.Fatal("expected deleted_at to be set")
	}
}

func TestRepositoryDeactivateCustomerNotFound(t *testing.T) {
	repo, _ := setupCustomerRepository(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := repo.Deactivate(
		ctx,
		"00000000-0000-4000-8000-000000000000",
	)

	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows, got %v", err)
	}
}

func TestRepositoryDeactivateCustomerAlreadyDeactivated(t *testing.T) {
	repo, pool := setupCustomerRepository(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	email := fmt.Sprintf(
		"staynest-deactivated-%d@example.com",
		time.Now().UnixNano(),
	)

	created, err := repo.Create(ctx, Customer{
		Name:         "Already Deactivated",
		Email:        email,
		PasswordHash: "test-password-hash",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	t.Cleanup(func() {
		_, err := pool.Exec(
			context.Background(),
			"DELETE FROM customers WHERE id = $1",
			created.ID,
		)
		if err != nil {
			t.Errorf("cleanup failed: %v", err)
		}
	})

	if err := repo.Deactivate(ctx, created.ID); err != nil {
		t.Fatalf("first Deactivate() error = %v", err)
	}

	err = repo.Deactivate(ctx, created.ID)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows on second deactivation, got %v", err)
	}
}

func TestRepositoryDeactivateCustomerDatabaseError(t *testing.T) {
	repo, _ := setupCustomerRepository(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cancelledCtx, cancelOperation := context.WithCancel(ctx)
	cancelOperation()

	err := repo.Deactivate(
		cancelledCtx,
		"00000000-0000-4000-8000-000000000000",
	)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRepositoryIsActive(t *testing.T) {
	repo, pool := setupCustomerRepository(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	email := fmt.Sprintf(
		"staynest-active-%d@example.com",
		time.Now().UnixNano(),
	)

	created, err := repo.Create(ctx, Customer{
		Name:         "Active Test Customer",
		Email:        email,
		PasswordHash: "test-password-hash",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	t.Cleanup(func() {
		_, err := pool.Exec(
			context.Background(),
			"DELETE FROM customers WHERE id = $1",
			created.ID,
		)
		if err != nil {
			t.Errorf("cleanup customer: %v", err)
		}
	})

	active, err := repo.IsActive(ctx, created.ID)
	if err != nil {
		t.Fatalf("IsActive() error = %v", err)
	}

	if !active {
		t.Fatal("expected customer to be active")
	}
}

func TestRepositoryIsActiveReturnsFalseForDeactivatedCustomer(t *testing.T) {
	repo, pool := setupCustomerRepository(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	email := fmt.Sprintf(
		"staynest-inactive-%d@example.com",
		time.Now().UnixNano(),
	)

	created, err := repo.Create(ctx, Customer{
		Name:         "Inactive Test Customer",
		Email:        email,
		PasswordHash: "test-password-hash",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	t.Cleanup(func() {
		_, err := pool.Exec(
			context.Background(),
			"DELETE FROM customers WHERE id = $1",
			created.ID,
		)
		if err != nil {
			t.Errorf("cleanup customer: %v", err)
		}
	})

	if err := repo.Deactivate(ctx, created.ID); err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}

	active, err := repo.IsActive(ctx, created.ID)
	if err != nil {
		t.Fatalf("IsActive() error = %v", err)
	}

	if active {
		t.Fatal("expected deactivated customer to be inactive")
	}
}

func TestRepositoryIsActiveDatabaseError(t *testing.T) {
	repo, _ := setupCustomerRepository(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := repo.IsActive(ctx, "customer-123")
	if err == nil {
		t.Fatal("expected database error, got nil")
	}

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func createRoleTestCustomer(
	t *testing.T,
	repo *Repository,
	pool *pgxpool.Pool,
) *Customer {
	t.Helper()

	email := fmt.Sprintf(
		"staynest-role-%d@example.com",
		time.Now().UnixNano(),
	)

	created, err := repo.Create(context.Background(), Customer{
		Name:         "Role Test Customer",
		Email:        email,
		PasswordHash: "test-password-hash",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	t.Cleanup(func() {
		_, err := pool.Exec(
			context.Background(),
			"DELETE FROM customers WHERE id = $1",
			created.ID,
		)
		if err != nil {
			t.Errorf("cleanup customer: %v", err)
		}
	})

	return created
}

func TestRepositoryIsAdmin(t *testing.T) {
	repo, pool := setupCustomerRepository(t)
	ctx := context.Background()

	created := createRoleTestCustomer(t, repo, pool)

	_, err := pool.Exec(
		ctx,
		"UPDATE customers SET role = $1 WHERE id = $2",
		RoleAdmin,
		created.ID,
	)
	if err != nil {
		t.Fatalf("promote test customer: %v", err)
	}

	isAdmin, err := repo.IsAdmin(ctx, created.ID)
	if err != nil {
		t.Fatalf("IsAdmin() error = %v", err)
	}

	if !isAdmin {
		t.Fatal("expected active administrator")
	}
}

func TestRepositoryIsAdminReturnsFalseForCustomer(t *testing.T) {
	repo, pool := setupCustomerRepository(t)
	created := createRoleTestCustomer(t, repo, pool)

	isAdmin, err := repo.IsAdmin(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("IsAdmin() error = %v", err)
	}

	if isAdmin {
		t.Fatal("expected ordinary customer not to be an admin")
	}
}

func TestRepositoryIsAdminReturnsFalseForDeactivatedAdmin(t *testing.T) {
	repo, pool := setupCustomerRepository(t)
	ctx := context.Background()

	created := createRoleTestCustomer(t, repo, pool)

	_, err := pool.Exec(
		ctx,
		"UPDATE customers SET role = $1 WHERE id = $2",
		RoleAdmin,
		created.ID,
	)
	if err != nil {
		t.Fatalf("promote test customer: %v", err)
	}

	if err := repo.Deactivate(ctx, created.ID); err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}

	isAdmin, err := repo.IsAdmin(ctx, created.ID)
	if err != nil {
		t.Fatalf("IsAdmin() error = %v", err)
	}

	if isAdmin {
		t.Fatal("expected deactivated administrator not to have admin access")
	}
}

func TestRepositoryIsAdminDatabaseError(t *testing.T) {
	repo, _ := setupCustomerRepository(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := repo.IsAdmin(ctx, "customer-123")
	if err == nil {
		t.Fatal("expected database error, got nil")
	}

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
