BEGIN;

ALTER TABLE payments
    ADD COLUMN provider_order_lease_token UUID,
    ADD COLUMN provider_order_lease_expires_at TIMESTAMPTZ,

    ADD CONSTRAINT chk_payment_order_lease_pair
    CHECK (
        (provider_order_lease_token IS NULL) =
        (provider_order_lease_expires_at IS NULL)
    ),

    ADD CONSTRAINT chk_payment_order_lease_without_order
    CHECK (
        provider_order_id IS NULL OR (
            provider_order_lease_token IS NULL
            AND provider_order_lease_expires_at IS NULL
        )
    );

CREATE INDEX idx_payments_order_lease_expiry
    ON payments (provider_order_lease_expires_at)
    WHERE provider_order_lease_token IS NOT NULL;

COMMIT;
