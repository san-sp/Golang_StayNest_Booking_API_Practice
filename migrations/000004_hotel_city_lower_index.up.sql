BEGIN;

CREATE INDEX idx_hotels_city_lower
    ON hotels (LOWER(city));

COMMIT;