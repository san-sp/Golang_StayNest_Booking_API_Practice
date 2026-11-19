package hotel

import "time"

type Hotel struct {
	ID          string
	Name        string
	Description string
	Address     string
	City        string
	Country     string
	Timezone    string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
