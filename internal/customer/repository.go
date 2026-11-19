package customer

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrEmailAlreadyExists = errors.New("customer email already exists")

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{
		pool: pool,
	}
}

func (r *Repository) Create(
	ctx context.Context,
	customer Customer,
) (*Customer, error) {
	query := `
		INSERT INTO customers (name, email, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id, name, email, password_hash, created_at, updated_at
	`

	created := &Customer{}

	err := r.pool.QueryRow(
		ctx,
		query,
		customer.Name,
		customer.Email,
		customer.PasswordHash,
	).Scan(
		&created.ID,
		&created.Name,
		&created.Email,
		&created.PasswordHash,
		&created.CreatedAt,
		&created.UpdatedAt,
	)

	if err != nil {
		var pgErr *pgconn.PgError

		if errors.As(err, &pgErr) &&
			pgErr.Code == "23505" &&
			pgErr.ConstraintName == "uq_customers_email_lower" {
			return nil, ErrEmailAlreadyExists
		}

		return nil, fmt.Errorf("create customer: %w", err)
	}

	return created, nil
}

func (r *Repository) GetByEmail(
	ctx context.Context,
	email string,
) (*Customer, error) {
	query := `
    SELECT id, name, email, password_hash, created_at, updated_at
    FROM customers
    WHERE LOWER(email) = LOWER($1)
      AND deleted_at IS NULL
	`

	customer := &Customer{}

	err := r.pool.QueryRow(ctx, query, email).Scan(
		&customer.ID,
		&customer.Name,
		&customer.Email,
		&customer.PasswordHash,
		&customer.CreatedAt,
		&customer.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
		}

		return nil, fmt.Errorf("get customer by email: %w", err)
	}

	return customer, nil
}

func (r *Repository) GetByID(
	ctx context.Context,
	id string,
) (*Customer, error) {
	query := `
    SELECT id, name, email, password_hash, created_at, updated_at
    FROM customers
    WHERE id = $1
      AND deleted_at IS NULL
	`

	customer := &Customer{}

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&customer.ID,
		&customer.Name,
		&customer.Email,
		&customer.PasswordHash,
		&customer.CreatedAt,
		&customer.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
		}

		return nil, fmt.Errorf("get customer by ID: %w", err)
	}

	return customer, nil
}

// IsActive reports whether a customer exists and has not been deactivated.
func (r *Repository) IsActive(
	ctx context.Context,
	id string,
) (bool, error) {
	query := `
		SELECT EXISTS (
			SELECT 1
			FROM customers
			WHERE id = $1
			  AND deleted_at IS NULL
		)
	`

	var active bool

	if err := r.pool.QueryRow(ctx, query, id).Scan(&active); err != nil {
		return false, fmt.Errorf("check customer active status: %w", err)
	}

	return active, nil
}

// UpdateName updates a customer's name and returns the updated customer.
func (r *Repository) UpdateName(
	ctx context.Context,
	id string,
	name string,
) (*Customer, error) {
	query := `
    UPDATE customers
    SET name = $1,
        updated_at = NOW()
    WHERE id = $2
      AND deleted_at IS NULL
    RETURNING id, name, email, password_hash, created_at, updated_at
	`

	updated := &Customer{}

	err := r.pool.QueryRow(ctx, query, name, id).Scan(
		&updated.ID,
		&updated.Name,
		&updated.Email,
		&updated.PasswordHash,
		&updated.CreatedAt,
		&updated.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
		}

		return nil, fmt.Errorf("update customer name: %w", err)
	}

	return updated, nil
}

// UpdatePasswordHash replaces a customer's stored password hash.
func (r *Repository) UpdatePasswordHash(
	ctx context.Context,
	id string,
	passwordHash string,
) error {
	query := `
    UPDATE customers
    SET password_hash = $1,
        updated_at = NOW()
    WHERE id = $2
      AND deleted_at IS NULL
	`

	result, err := r.pool.Exec(ctx, query, passwordHash, id)
	if err != nil {
		return fmt.Errorf("update customer password hash: %w", err)
	}

	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}

	return nil
}

// Deactivate marks an active customer account as deleted.
func (r *Repository) Deactivate(
	ctx context.Context,
	id string,
) error {
	query := `
        UPDATE customers
        SET deleted_at = NOW(),
            updated_at = NOW()
        WHERE id = $1
          AND deleted_at IS NULL
    `

	result, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("deactivate customer: %w", err)
	}

	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}

	return nil
}

// IsAdmin reports whether an active customer has administrator privileges.
func (r *Repository) IsAdmin(
	ctx context.Context,
	id string,
) (bool, error) {
	query := `
        SELECT EXISTS (
            SELECT 1
            FROM customers
            WHERE id = $1
              AND role = $2
              AND deleted_at IS NULL
        )
    `

	var isAdmin bool

	if err := r.pool.QueryRow(
		ctx,
		query,
		id,
		RoleAdmin,
	).Scan(&isAdmin); err != nil {
		return false, fmt.Errorf("check customer admin role: %w", err)
	}

	return isAdmin, nil
}
