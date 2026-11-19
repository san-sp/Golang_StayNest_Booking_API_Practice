package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"staynest/internal/booking"
	"staynest/internal/http/middleware"
)

type BookingService interface {
	Create(ctx context.Context, customerID string, input booking.CreateInput) (*booking.Booking, error)

	Modify(
		ctx context.Context,
		customerID string,
		bookingID string,
		input booking.ModifyInput,
	) (*booking.Booking, error)

	Cancel(ctx context.Context, customerID string, bookingID string) (*booking.Booking, error)

	GetByID(ctx context.Context, customerID string, bookingID string) (*booking.Booking, error)
	ListByCustomerID(
		ctx context.Context,
		customerID string,
		filter booking.ListFilter,
	) (booking.ListResult, error)
}

type createBookingRequest struct {
	RoomTypeID  string `json:"room_type_id"`
	CheckIn     string `json:"check_in"`
	CheckOut    string `json:"check_out"`
	RoomsCount  int    `json:"rooms_count"`
	GuestsCount int    `json:"guests_count"`
}

type modifyBookingRequest struct {
	CheckIn     string `json:"check_in"`
	CheckOut    string `json:"check_out"`
	RoomsCount  int    `json:"rooms_count"`
	GuestsCount int    `json:"guests_count"`
}

