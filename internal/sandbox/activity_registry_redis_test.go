package sandbox

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/background/backgroundtest"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestRedisActivityRegistryContract(t *testing.T) {
	backgroundtest.RunActivityRegistryContract(t,
		func(t *testing.T) (interfaces.ActivityRegistry, backgroundtest.Advance) {
			store, _, mini := newRedisBindingTestStore(t)
			now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
			mini.SetTime(now)
			advance := func(d time.Duration) {
				now = now.Add(d)
				mini.SetTime(now)
				mini.FastForward(d) // also expires key TTLs
			}
			return NewRedisActivityRegistry(store), advance
		})
}

func TestRedisActivityRegistryUsesBindingHashTag(t *testing.T) {
	store, _, _ := newRedisBindingTestStore(t)
	key := SessionSandboxKey{TenantID: 7, SessionID: "s1"}
	require.Equal(t, "weknora:sandbox:session:{test:7:s1}:activity", store.activityKey(key))
	require.Equal(t, "weknora:sandbox:session:{test:7:s1}:turn", store.turnKey(key))
}

func TestRedisActivityRegistryRejectsInvalidSession(t *testing.T) {
	store, _, _ := newRedisBindingTestStore(t)
	r := NewRedisActivityRegistry(store)
	err := r.Add(t.Context(), interfaces.SessionRef{}, interfaces.BackgroundActivity{ID: "x"}, time.Minute, 0)
	require.Error(t, err)
}
