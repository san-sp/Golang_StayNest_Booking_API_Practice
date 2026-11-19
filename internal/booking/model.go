package booking

import "time"

const (
	StatusPendingPayment = "pending_payment"
	StatusConfirmed      = "confirmed"
	StatusCancelled      = "cancelled"
	StatusExpired        = "expired"
)

type Booking struct {
	ID                 string
	BookingReference   string
	CustomerID         string
	RoomTypeID         string
	CheckIn            time.Time
	CheckOut           time.Time
	RoomsCount         int
	GuestsCount        int
	PricePerNightPaise int64
	TotalAmountPaise   int64
	Status             string

	PaymentExpiresAt *time.Time
	ExpiredAt        *time.Time

	CreatedAt   time.Time
	UpdatedAt   time.Time
	CancelledAt *time.Time
}

// CreateInput contains only values supplied by the customer.
// Price and booking status are controlled by the backend.
type CreateInput struct {
	RoomTypeID  string
	CheckIn     time.Time
	CheckOut    time.Time
	RoomsCount  int
	GuestsCount int
}

// ModifyInput contains the customer-editable booking details.
// Room type, price, status and booking reference remain backend-controlled.
type ModifyInput struct {
	CheckIn     time.Time
	CheckOut    time.Time
	RoomsCount  int
	GuestsCount int
}

// ListFilter contains customer booking search and pagination options.
type ListFilter struct {
	Status       string
	CheckInFrom  *time.Time
	CheckInTo    *time.Time
	CheckOutFrom *time.Time
	CheckOutTo   *time.Time
	Limit        int
	Offset       int
}

// ListResult contains matching bookings and pagination metadata.
type ListResult struct {
	Bookings []Booking
	Total    int
	Limit    int
	Offset   int
}
