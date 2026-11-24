BEGIN;

DROP INDEX IF EXISTS idx_payment_refunds_expired_lease;
DROP INDEX IF EXISTS idx_payment_refunds_pending_claim;

ALTER TABLE payment_refunds
    DROP COLUMN IF EXISTS next_attempt_at,
    DROP COLUMN IF EXISTS processing_lease_expires_at,
    DROP COLUMN IF EXISTS processing_lease_token;

COMMIT;
