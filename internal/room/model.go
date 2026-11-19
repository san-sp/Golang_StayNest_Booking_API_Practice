package room

import "time"

type RoomType struct {
	ID                 string
	HotelID            string
	Name               string
	Description        string
	MaxOccupancy       int
	PricePerNightPaise int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}
