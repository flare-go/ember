package retrier

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"time"
)

const (
	minMaxAttempts = 1
	minBaseDelay   = time.Millisecond
	minFactor      = 1.0
	maxJitter      = 1.0
)

// ExponentialBackoff represents a backoff strategy where intervals exponentially increase.
// LinearBackoff represents a backoff strategy where intervals increase linearly.
// FibonacciBackoff represents a backoff strategy where intervals increase based on the Fibonacci sequence.
const (
	ExponentialBackoff BackoffStrategy = iota
	LinearBackoff
	FibonacciBackoff
)

var (
	// ErrInvalidMaxAttempts is returned when the max attempts parameter is invalid.
	ErrInvalidMaxAttempts = errors.New("max attempts must be at least 1")
	// ErrInvalidBaseDelay is returned when the base delay parameter is invalid.
	ErrInvalidBaseDelay = errors.New("base delay must be at least 1ms")
	// ErrInvalidFactor is returned when the factor parameter is invalid.
	ErrInvalidFactor = errors.New("factor must be at least 1.0")
	// ErrInvalidJitter is returned when the jitter parameter is invalid.
	ErrInvalidJitter = errors.New("jitter must be between 0 and 1")
)

// BackoffStrategy defines the strategy used for calculating backoff intervals in retry mechanisms.
type BackoffStrategy int

// Retrier provides functionality to execute a function with retry logic based on different backoff strategies.
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

type cryptoSource struct{}

// NewRetrier creates a new Retrier instance with specified parameters for handling retry logic.
// Parameters:
// - maxAttempts: maximum number of retry attempts.
// - baseDelay: basic delay duration between retries.
// - maxDelay: maximum allowed delay duration between retries.
// - factor: multiplier for exponential backoff calculation.
// - jitter: randomness factor to avoid retry storms.
// - strategy: backoff strategy to use (e.g., ExponentialBackoff, LinearBackoff, FibonacciBackoff).
// - tempErrorFunc: optional function to determine if an error is temporary.
func NewRetrier(maxAttempts int, baseDelay, maxDelay time.Duration, factor, jitter float64, strategy BackoffStrategy, tempErrorFunc func(error) bool) (*Retrier, error) {
	if maxAttempts < minMaxAttempts {
		return nil, ErrInvalidMaxAttempts
	}
	if baseDelay < minBaseDelay {
		return nil, ErrInvalidBaseDelay
	}
	if factor < minFactor {
		return nil, ErrInvalidFactor
	}
	if jitter < 0 || jitter > maxJitter {
		return nil, ErrInvalidJitter
	}

	return &Retrier{
		maxAttempts: maxAttempts,
		baseDelay:   baseDelay,
		maxDelay:    maxDelay,
		factor:      factor,
		jitter:      jitter,
		randPool: &sync.Pool{
			New: func() any {
				return &cryptoSource{}
			},
		},
		strategy:       strategy,
		fibonacciCache: []time.Duration{0, baseDelay},
		TempErrorFunc:  tempErrorFunc,
	}, nil
}

// Run executes the provided function with retries according to the Retriever's configuration.
func (r *Retrier) Run(ctx context.Context, fn func() error) error {
	var err error
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

// calculateDelay computes the delay duration based on the retry attempt and backoff strategy.
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

// getFibonacciDelay returns the delay for the given attempt using the Fibonacci sequence.
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
