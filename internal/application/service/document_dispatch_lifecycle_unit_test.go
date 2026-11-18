package service

import (
	"context"
	"sync"
	"testing"
)

func TestDocumentLifecycleConcurrentStopAndCompletion(t *testing.T) {
	var lifecycle documentDispatchLifecycle
	var workers sync.WaitGroup
	for range 32 {
		ctx, finish, admitted := lifecycle.begin(context.Background())
		if !admitted {
			t.Fatal("initial handler was rejected")
		}
		workers.Go(func() {
			<-ctx.Done()
			finish()
		})
	}
	var stoppers sync.WaitGroup
	for range 8 {
		stoppers.Go(func() {
			if err := lifecycle.stop(context.Background()); err != nil {
				t.Errorf("stop: %v", err)
			}
		})
	}
	stoppers.Wait()
	workers.Wait()
	if !lifecycle.stopping() {
		t.Fatal("shutdown did not close admission")
	}
	if _, _, admitted := lifecycle.begin(context.Background()); admitted {
		t.Fatal("handler admitted after shutdown")
	}
}
