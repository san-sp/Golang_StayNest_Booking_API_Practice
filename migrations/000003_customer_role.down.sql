BEGIN;

ALTER TABLE customers
    DROP CONSTRAINT chk_customers_role;

ALTER TABLE customers
    DROP COLUMN role;

COMMIT;