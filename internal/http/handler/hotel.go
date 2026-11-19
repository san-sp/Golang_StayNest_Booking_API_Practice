package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"staynest/internal/hotel"
)

type HotelService interface {
	Create(ctx context.Context, hotel hotel.Hotel) (*hotel.Hotel, error)
	GetByID(ctx context.Context, id string) (*hotel.Hotel, error)
	Update(ctx context.Context, id string, hotel hotel.Hotel) (*hotel.Hotel, error)
	List(ctx context.Context, params hotel.ListParams) (*hotel.ListResult, error)
	Search(ctx context.Context, params hotel.SearchParams) ([]hotel.SearchResult, error)
}

type hotelRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Address     string `json:"address"`
	City        string `json:"city"`
	Country     string `json:"country"`
	Timezone    string `json:"timezone"`
}

type hotelResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Address     string `json:"address"`
	City        string `json:"city"`
	Country     string `json:"country"`
	Timezone    string `json:"timezone"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type hotelListResponse struct {
	Hotels []hotelResponse `json:"hotels"`
	Total  int             `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

type availableRoomResponse struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	MaxOccupancy       int    `json:"max_occupancy"`
	PricePerNightPaise int64  `json:"price_per_night_paise"`
	AvailableRooms     int    `json:"available_rooms"`
}

type hotelSearchResultResponse struct {
	Hotel hotelResponse           `json:"hotel"`
	Rooms []availableRoomResponse `json:"rooms"`
}

type hotelSearchResponse struct {
	Hotels []hotelSearchResultResponse `json:"hotels"`
}

func newHotelResponse(h *hotel.Hotel) hotelResponse {
	return hotelResponse{
		ID:          h.ID,
		Name:        h.Name,
		Description: h.Description,
		Address:     h.Address,
		City:        h.City,
		Country:     h.Country,
		CreatedAt:   h.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   h.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		Timezone:    h.Timezone,
	}
}

// CreateHotel handles hotel creation by an administrator.
func CreateHotel(service HotelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req hotelRequest

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid request body",
			})
			return
		}

		created, err := service.Create(
			c.Request.Context(),
			hotel.Hotel{
				Name:        req.Name,
				Description: req.Description,
				Address:     req.Address,
				City:        req.City,
				Country:     req.Country,
			},
		)
		if err != nil {
			if isHotelValidationError(err) {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": err.Error(),
				})
				return
			}

			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "internal server error",
			})
			return
		}

		c.JSON(http.StatusCreated, newHotelResponse(created))
	}
}

// UpdateHotel handles hotel updates by an administrator.
func UpdateHotel(service HotelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req hotelRequest

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid request body",
			})
			return
		}

		updated, err := service.Update(
			c.Request.Context(),
			c.Param("id"),
			hotel.Hotel{
				Name:        req.Name,
				Description: req.Description,
				Address:     req.Address,
				City:        req.City,
				Country:     req.Country,
			},
		)
		if err != nil {
			switch {
			case errors.Is(err, hotel.ErrHotelNotFound):
				c.JSON(http.StatusNotFound, gin.H{
					"error": "hotel not found",
				})

			case isHotelValidationError(err):
				c.JSON(http.StatusBadRequest, gin.H{
					"error": err.Error(),
				})

			default:
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "internal server error",
				})
			}
			return
		}

		c.JSON(http.StatusOK, newHotelResponse(updated))
	}
}

func isHotelValidationError(err error) bool {
	return errors.Is(err, hotel.ErrInvalidHotelName) ||
		errors.Is(err, hotel.ErrHotelNameTooLong) ||
		errors.Is(err, hotel.ErrInvalidHotelAddress) ||
		errors.Is(err, hotel.ErrInvalidHotelCity) ||
		errors.Is(err, hotel.ErrHotelCityTooLong) ||
		errors.Is(err, hotel.ErrInvalidHotelCountry) ||
		errors.Is(err, hotel.ErrHotelCountryTooLong) ||
		errors.Is(err, hotel.ErrInvalidHotelTimezone)
}

