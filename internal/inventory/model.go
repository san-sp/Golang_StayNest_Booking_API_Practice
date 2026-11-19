package inventory

import "time"

type DailyInventory struct {
	RoomTypeID     string
	StayDate       time.Time
	TotalRooms     int
	AvailableRooms int
	UpdatedAt      time.Time
}
