CREATE TABLE customers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(120) NOT NULL,
    email VARCHAR(255) NOT NULL,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX uq_customers_email_lower
    ON customers (LOWER(email));

CREATE TABLE hotels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(150) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    address TEXT NOT NULL,
    city VARCHAR(100) NOT NULL,
    country VARCHAR(100) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_hotels_city
    ON hotels (city);

CREATE TABLE room_types (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    hotel_id UUID NOT NULL REFERENCES hotels(id) ON DELETE RESTRICT,
    name VARCHAR(100) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    max_occupancy INT NOT NULL CHECK (max_occupancy > 0),
    price_per_night_paise BIGINT NOT NULL CHECK (price_per_night_paise >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_room_types_hotel_name UNIQUE (hotel_id, name)
);

CREATE TABLE room_inventory (
    room_type_id UUID NOT NULL REFERENCES room_types(id) ON DELETE RESTRICT,
    stay_date DATE NOT NULL,
    total_rooms INT NOT NULL CHECK (total_rooms >= 0),
    available_rooms INT NOT NULL CHECK (
        available_rooms >= 0
        AND available_rooms <= total_rooms
    ),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (room_type_id, stay_date)
);

CREATE TABLE bookings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_reference VARCHAR(30) NOT NULL UNIQUE,
    customer_id UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    room_type_id UUID NOT NULL REFERENCES room_types(id) ON DELETE RESTRICT,

    check_in DATE NOT NULL,
    check_out DATE NOT NULL,

    rooms_count INT NOT NULL CHECK (rooms_count > 0),
    guests_count INT NOT NULL CHECK (guests_count > 0),

    price_per_night_paise BIGINT NOT NULL CHECK (price_per_night_paise >= 0),
    total_amount_paise BIGINT NOT NULL CHECK (total_amount_paise >= 0),

    status VARCHAR(20) NOT NULL DEFAULT 'confirmed'
        CHECK (status IN ('confirmed', 'cancelled')),

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    cancelled_at TIMESTAMPTZ,

    CONSTRAINT chk_booking_dates CHECK (check_out > check_in),
    CONSTRAINT chk_booking_cancellation CHECK (
        (status = 'cancelled') = (cancelled_at IS NOT NULL)
    )
);

CREATE INDEX idx_bookings_customer_id
    ON bookings (customer_id);

CREATE INDEX idx_bookings_room_type_dates
    ON bookings (room_type_id, check_in, check_out);