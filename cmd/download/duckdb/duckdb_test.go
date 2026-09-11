package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryWithBackoffRetriesTransientFailure(t *testing.T) {
	attempts := 0
	wantErr := errors.New("temporary upstream failure")

	err := retryWithBackoff(context.Background(), 3, 0, func() error {
		attempts++
		if attempts < 3 {
			return wantErr
		}
		return nil
	})

	if err != nil {
		t.Fatalf("retryWithBackoff() error = %v, want nil", err)
	}
	if attempts != 3 {
		t.Fatalf("operation attempts = %d, want 3", attempts)
	}
}

func TestRetryWithBackoffReturnsLastError(t *testing.T) {
	attempts := 0
	wantErr := errors.New("persistent upstream failure")

	err := retryWithBackoff(context.Background(), 2, 0, func() error {
		attempts++
		return wantErr
	})

	if !errors.Is(err, wantErr) {
		t.Fatalf("retryWithBackoff() error = %v, want %v", err, wantErr)
	}
	if attempts != 2 {
		t.Fatalf("operation attempts = %d, want 2", attempts)
	}
}

func TestRetryWithBackoffHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := retryWithBackoff(ctx, 2, time.Hour, func() error {
		return errors.New("temporary upstream failure")
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("retryWithBackoff() error = %v, want context.Canceled", err)
	}
}
