package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRetryDeliveryOutcomeMarksFailedOnLastAttempt(t *testing.T) {
	t.Parallel()

	mid := types.WithTaskRetryMetadata(context.Background(), 2, 5)
	status, finished := retryDeliveryOutcome(mid)
	if status != types.WebhookDeliveryPending || finished {
		t.Fatalf("mid retry: status=%s finished=%v, want pending/false", status, finished)
	}

	last := types.WithTaskRetryMetadata(context.Background(), 5, 5)
	status, finished = retryDeliveryOutcome(last)
	if status != types.WebhookDeliveryFailed || !finished {
		t.Fatalf("last retry: status=%s finished=%v, want failed/true", status, finished)
	}

	status, finished = retryDeliveryOutcome(context.Background())
	if status != types.WebhookDeliveryPending || finished {
		t.Fatalf("plain ctx: status=%s finished=%v, want pending/false", status, finished)
	}
}
