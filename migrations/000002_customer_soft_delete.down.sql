BEGIN;

DROP INDEX uq_customers_email_lower;

CREATE UNIQUE INDEX uq_customers_email_lower
    ON customers (LOWER(email));

ALTER TABLE customers
    DROP COLUMN deleted_at;

COMMIT;