
BEGIN;

-- Allow bookings to move through the payment lifecycle.
ALTER TABLE bookings
    DROP CONSTRAINT IF EXISTS bookings_status_check;

ALTER TABLE bookings
    ADD CONSTRAINT chk_bookings_status
    CHECK (
        status IN (
            'pending_payment',
            'confirmed',
            'cancelled',
            'expired'
        )
    );

-- Payment deadline for newly created bookings.
ALTER TABLE bookings
    ADD COLUMN payment_expires_at TIMESTAMPTZ;

-- Timestamp recording when a pending booking expires.
ALTER TABLE bookings
    ADD COLUMN expired_at TIMESTAMPTZ;

-- An expired booking must have an expiration timestamp,
-- and a non-expired booking must not have one.
ALTER TABLE bookings
    ADD CONSTRAINT chk_booking_expiration
    CHECK (
        (status = 'expired') = (expired_at IS NOT NULL)
    );

-- Helps the expiration worker find pending bookings
-- whose payment deadlines have passed.
CREATE INDEX idx_bookings_pending_payment_expiry
    ON bookings (payment_expires_at, id)
    WHERE status = 'pending_payment';

COMMIT;
