package models

import "time"

type Entry struct {
	Data       any
	Expiration time.Time
}
