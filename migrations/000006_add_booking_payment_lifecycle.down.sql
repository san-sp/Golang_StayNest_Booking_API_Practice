BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM bookings
        WHERE status IN ('pending_payment', 'expired')
    ) THEN
        RAISE EXCEPTION
            'Cannot roll back: pending_payment or expired bookings exist';
    END IF;
END $$;

DROP INDEX IF EXISTS idx_bookings_pending_payment_expiry;

ALTER TABLE bookings
    DROP CONSTRAINT IF EXISTS chk_booking_expiration;

ALTER TABLE bookings
    DROP CONSTRAINT IF EXISTS chk_bookings_status;

ALTER TABLE bookings
    ADD CONSTRAINT bookings_status_check
    CHECK (status IN ('confirmed', 'cancelled'));

ALTER TABLE bookings
    DROP COLUMN expired_at,
    DROP COLUMN payment_expires_at;

COMMIT;
