package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const knowledgeReconcileLockTTL = 2 * time.Hour

const knowledgeReconcileRenewInterval = 30 * time.Second

type knowledgeReconcileGate struct {
	token chan struct{}
	refs  int
}

var localKnowledgeReconcileGates = struct {
	sync.Mutex
	gates map[string]*knowledgeReconcileGate
}{
	gates: make(map[string]*knowledgeReconcileGate),
}

var (
	renewKnowledgeReconcileLock = redis.NewScript(`
		if redis.call("GET", KEYS[1]) == ARGV[1] then
			return redis.call("PEXPIRE", KEYS[1], ARGV[2])
		end
		return 0
	`)
	releaseKnowledgeReconcileLock = redis.NewScript(`
		if redis.call("GET", KEYS[1]) == ARGV[1] then
			return redis.call("DEL", KEYS[1])
		end
		return 0
	`)
)

// acquireKnowledgeReconcileLock serializes mutable binding operations for one
// knowledge generation. Lite mode uses a process-local gate; standard mode
// uses an ownership-token Redis lease shared by document and image workers.
func acquireKnowledgeReconcileLock(
	ctx context.Context,
	client *redis.Client,
	knowledgeID string,
) (context.Context, func(), error) {
	return acquireKnowledgeReconcileLockWithRenewInterval(
		ctx,
		client,
		knowledgeID,
		knowledgeReconcileRenewInterval,
	)
}

func acquireKnowledgeReconcileLockWithRenewInterval(
	ctx context.Context,
	client *redis.Client,
	knowledgeID string,
	renewInterval time.Duration,
) (context.Context, func(), error) {
	if knowledgeID == "" {
		return nil, nil, errors.New("knowledge reconciliation requires an ID")
	}
	if client == nil {
		localKnowledgeReconcileGates.Lock()
		gate := localKnowledgeReconcileGates.gates[knowledgeID]
		if gate == nil {
			gate = &knowledgeReconcileGate{token: make(chan struct{}, 1)}
			localKnowledgeReconcileGates.gates[knowledgeID] = gate
		}
		gate.refs++
		localKnowledgeReconcileGates.Unlock()
		detach := func() {
			localKnowledgeReconcileGates.Lock()
			gate.refs--
			if gate.refs == 0 && localKnowledgeReconcileGates.gates[knowledgeID] == gate {
				delete(localKnowledgeReconcileGates.gates, knowledgeID)
			}
			localKnowledgeReconcileGates.Unlock()
		}
		select {
		case gate.token <- struct{}{}:
			var once sync.Once
			return ctx, func() {
				once.Do(func() {
					<-gate.token
					detach()
				})
			}, nil
		case <-ctx.Done():
			detach()
			return nil, nil, ctx.Err()
		}
	}

	key := "weknora:knowledge:reconcile:" + knowledgeID
	owner := uuid.NewString()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		acquired, err := client.SetNX(ctx, key, owner, knowledgeReconcileLockTTL).Result()
		if err != nil {
			return nil, nil, err
		}
		if acquired {
			break
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-ticker.C:
		}
	}

	lockCtx, cancelLock := context.WithCancel(ctx)
	stopRenewal := make(chan struct{})
	renewalDone := make(chan struct{})
	go func() {
		defer close(renewalDone)
		ticker := time.NewTicker(renewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopRenewal:
				return
			case <-ticker.C:
				renewCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				renewed, err := renewKnowledgeReconcileLock.Run(
					renewCtx,
					client,
					[]string{key},
					owner,
					knowledgeReconcileLockTTL.Milliseconds(),
				).Int64()
				cancel()
				if err != nil || renewed != 1 {
					cancelLock()
					return
				}
			}
		}
	}()

	var once sync.Once
	return lockCtx, func() {
		once.Do(func() {
			close(stopRenewal)
			<-renewalDone
			cancelLock()
			releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = releaseKnowledgeReconcileLock.Run(
				releaseCtx,
				client,
				[]string{key},
				owner,
			).Result()
		})
	}, nil
}
