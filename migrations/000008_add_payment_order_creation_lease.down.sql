BEGIN;

DROP INDEX IF EXISTS idx_payments_order_lease_expiry;

ALTER TABLE payments
    DROP CONSTRAINT IF EXISTS chk_payment_order_lease_without_order,
    DROP CONSTRAINT IF EXISTS chk_payment_order_lease_pair,
    DROP COLUMN IF EXISTS provider_order_lease_expires_at,
    DROP COLUMN IF EXISTS provider_order_lease_token;

COMMIT;
