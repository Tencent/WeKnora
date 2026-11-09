package admission

import (
	"github.com/Tencent/WeKnora/internal/models/limiter"
	"github.com/Tencent/WeKnora/internal/types"
	"testing"
	"time"
)

func TestUnsavedConnectionIdentity(t *testing.T) {
	if err := limiter.ConfigureSharedGate(limiter.SharedGateConfig{Enabled: true, Concurrency: 1, MaxWaiting: 1, InteractiveBurst: 3, MaxWait: time.Second}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := limiter.ConfigureSharedGate(limiter.SharedGateConfig{}); err != nil {
			t.Error(err)
		}
	})
	a, err := Register("", "model", "http://127.0.0.1:8083/v1", types.ModelSourceRemote)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Register("", "model", "http://127.0.0.1:8083/other", types.ModelSourceRemote)
	if err != nil || a != b {
		t.Fatalf("alias identity %q %q %v", a, b, err)
	}
	c, err := Register("", "model", "http://127.0.0.1:8084/v1", types.ModelSourceRemote)
	if err != nil || a == c {
		t.Fatal("different endpoint reused temporary identity")
	}
	if _, err := Register("", "model", "http://secret:password@127.0.0.1:8083", types.ModelSourceRemote); err == nil {
		t.Fatal("credential endpoint accepted")
	}
}
