BEGIN;

-- Add a timestamp indicating when an account was deactivated.
ALTER TABLE customers
    ADD COLUMN deleted_at TIMESTAMPTZ;

-- Replace the existing unique email index.
DROP INDEX uq_customers_email_lower;

-- Email addresses must be unique among active customers.
CREATE UNIQUE INDEX uq_customers_email_lower
    ON customers (LOWER(email))
    WHERE deleted_at IS NULL;

COMMIT;