// GetHotel handles public retrieval of a hotel by ID.
func GetHotel(service HotelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		found, err := service.GetByID(
			c.Request.Context(),
			c.Param("id"),
		)

		if err != nil {
			switch {
			case errors.Is(err, hotel.ErrHotelNotFound):
				c.JSON(http.StatusNotFound, gin.H{
					"error": "hotel not found",
				})

			default:
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "internal server error",
				})
			}
			return
		}

		c.JSON(http.StatusOK, newHotelResponse(found))
	}
}

func ListHotels(service HotelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		params := hotel.ListParams{
			City: c.Query("city"),
		}

		if value := c.Query("limit"); value != "" {
			limit, err := strconv.Atoi(value)
			if err != nil || limit <= 0 {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "invalid limit",
				})
				return
			}

			params.Limit = limit
		}

		if value := c.Query("offset"); value != "" {
			offset, err := strconv.Atoi(value)
			if err != nil || offset < 0 {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "invalid offset",
				})
				return
			}
			params.Offset = offset
		}

		result, err := service.List(c.Request.Context(), params)
		if err != nil {
			if errors.Is(err, hotel.ErrInvalidHotelListOffset) {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "invalid offset",
				})
				return
			}

			if errors.Is(err, hotel.ErrInvalidHotelListLimit) {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "invalid limit",
				})
				return
			}

			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "failed to retrieve hotels",
			})
			return
		}

		response := hotelListResponse{
			Hotels: make([]hotelResponse, 0, len(result.Hotels)),
			Total:  result.Total,
			Limit:  result.Limit,
			Offset: result.Offset,
		}

		for _, h := range result.Hotels {
			response.Hotels = append(response.Hotels, newHotelResponse(&h))
		}

		c.JSON(http.StatusOK, response)
	}
}

// SearchHotels handles public hotel availability searches.
func SearchHotels(service HotelService) gin.HandlerFunc {
	return func(c *gin.Context) {
		checkIn, err := time.Parse("2006-01-02", c.Query("check_in"))
		if err != nil && c.Query("check_in") != "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "check_in must use YYYY-MM-DD format",
			})
			return
		}

		checkOut, err := time.Parse("2006-01-02", c.Query("check_out"))
		if err != nil && c.Query("check_out") != "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "check_out must use YYYY-MM-DD format",
			})
			return
		}

		guests := 0
		if value := c.Query("guests"); value != "" {
			guests, err = strconv.Atoi(value)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "guests must be a valid integer",
				})
				return
			}
		}

		rooms := 0
		if value := c.Query("rooms"); value != "" {
			rooms, err = strconv.Atoi(value)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "rooms must be a valid integer",
				})
				return
			}
		}

		results, err := service.Search(
			c.Request.Context(),
			hotel.SearchParams{
				City:     c.Query("city"),
				CheckIn:  checkIn,
				CheckOut: checkOut,
				Guests:   guests,
				Rooms:    rooms,
			},
		)

		if err != nil {
			switch {
			case errors.Is(err, hotel.ErrInvalidSearchCity),
				errors.Is(err, hotel.ErrInvalidSearchCheckIn),
				errors.Is(err, hotel.ErrInvalidSearchCheckOut),
				errors.Is(err, hotel.ErrInvalidSearchDateRange),
				errors.Is(err, hotel.ErrInvalidSearchGuests),
				errors.Is(err, hotel.ErrInvalidSearchRooms):

				c.JSON(http.StatusBadRequest, gin.H{
					"error": err.Error(),
				})

			default:
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "failed to search available hotels",
				})
			}
			return
		}

		response := hotelSearchResponse{
			Hotels: make([]hotelSearchResultResponse, 0, len(results)),
		}

		for _, result := range results {
			item := hotelSearchResultResponse{
				Hotel: newHotelResponse(&result.Hotel),
				Rooms: make([]availableRoomResponse, 0, len(result.Rooms)),
			}

			for _, room := range result.Rooms {
				item.Rooms = append(item.Rooms, availableRoomResponse{
					ID:                 room.ID,
					Name:               room.Name,
					Description:        room.Description,
					MaxOccupancy:       room.MaxOccupancy,
					PricePerNightPaise: room.PricePerNightPaise,
					AvailableRooms:     room.AvailableRooms,
				})
			}

			response.Hotels = append(response.Hotels, item)
		}

		c.JSON(http.StatusOK, response)
	}
}
