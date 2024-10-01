package retrier

import "errors"

// Temporary indicates if an error condition is temporary and may succeed if retried.
type Temporary interface {
	Temporary() bool
}

// IsTemporary checks if the provided error implements the Temporary interface and returns true if it does.
func IsTemporary(err error) bool {
	var temp Temporary
	if errors.As(err, &temp) {
		return temp.Temporary()
	}
	return false
}
