// Package retry provides a small exponential-backoff helper.
package retry

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"time"
)

// IsPermanent lets a caller give up early on certain errors.
type IsPermanent func(err error) bool

// Do runs fn with exponential backoff until success, permanent error,
// or ctx canceled.
func Do(ctx context.Context, maxAttempts int, base, cap time.Duration, isPermanent IsPermanent, fn func() error) error {
	if base <= 0 {
		base = 100 * time.Millisecond
	}
	if cap <= 0 {
		cap = 30 * time.Second
	}
	if maxAttempts <= 0 {
		maxAttempts = 6
	}
	var last error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := fn()
		if err == nil {
			return nil
		}
		last = err
		if isPermanent != nil && isPermanent(err) {
			return err
		}
		if attempt == maxAttempts {
			break
		}
		// Backoff: base * 2^(attempt-1), capped, with jitter.
		mult := math.Pow(2, float64(attempt-1))
		d := time.Duration(float64(base) * mult)
		if d > cap {
			d = cap
		}
		jitter := time.Duration(rand.Int64N(int64(d / 4)))
		d += jitter
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	return last
}

// Permanent wraps an error to mark it as not retryable.
func Permanent(err error) error { return permanentErr{err} }

type permanentErr struct{ err error }

func (p permanentErr) Error() string { return p.err.Error() }
func (p permanentErr) Unwrap() error { return p.err }

// IsPermanent returns true if err or any wrapped error is permanent.
func Is(err error) bool {
	var pe permanentErr
	return errors.As(err, &pe)
}
