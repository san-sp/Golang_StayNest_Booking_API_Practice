package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"staynest/internal/room"
)

type RoomService interface {
	Create(ctx context.Context, input room.RoomType) (*room.RoomType, error)
	GetByID(ctx context.Context, id string) (*room.RoomType, error)
	ListByHotelID(ctx context.Context, hotelID string) ([]room.RoomType, error)
	Update(ctx context.Context, id string, input room.RoomType) (*room.RoomType, error)
}

type roomTypeRequest struct {
	Name               string `json:"name"`
	Description        string `json:"description"`
	MaxOccupancy       int    `json:"max_occupancy"`
	PricePerNightPaise int64  `json:"price_per_night_paise"`
}

type roomTypeResponse struct {
	ID                 string `json:"id"`
	HotelID            string `json:"hotel_id"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	MaxOccupancy       int    `json:"max_occupancy"`
	PricePerNightPaise int64  `json:"price_per_night_paise"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

type roomTypeListResponse struct {
	Rooms []roomTypeResponse `json:"rooms"`
}

func newRoomTypeResponse(r *room.RoomType) roomTypeResponse {
	return roomTypeResponse{
		ID:                 r.ID,
		HotelID:            r.HotelID,
		Name:               r.Name,
		Description:        r.Description,
		MaxOccupancy:       r.MaxOccupancy,
		PricePerNightPaise: r.PricePerNightPaise,
		CreatedAt:          r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:          r.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func isRoomTypeValidationError(err error) bool {
	return errors.Is(err, room.ErrInvalidHotelID) ||
		errors.Is(err, room.ErrInvalidRoomName) ||
		errors.Is(err, room.ErrRoomNameTooLong) ||
		errors.Is(err, room.ErrInvalidMaxOccupancy) ||
		errors.Is(err, room.ErrInvalidRoomPrice)
}

// CreateRoomType handles room type creation by an administrator.
func CreateRoomType(service RoomService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req roomTypeRequest

		if err := decodeJSON(c.Writer, c.Request, &req); err != nil {
			var maxBytesError *http.MaxBytesError

			if errors.As(err, &maxBytesError) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{
					"error": "request body too large",
				})
				return
			}

			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid request body",
			})
			return
		}

		created, err := service.Create(
			c.Request.Context(),
			room.RoomType{
				HotelID:            c.Param("id"),
				Name:               req.Name,
				Description:        req.Description,
				MaxOccupancy:       req.MaxOccupancy,
				PricePerNightPaise: req.PricePerNightPaise,
			},
		)
		if err != nil {
			switch {
			case isRoomTypeValidationError(err):
				c.JSON(http.StatusBadRequest, gin.H{
					"error": err.Error(),
				})

			case errors.Is(err, room.ErrHotelNotFound):
				c.JSON(http.StatusNotFound, gin.H{
					"error": "hotel not found",
				})

			case errors.Is(err, room.ErrRoomTypeAlreadyExists):
				c.JSON(http.StatusConflict, gin.H{
					"error": "room type already exists for this hotel",
				})

			default:
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "internal server error",
				})
			}
			return
		}

		c.JSON(http.StatusCreated, newRoomTypeResponse(created))
	}
}

// UpdateRoomType handles room type updates by an administrator.
func UpdateRoomType(service RoomService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req roomTypeRequest

		if err := decodeJSON(c.Writer, c.Request, &req); err != nil {
			var maxBytesError *http.MaxBytesError

			if errors.As(err, &maxBytesError) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{
					"error": "request body too large",
				})
				return
			}

			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid request body",
			})
			return
		}

		updated, err := service.Update(
			c.Request.Context(),
			c.Param("id"),
			room.RoomType{
				Name:               req.Name,
				Description:        req.Description,
				MaxOccupancy:       req.MaxOccupancy,
				PricePerNightPaise: req.PricePerNightPaise,
			},
		)
		if err != nil {
			switch {
			case errors.Is(err, room.ErrRoomTypeNotFound):
				c.JSON(http.StatusNotFound, gin.H{
					"error": "room type not found",
				})

			case isRoomTypeValidationError(err):
				c.JSON(http.StatusBadRequest, gin.H{
					"error": err.Error(),
				})

			case errors.Is(err, room.ErrRoomTypeAlreadyExists):
				c.JSON(http.StatusConflict, gin.H{
					"error": "room type already exists for this hotel",
				})

			default:
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "internal server error",
				})
			}
			return
		}

		c.JSON(http.StatusOK, newRoomTypeResponse(updated))
	}
}

// GetRoomType handles public retrieval of a room type by ID.
func GetRoomType(service RoomService) gin.HandlerFunc {
	return func(c *gin.Context) {
		found, err := service.GetByID(
			c.Request.Context(),
			c.Param("id"),
		)

		if err != nil {
			if errors.Is(err, room.ErrRoomTypeNotFound) {
				c.JSON(http.StatusNotFound, gin.H{
					"error": "room type not found",
				})
				return
			}

			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "internal server error",
			})
			return
		}

		c.JSON(http.StatusOK, newRoomTypeResponse(found))
	}
}

// ListRoomTypesByHotelID handles public listing of a hotel's room types.
func ListRoomTypesByHotelID(service RoomService) gin.HandlerFunc {
	return func(c *gin.Context) {
		rooms, err := service.ListByHotelID(
			c.Request.Context(),
			c.Param("id"),
		)
		if err != nil {
			switch {
			case errors.Is(err, room.ErrInvalidHotelID):
				c.JSON(http.StatusBadRequest, gin.H{
					"error": err.Error(),
				})

			case errors.Is(err, room.ErrHotelNotFound):
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

		response := roomTypeListResponse{
			Rooms: make([]roomTypeResponse, 0, len(rooms)),
		}

		for i := range rooms {
			response.Rooms = append(
				response.Rooms,
				newRoomTypeResponse(&rooms[i]),
			)
		}

		c.JSON(http.StatusOK, response)
	}
}
