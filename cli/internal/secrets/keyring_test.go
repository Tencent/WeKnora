package secrets

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// Use the in-memory keyring backend for deterministic tests.
//
// go-keyring exposes MockInit so tests don't need a real OS keychain.
func TestKeyringStore_RoundTrip(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(func() {
		// Reset the mock so other test packages don't see leaked state.
		keyring.MockInit()
	})
	k := NewKeyringStore()

	// Get on missing returns ErrNotFound.
	_, err := k.Get("prod", "access")
	require.ErrorIs(t, err, ErrNotFound)

	// Set + Get.
	require.NoError(t, k.Set("prod", "access", "jwt-x"))
	got, err := k.Get("prod", "access")
	require.NoError(t, err)
	assert.Equal(t, "jwt-x", got)

	// Delete + Get.
	require.NoError(t, k.Delete("prod", "access"))
	_, err = k.Get("prod", "access")
	require.ErrorIs(t, err, ErrNotFound)

	// Delete on missing is a no-op.
	require.NoError(t, k.Delete("prod", "access"))
}

func TestKeyringStore_NamespaceIsolation(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(func() { keyring.MockInit() })
	k := NewKeyringStore()
	require.NoError(t, k.Set("prod", "access", "P"))
	require.NoError(t, k.Set("staging", "access", "S"))

	got, _ := k.Get("prod", "access")
	assert.Equal(t, "P", got)
	got, _ = k.Get("staging", "access")
	assert.Equal(t, "S", got)
}

func TestNewBestEffortStore(t *testing.T) {
	keyring.MockInit() // mock backend "supports" keyring → returns KeyringStore
	t.Cleanup(func() { keyring.MockInit() })
	s, err := NewBestEffortStore()
	require.NoError(t, err)
	_, ok := s.(*KeyringStore)
	assert.True(t, ok, "with mock keyring backend, BestEffort must return KeyringStore")
}

func TestKeyringStore_Ref(t *testing.T) {
	k := NewKeyringStore()
	got := k.Ref("prod", "access")
	assert.Equal(t, "keychain://weknora/prod/access", got)
}

// A backend that never answers must not hold the command: the probe gives up
// after keyringProbeTimeout and falls back to the file store.
func TestNewBestEffortStoreFallsBackWhenTheProbeHangs(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	prev := keyringGet
	keyringGet = func(service, user string) (string, error) {
		<-release
		return "", keyring.ErrNotFound
	}
	t.Cleanup(func() { keyringGet = prev })

	type result struct {
		store Store
		err   error
	}
	done := make(chan result, 1)
	go func() {
		s, err := NewBestEffortStore()
		done <- result{store: s, err: err}
	}()

	select {
	case got := <-done:
		require.NoError(t, got.err)
		_, ok := got.store.(*FileStore)
		assert.True(t, ok, "a hung keyring probe must fall back to the file store")
	case <-time.After(keyringProbeTimeout + 3*time.Second):
		t.Fatal("NewBestEffortStore blocked on a hung keyring backend")
	}
}
