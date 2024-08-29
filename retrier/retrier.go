package retrier

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"time"
)

type Retrier struct {
	maxAttempts int
	baseDelay   time.Duration
	maxDelay    time.Duration
	factor      float64
	jitter      float64
}

func NewRetrier(maxAttempts int, baseDelay, maxDelay time.Duration, factor, jitter float64) *Retrier {
	return &Retrier{
		maxAttempts: maxAttempts,
		baseDelay:   baseDelay,
		maxDelay:    maxDelay,
		factor:      factor,
		jitter:      jitter,
	}
}

func (r *Retrier) Run(ctx context.Context, fn func() error) error {
	var err error
	for attempt := 0; attempt < r.maxAttempts; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}

		if attempt == r.maxAttempts-1 {
			break
		}

		delay := r.calculateDelay(attempt)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
			// Continue to next iteration
		}
	}
	return errors.New("max retry attempts reached: " + err.Error())
}

func (r *Retrier) calculateDelay(attempt int) time.Duration {
	delay := float64(r.baseDelay) * math.Pow(r.factor, float64(attempt))
	if delay > float64(r.maxDelay) {
		delay = float64(r.maxDelay)
	}
	jitterAmount := rand.Float64() * r.jitter * delay
	delay += jitterAmount
	return time.Duration(delay)
}
