BEGIN;

ALTER TABLE payment_refunds
    ADD COLUMN processing_lease_token UUID,
    ADD COLUMN processing_lease_expires_at TIMESTAMPTZ,
    ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

CREATE INDEX idx_payment_refunds_pending_claim
    ON payment_refunds (next_attempt_at, created_at)
    WHERE status = 'pending';

CREATE INDEX idx_payment_refunds_expired_lease
    ON payment_refunds (processing_lease_expires_at)
    WHERE status = 'processing';

COMMIT;
