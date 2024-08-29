package ember

import (
	"errors"
)

var (
	ErrSetFailed      = errors.New("failed to set value in cache")
	ErrKeyNotFound    = errors.New("key not found")
	ErrMaxSizeReached = errors.New("max cache size reached")
)
