package handler

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRuntimeWorkerPoolPendingRestart(t *testing.T) {
	for _, tc := range []struct {
		name        string
		concurrency []int
		pending     *bool
	}{
		{name: "no heartbeat"},
		{name: "configured value is live", concurrency: []int{8}, pending: boolPointer(false)},
		{name: "decrease pending", concurrency: []int{16}, pending: boolPointer(true)},
		{name: "increase pending", concurrency: []int{4}, pending: boolPointer(true)},
		{name: "matching instance before outdated instance", concurrency: []int{8, 16}, pending: boolPointer(true)},
		{name: "rolling restart", concurrency: []int{16, 8}, pending: boolPointer(true)},
		{name: "all instances restarted", concurrency: []int{8, 8}, pending: boolPointer(false)},
		{name: "matching average still requires restart", concurrency: []int{4, 12}, pending: boolPointer(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pools := []RuntimeWorkerPool{{Name: types.WorkerPoolCore, Concurrency: 8}}
			servers := []types.WorkerServerStat{}
			for _, concurrency := range tc.concurrency {
				servers = append(servers, types.WorkerServerStat{
					Status: "active", Concurrency: concurrency,
					Queues: types.QueueWeightsForPool(types.WorkerPoolCore),
				})
			}
			// Stopped servers must not make a completed restart look pending.
			servers = append(servers, types.WorkerServerStat{
				Status: "stopped", Concurrency: 32,
				Queues: types.QueueWeightsForPool(types.WorkerPoolCore),
			})
			aggregateRuntimeWorkerPools(pools, servers)
			got := pools[0].PendingRestart
			if tc.pending == nil {
				if got != nil {
					t.Fatalf("pending_restart = %v, want unknown", *got)
				}
			} else if got == nil || *got != *tc.pending {
				t.Fatalf("pending_restart = %v, want %v", got, *tc.pending)
			}
			if pools[0].Instances != len(tc.concurrency) {
				t.Fatalf("instances = %d, want %d", pools[0].Instances, len(tc.concurrency))
			}
		})
	}
}

func boolPointer(value bool) *bool { return &value }
