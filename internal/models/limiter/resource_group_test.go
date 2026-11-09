package limiter

import (
	"context"
	"testing"
	"time"
)

func TestPhysicalResourceGroupBoundsDifferentEndpoints(t *testing.T) {
	cfg := SharedGateConfig{Enabled: true, Concurrency: 5, MaxWaiting: 8, InteractiveBurst: 100, MaxWait: time.Second, ResourceGroups: map[string][]string{"cpu": {"http://127.0.0.1:8084", "http://127.0.0.1:8085"}}}
	if err := ConfigureSharedGate(cfg); err != nil {
		t.Fatal(err)
	}
	defer ConfigureSharedGate(SharedGateConfig{})
	for id, url := range map[string]string{"embedding": "http://127.0.0.1:8084/v1", "rerank": "http://127.0.0.1:8085/v1"} {
		if err := RegisterModelEndpoint(id, url); err != nil {
			t.Fatal(err)
		}
	}
	release, err := AcquireCall(context.Background(), "embedding", "", 4)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if r, err := AcquireCall(ctx, "rerank", "", 4); err == nil {
		r()
		t.Fatal("different endpoints overlapped on one CPU resource")
	}
	release()
	r, err := AcquireCall(context.Background(), "rerank", "", 4)
	if err != nil {
		t.Fatal(err)
	}
	r()
	if stats := SharedRuntimeStats(); stats.Active != 0 || stats.Waiting != 0 {
		t.Fatalf("lease or cancelled waiter leaked: %+v", stats)
	}
}
