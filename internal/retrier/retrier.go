package retrier

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"
)

// Temporary interface defines a method to indicate if an error is temporary.
type Temporary interface {
	Temporary() bool
}

// IsTemporary checks if the error is temporary.
func IsTemporary(err error) bool {
	var temp Temporary
	if errors.As(err, &temp) {
		return temp.Temporary()
	}
	return false
}

// BackoffStrategy defines the type for backoff strategies.
type BackoffStrategy int

const (
	ExponentialBackoff BackoffStrategy = iota
	LinearBackoff
	FibonacciBackoff
)

// Retrier implements a retry mechanism with backoff strategies.
type Retrier struct {
	maxAttempts    int
	baseDelay      time.Duration
	maxDelay       time.Duration
	factor         float64
	jitter         float64
	randPool       *sync.Pool
	strategy       BackoffStrategy
	fibonacciCache []time.Duration
	TempErrorFunc  func(error) bool // Custom temporary error function
}

// NewRetrier creates a new Retrier instance.
func NewRetrier(maxAttempts int, baseDelay, maxDelay time.Duration, factor, jitter float64, strategy BackoffStrategy, tempErrorFunc func(error) bool) *Retrier {
	return &Retrier{
		maxAttempts:    maxAttempts,
		baseDelay:      baseDelay,
		maxDelay:       maxDelay,
		factor:         factor,
		jitter:         jitter,
		randPool:       &sync.Pool{New: func() any { return rand.New(rand.NewSource(time.Now().UnixNano())) }},
		strategy:       strategy,
		fibonacciCache: []time.Duration{0, baseDelay},
		TempErrorFunc:  tempErrorFunc,
	}
}

// Run executes the given function with retry logic.
func (r *Retrier) Run(ctx context.Context, fn func() error) error {
	var err error
	var errs []error
	for attempt := 0; attempt < r.maxAttempts; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}

		var isTemp bool
		if r.TempErrorFunc != nil {
			isTemp = r.TempErrorFunc(err)
		} else {
			isTemp = IsTemporary(err)
		}

		if !isTemp {
			// Non-temporary error, do not retry
			return err
		}

		errs = append(errs, err)

		if attempt == r.maxAttempts-1 {
			break
		}

		delay := r.calculateDelay(attempt)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
			// Continue to next retry
		}
	}

	// Wrap all errors
	return fmt.Errorf("max retry attempts reached: %w", err)
}

// calculateDelay calculates the delay based on the backoff strategy.
func (r *Retrier) calculateDelay(attempt int) time.Duration {
	var delay float64

	switch r.strategy {
	case ExponentialBackoff:
		delay = float64(r.baseDelay) * math.Pow(r.factor, float64(attempt))
	case LinearBackoff:
		delay = float64(r.baseDelay) * float64(attempt+1)
	case FibonacciBackoff:
		delay = float64(r.getFibonacciDelay(attempt))
	default:
		delay = float64(r.baseDelay) * math.Pow(r.factor, float64(attempt))
	}

	if delay > float64(r.maxDelay) {
		delay = float64(r.maxDelay)
	}

	rng := r.randPool.Get().(*rand.Rand)
	jitterAmount := rng.Float64() * r.jitter * delay
	r.randPool.Put(rng)

	delay += jitterAmount
	if delay < 0 {
		delay = 0
	}
	if delay > float64(time.Hour) {
		delay = float64(time.Hour)
	}
	return time.Duration(delay)
}

// getFibonacciDelay returns the Fibonacci delay for the given attempt.
func (r *Retrier) getFibonacciDelay(attempt int) time.Duration {
	if attempt < len(r.fibonacciCache) {
		return r.fibonacciCache[attempt]
	}

	for len(r.fibonacciCache) <= attempt {
		next := r.fibonacciCache[len(r.fibonacciCache)-1] + r.fibonacciCache[len(r.fibonacciCache)-2]
		if next > r.maxDelay {
			next = r.maxDelay
		}
		r.fibonacciCache = append(r.fibonacciCache, next)
	}
	return r.fibonacciCache[attempt]
}