type bookingResponse struct {
	ID                 string  `json:"id"`
	BookingReference   string  `json:"booking_reference"`
	RoomTypeID         string  `json:"room_type_id"`
	CheckIn            string  `json:"check_in"`
	CheckOut           string  `json:"check_out"`
	RoomsCount         int     `json:"rooms_count"`
	GuestsCount        int     `json:"guests_count"`
	PricePerNightPaise int64   `json:"price_per_night_paise"`
	TotalAmountPaise   int64   `json:"total_amount_paise"`
	Status             string  `json:"status"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
	CancelledAt        *string `json:"cancelled_at,omitempty"`
}

type bookingPaginationResponse struct {
	Total   int  `json:"total"`
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	HasMore bool `json:"has_more"`
}

type bookingListResponse struct {
	Bookings   []bookingResponse         `json:"bookings"`
	Pagination bookingPaginationResponse `json:"pagination"`
}

func newBookingResponse(b *booking.Booking) bookingResponse {
	response := bookingResponse{
		ID:                 b.ID,
		BookingReference:   b.BookingReference,
		RoomTypeID:         b.RoomTypeID,
		CheckIn:            b.CheckIn.Format("2006-01-02"),
		CheckOut:           b.CheckOut.Format("2006-01-02"),
		RoomsCount:         b.RoomsCount,
		GuestsCount:        b.GuestsCount,
		PricePerNightPaise: b.PricePerNightPaise,
		TotalAmountPaise:   b.TotalAmountPaise,
		Status:             b.Status,
		CreatedAt:          b.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:          b.UpdatedAt.UTC().Format(time.RFC3339),
	}

	if b.CancelledAt != nil {
		cancelledAt := b.CancelledAt.UTC().Format(time.RFC3339)
		response.CancelledAt = &cancelledAt
	}

	return response
}

// CreateBooking creates a booking for the authenticated customer.
func CreateBooking(service BookingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		customerID, exists := middleware.CustomerIDFromContext(c)
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "authentication required",
			})
			return
		}

		var req createBookingRequest

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid request body",
			})
			return
		}

		checkIn, err := time.Parse("2006-01-02", req.CheckIn)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "check_in must use YYYY-MM-DD format",
			})
			return
		}

		checkOut, err := time.Parse("2006-01-02", req.CheckOut)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "check_out must use YYYY-MM-DD format",
			})
			return
		}

		created, err := service.Create(
			c.Request.Context(),
			customerID,
			booking.CreateInput{
				RoomTypeID:  req.RoomTypeID,
				CheckIn:     checkIn,
				CheckOut:    checkOut,
				RoomsCount:  req.RoomsCount,
				GuestsCount: req.GuestsCount,
			},
		)

		if err != nil {
			switch {
			case errors.Is(err, booking.ErrInvalidCustomerID):
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "authentication required",
				})

			case errors.Is(err, booking.ErrInvalidRoomTypeID),
				errors.Is(err, booking.ErrInvalidCheckIn),
				errors.Is(err, booking.ErrInvalidCheckOut),
				errors.Is(err, booking.ErrInvalidDateRange),
				errors.Is(err, booking.ErrInvalidRoomsCount),
				errors.Is(err, booking.ErrInvalidGuestsCount),
				errors.Is(err, booking.ErrGuestCapacityExceeded),
				errors.Is(err, booking.ErrBookingDateInPast):
				c.JSON(http.StatusBadRequest, gin.H{
					"error": err.Error(),
				})

			case errors.Is(err, booking.ErrRoomTypeNotFound):
				c.JSON(http.StatusNotFound, gin.H{
					"error": "room type not found",
				})

			case errors.Is(err, booking.ErrInventoryNotConfigured):
				c.JSON(http.StatusConflict, gin.H{
					"error": "inventory is not configured for the requested dates",
				})

			case errors.Is(err, booking.ErrInsufficientAvailability):
				c.JSON(http.StatusConflict, gin.H{
					"error": "insufficient room availability",
				})

			case errors.Is(err, booking.ErrBookingAmountOverflow):
				c.JSON(http.StatusUnprocessableEntity, gin.H{
					"error": "booking amount exceeds the supported limit",
				})

			default:
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "internal server error",
				})
			}
			return
		}

		c.JSON(http.StatusCreated, newBookingResponse(created))
	}
}

// ModifyBooking modifies a confirmed booking owned by the authenticated customer.
func ModifyBooking(service BookingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		customerID, exists := middleware.CustomerIDFromContext(c)
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "authentication required",
			})
			return
		}

		bookingID := c.Param("id")

		var req modifyBookingRequest

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid request body",
			})
			return
		}

		checkIn, err := time.Parse("2006-01-02", req.CheckIn)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "check_in must use YYYY-MM-DD format",
			})
			return
		}

		checkOut, err := time.Parse("2006-01-02", req.CheckOut)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "check_out must use YYYY-MM-DD format",
			})
			return
		}

		modified, err := service.Modify(
			c.Request.Context(),
			customerID,
			bookingID,
			booking.ModifyInput{
				CheckIn:     checkIn,
				CheckOut:    checkOut,
				RoomsCount:  req.RoomsCount,
				GuestsCount: req.GuestsCount,
			},
		)

		if err != nil {
			switch {
			case errors.Is(err, booking.ErrInvalidCustomerID):
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "authentication required",
				})

			case errors.Is(err, booking.ErrInvalidBookingID):
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "booking ID is required",
				})

			case errors.Is(err, booking.ErrInvalidCheckIn),
				errors.Is(err, booking.ErrInvalidCheckOut),
				errors.Is(err, booking.ErrInvalidDateRange),
				errors.Is(err, booking.ErrInvalidRoomsCount),
				errors.Is(err, booking.ErrInvalidGuestsCount),
				errors.Is(err, booking.ErrGuestCapacityExceeded),
				errors.Is(err, booking.ErrBookingDateInPast):
				c.JSON(http.StatusBadRequest, gin.H{
					"error": err.Error(),
				})

			case errors.Is(err, booking.ErrBookingNotFound):
				c.JSON(http.StatusNotFound, gin.H{
					"error": "booking not found",
				})

			case errors.Is(err, booking.ErrBookingNotModifiable):
				c.JSON(http.StatusConflict, gin.H{
					"error": "this booking can no longer be modified",
				})

			case errors.Is(err, booking.ErrInventoryNotConfigured):
				c.JSON(http.StatusConflict, gin.H{
					"error": "inventory is not configured for the requested dates",
				})

			case errors.Is(err, booking.ErrInsufficientAvailability):
				c.JSON(http.StatusConflict, gin.H{
					"error": "insufficient room availability",
				})

			case errors.Is(err, booking.ErrBookingInventoryInconsistent):
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "unable to modify booking",
				})

			case errors.Is(err, booking.ErrBookingAmountOverflow):
				c.JSON(http.StatusUnprocessableEntity, gin.H{
					"error": "booking amount exceeds the supported limit",
				})

			default:
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "internal server error",
				})
			}
			return
		}

		c.JSON(http.StatusOK, newBookingResponse(modified))
	}
}

// CancelBooking cancels a booking owned by the authenticated customer.
func CancelBooking(service BookingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		customerID, exists := middleware.CustomerIDFromContext(c)
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "authentication required",
			})
			return
		}

		bookingID := c.Param("id")

		cancelled, err := service.Cancel(
			c.Request.Context(),
			customerID,
			bookingID,
		)

		if err != nil {
			switch {
			case errors.Is(err, booking.ErrInvalidCustomerID):
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "authentication required",
				})

			case errors.Is(err, booking.ErrInvalidBookingID):
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "booking ID is required",
				})

			case errors.Is(err, booking.ErrBookingNotFound):
				c.JSON(http.StatusNotFound, gin.H{
					"error": "booking not found",
				})

			case errors.Is(err, booking.ErrBookingInventoryInconsistent):
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "unable to cancel booking",
				})

			default:
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "internal server error",
				})
			}
			return
		}

		c.JSON(http.StatusOK, newBookingResponse(cancelled))
	}
}

// ListCustomerBookings returns all bookings belonging to the authenticated customer.

func ListCustomerBookings(service BookingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		customerID, ok := middleware.CustomerIDFromContext(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "authentication required",
			})
			return
		}

		filter := booking.ListFilter{
			Status: c.Query("status"),
			Limit:  10,
			Offset: 0,
		}

		if raw := c.Query("limit"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "limit must be an integer",
				})
				return
			}
			filter.Limit = value
		}

		if raw := c.Query("offset"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "offset must be an integer",
				})
				return
			}
			filter.Offset = value
		}

		dateFilters := []struct {
			name   string
			target **time.Time
		}{
			{"check_in_from", &filter.CheckInFrom},
			{"check_in_to", &filter.CheckInTo},
			{"check_out_from", &filter.CheckOutFrom},
			{"check_out_to", &filter.CheckOutTo},
		}

		for _, dateFilter := range dateFilters {
			raw := c.Query(dateFilter.name)
			if raw == "" {
				continue
			}

			parsed, err := time.Parse("2006-01-02", raw)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": dateFilter.name + " must use YYYY-MM-DD format",
				})
				return
			}

			*dateFilter.target = &parsed
		}

		result, err := service.ListByCustomerID(
			c.Request.Context(),
			customerID,
			filter,
		)
		if err != nil {
			switch {
			case errors.Is(err, booking.ErrInvalidCustomerID):
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "authentication required",
				})

			case errors.Is(err, booking.ErrInvalidBookingStatus),
				errors.Is(err, booking.ErrInvalidFilterRange),
				errors.Is(err, booking.ErrInvalidPagination):
				c.JSON(http.StatusBadRequest, gin.H{
					"error": err.Error(),
				})

			default:
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "failed to retrieve bookings",
				})
			}
			return
		}

		responseBookings := make([]bookingResponse, 0, len(result.Bookings))
		for i := range result.Bookings {
			responseBookings = append(
				responseBookings,
				newBookingResponse(&result.Bookings[i]),
			)
		}

		c.JSON(http.StatusOK, bookingListResponse{
			Bookings: responseBookings,
			Pagination: bookingPaginationResponse{
				Total:   result.Total,
				Limit:   result.Limit,
				Offset:  result.Offset,
				HasMore: result.Offset+len(result.Bookings) < result.Total,
			},
		})
	}
}

// GetCustomerBooking returns a booking belonging to the authenticated customer.
func GetCustomerBooking(service BookingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		customerID, ok := middleware.CustomerIDFromContext(c)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "authentication required",
			})
			return
		}
		bookingID := c.Param("id")

		found, err := service.GetByID(
			c.Request.Context(),
			customerID,
			bookingID,
		)
		if err != nil {
			switch {
			case errors.Is(err, booking.ErrInvalidCustomerID):
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "authentication required",
				})

			case errors.Is(err, booking.ErrInvalidBookingID):
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "invalid booking ID",
				})

			case errors.Is(err, booking.ErrBookingNotFound):
				c.JSON(http.StatusNotFound, gin.H{
					"error": "booking not found",
				})

			default:
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "failed to retrieve booking",
				})
			}
			return
		}

		c.JSON(http.StatusOK, newBookingResponse(found))
	}
}
