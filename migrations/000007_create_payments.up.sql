BEGIN;

-- One payment record per booking.
CREATE TABLE payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    booking_id UUID NOT NULL UNIQUE
        REFERENCES bookings(id) ON DELETE RESTRICT,

    customer_id UUID NOT NULL
        REFERENCES customers(id) ON DELETE RESTRICT,

    amount_paise BIGINT NOT NULL
        CHECK (amount_paise > 0),

    currency VARCHAR(3) NOT NULL DEFAULT 'INR'
        CHECK (currency = 'INR'),

    provider VARCHAR(30) NOT NULL DEFAULT 'razorpay'
        CHECK (provider = 'razorpay'),

    provider_order_id VARCHAR(100),

    idempotency_key VARCHAR(128) NOT NULL,

    status VARCHAR(20) NOT NULL DEFAULT 'created'
        CHECK (
            status IN (
                'created',
                'authorized',
                'captured',
                'failed'
            )
        ),

    fulfillment_status VARCHAR(20) NOT NULL DEFAULT 'pending'
        CHECK (
            fulfillment_status IN (
                'pending',
                'fulfilled',
                'refund_required',
                'review_required'
            )
        ),

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_payment_customer_idempotency
        UNIQUE (customer_id, idempotency_key)
);

-- Provider order IDs must be unique when present.
CREATE UNIQUE INDEX uq_payments_provider_order
    ON payments (provider, provider_order_id)
    WHERE provider_order_id IS NOT NULL;

CREATE INDEX idx_payments_customer_created
    ON payments (customer_id, created_at DESC);

CREATE INDEX idx_payments_status
    ON payments (status);


-- Records individual payment attempts made against a payment.
CREATE TABLE payment_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    payment_id UUID NOT NULL
        REFERENCES payments(id) ON DELETE RESTRICT,

    provider VARCHAR(30) NOT NULL DEFAULT 'razorpay'
        CHECK (provider = 'razorpay'),

    provider_payment_id VARCHAR(100),

    amount_paise BIGINT NOT NULL
        CHECK (amount_paise > 0),

    currency VARCHAR(3) NOT NULL DEFAULT 'INR'
        CHECK (currency = 'INR'),

    status VARCHAR(20) NOT NULL
        CHECK (
            status IN (
                'created',
                'authorized',
                'captured',
                'failed'
            )
        ),

    failure_code VARCHAR(100),
    failure_description TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX uq_payment_attempts_provider_payment
    ON payment_attempts (provider, provider_payment_id)
    WHERE provider_payment_id IS NOT NULL;

CREATE INDEX idx_payment_attempts_payment_created
    ON payment_attempts (payment_id, created_at DESC);


-- Stores webhook events to support idempotent processing.
CREATE TABLE payment_webhook_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    payment_id UUID
        REFERENCES payments(id) ON DELETE RESTRICT,

    provider VARCHAR(30) NOT NULL DEFAULT 'razorpay'
        CHECK (provider = 'razorpay'),

    provider_event_id VARCHAR(255) NOT NULL,
    event_type VARCHAR(100) NOT NULL,

    processing_status VARCHAR(20) NOT NULL DEFAULT 'received'
        CHECK (
            processing_status IN (
                'received',
                'processing',
                'processed',
                'failed'
            )
        ),

    payload JSONB NOT NULL,

    processed_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_payment_webhook_provider_event
        UNIQUE (provider, provider_event_id)
);

CREATE INDEX idx_payment_webhook_events_payment
    ON payment_webhook_events (payment_id);


-- Tracks full refunds for payments that cannot be fulfilled.
CREATE TABLE payment_refunds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    payment_id UUID NOT NULL UNIQUE
        REFERENCES payments(id) ON DELETE RESTRICT,

    provider VARCHAR(30) NOT NULL DEFAULT 'razorpay'
        CHECK (provider = 'razorpay'),

    provider_payment_id VARCHAR(100) NOT NULL,
    provider_refund_id VARCHAR(100),

    idempotency_key VARCHAR(128) NOT NULL UNIQUE,

    amount_paise BIGINT NOT NULL
        CHECK (amount_paise > 0),

    currency VARCHAR(3) NOT NULL DEFAULT 'INR'
        CHECK (currency = 'INR'),

    status VARCHAR(20) NOT NULL DEFAULT 'pending'
        CHECK (
            status IN (
                'pending',
                'processing',
                'succeeded',
                'failed',
                'review_required'
            )
        ),

    attempt_count INT NOT NULL DEFAULT 0
        CHECK (attempt_count >= 0),

    last_error TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX uq_payment_refunds_provider_refund
    ON payment_refunds (provider, provider_refund_id)
    WHERE provider_refund_id IS NOT NULL;

CREATE INDEX idx_payment_refunds_status
    ON payment_refunds (status, created_at);

COMMIT;
