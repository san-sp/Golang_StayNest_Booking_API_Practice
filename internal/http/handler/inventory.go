package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"staynest/internal/inventory"
)

type InventoryService interface {
	SetDailyCapacity(
		ctx context.Context,
		roomTypeID string,
		stayDate time.Time,
		totalRooms int,
	) (*inventory.DailyInventory, error)

	ListByRoomTypeAndDateRange(
		ctx context.Context,
		roomTypeID string,
		startDate time.Time,
		endDate time.Time,
	) ([]inventory.DailyInventory, error)
}

type setInventoryRequest struct {
	TotalRooms int `json:"total_rooms"`
}

type dailyInventoryResponse struct {
	RoomTypeID     string `json:"room_type_id"`
	StayDate       string `json:"stay_date"`
	TotalRooms     int    `json:"total_rooms"`
	AvailableRooms int    `json:"available_rooms"`
	UpdatedAt      string `json:"updated_at"`
}

type inventoryListResponse struct {
	Inventory []dailyInventoryResponse `json:"inventory"`
}

func newDailyInventoryResponse(
	daily *inventory.DailyInventory,
) dailyInventoryResponse {
	return dailyInventoryResponse{
		RoomTypeID:     daily.RoomTypeID,
		StayDate:       daily.StayDate.Format("2006-01-02"),
		TotalRooms:     daily.TotalRooms,
		AvailableRooms: daily.AvailableRooms,
		UpdatedAt:      daily.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func isInventoryValidationError(err error) bool {
	return errors.Is(err, inventory.ErrInvalidRoomTypeID) ||
		errors.Is(err, inventory.ErrInvalidStayDate) ||
		errors.Is(err, inventory.ErrInvalidTotalRooms) ||
		errors.Is(err, inventory.ErrInvalidDateRange)
}

// SetDailyInventory handles administrator inventory capacity updates.
func SetDailyInventory(service InventoryService) gin.HandlerFunc {
	return func(c *gin.Context) {
		stayDate, err := time.Parse("2006-01-02", c.Param("date"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "date must use YYYY-MM-DD format",
			})
			return
		}

		var req setInventoryRequest

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

		created, err := service.SetDailyCapacity(
			c.Request.Context(),
			c.Param("id"),
			stayDate,
			req.TotalRooms,
		)
		if err != nil {
			switch {
			case isInventoryValidationError(err):
				c.JSON(http.StatusBadRequest, gin.H{
					"error": err.Error(),
				})

			case errors.Is(err, inventory.ErrRoomTypeNotFound):
				c.JSON(http.StatusNotFound, gin.H{
					"error": "room type not found",
				})

			case errors.Is(err, inventory.ErrCapacityBelowBooked):
				c.JSON(http.StatusConflict, gin.H{
					"error": "total rooms cannot be lower than already booked rooms",
				})

			default:
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "internal server error",
				})
			}
			return
		}

		c.JSON(http.StatusOK, newDailyInventoryResponse(created))
	}
}

// ListRoomInventory handles public inventory queries.
// The start date is inclusive and the end date is exclusive.
func ListRoomInventory(service InventoryService) gin.HandlerFunc {
	return func(c *gin.Context) {
		startDate, err := time.Parse(
			"2006-01-02",
			c.Query("start_date"),
		)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "start_date must use YYYY-MM-DD format",
			})
			return
		}

		endDate, err := time.Parse(
			"2006-01-02",
			c.Query("end_date"),
		)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "end_date must use YYYY-MM-DD format",
			})
			return
		}

		dailyInventory, err := service.ListByRoomTypeAndDateRange(
			c.Request.Context(),
			c.Param("id"),
			startDate,
			endDate,
		)
		if err != nil {
			switch {
			case isInventoryValidationError(err):
				c.JSON(http.StatusBadRequest, gin.H{
					"error": err.Error(),
				})

			case errors.Is(err, inventory.ErrRoomTypeNotFound):
				c.JSON(http.StatusNotFound, gin.H{
					"error": "room type not found",
				})

			default:
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "internal server error",
				})
			}
			return
		}

		response := inventoryListResponse{
			Inventory: make(
				[]dailyInventoryResponse,
				0,
				len(dailyInventory),
			),
		}

		for i := range dailyInventory {
			response.Inventory = append(
				response.Inventory,
				newDailyInventoryResponse(&dailyInventory[i]),
			)
		}

		c.JSON(http.StatusOK, response)
	}
}
