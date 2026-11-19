BEGIN;

ALTER TABLE customers
    ADD COLUMN role VARCHAR(20) NOT NULL DEFAULT 'customer';

ALTER TABLE customers
    ADD CONSTRAINT chk_customers_role
    CHECK (role IN ('customer', 'admin'));

COMMIT;