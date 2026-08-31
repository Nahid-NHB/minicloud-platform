package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetrySucceedsAfterFailures(t *testing.T) {
	calls := 0
	err := Do(context.Background(), 5, time.Millisecond, time.Millisecond, nil, func() error {
		calls++
		if calls < 3 {
			return errors.New("retry me")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestRetryGivesUpOnPermanent(t *testing.T) {
	calls := 0
	err := Do(context.Background(), 5, time.Millisecond, time.Millisecond, Is, func() error {
		calls++
		return Permanent(errors.New("no"))
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}
