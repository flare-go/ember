package utils

import "time"

func GetExpirationTime(defaultTime time.Duration, ttl ...time.Duration) time.Duration {

	if len(ttl) > 0 {
		return ttl[0]
	}

	return defaultTime
}
