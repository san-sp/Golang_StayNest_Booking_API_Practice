package hotel

import "time"

// ListParams contains filters and pagination settings for hotel listing.
type ListParams struct {
	City   string
	Limit  int
	Offset int
}

// ListResult contains the matching hotels and total result count.
type ListResult struct {
	Hotels []Hotel
	Total  int
	Limit  int
	Offset int
}

// SearchParams contains the criteria used to search for available hotels.
type SearchParams struct {
	City     string
	CheckIn  time.Time
	CheckOut time.Time
	Guests   int
	Rooms    int
}

// AvailableRoom contains a room type that has sufficient
// inventory for the requested stay.
type AvailableRoom struct {
	ID                 string
	Name               string
	Description        string
	MaxOccupancy       int
	PricePerNightPaise int64
	AvailableRooms     int
}

// SearchResult groups matching room types under a hotel.
type SearchResult struct {
	Hotel Hotel
	Rooms []AvailableRoom
}
