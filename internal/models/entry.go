package models

import (
	"time"

	"go.uber.org/atomic"
)

// Entry represents a cache entry.
type Entry struct {
	Data           []byte
	AccessCount    *atomic.Int64
	LastAccessTime *atomic.Time
	Expiration     time.Time
}

// NewEntry creates a new Entry.
func NewEntry(data []byte, expiration time.Time) *Entry {
	return &Entry{
		Data:           data,
		AccessCount:    atomic.NewInt64(1),
		LastAccessTime: atomic.NewTime(time.Now()),
		Expiration:     expiration,
	}
}

// IsExpired checks if the entry has expired.
func (e *Entry) IsExpired() bool {
	return time.Now().After(e.Expiration)
}

// IncrementAccess increments the access count and updates the last access time.
func (e *Entry) IncrementAccess() {
	e.AccessCount.Inc()
	e.LastAccessTime.Store(time.Now())
}
