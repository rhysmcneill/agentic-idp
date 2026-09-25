package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBootstrapRetry_SucceedsFirstTry(t *testing.T) {
	calls := 0
	err := bootstrapRetry(context.Background(), time.Millisecond, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("bootstrapRetry: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestBootstrapRetry_RetriesUntilSuccess(t *testing.T) {
	calls := 0
	err := bootstrapRetry(context.Background(), time.Millisecond, func() error {
		calls++
		if calls < 3 {
			return errors.New("not ready yet")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("bootstrapRetry: %v", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestBootstrapRetry_StopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0

	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	err := bootstrapRetry(ctx, time.Millisecond, func() error {
		calls++
		return errors.New("never ready")
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
	if calls == 0 {
		t.Error("fn was never called")
	}
}
