package secrets

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// keyringService is the namespace prefix passed to OS keyring backends.
// Final keyring entries look like service="weknora:<context>", account="<key>".
const keyringService = "weknora"

// KeyringStore is the OS-backed credential store: macOS Keychain, GNOME
// libsecret, KWallet, Windows Credential Manager. Falls back to ErrUnsupported
// (callers swap to FileStore when this happens, see NewBestEffortStore).
type KeyringStore struct{}

// NewKeyringStore returns a KeyringStore. The constructor never fails; first
// real Get/Set surfaces ErrUnsupported on systems with no keyring backend.
func NewKeyringStore() *KeyringStore { return &KeyringStore{} }

// service returns the per-context service identifier. Splitting by context
// (rather than by host) follows the Supabase pattern: a user with
// two tenants on the same host gets two distinct keyring namespaces.
func (k *KeyringStore) service(context string) string {
	return keyringService + ":" + context
}

func (k *KeyringStore) Get(context, key string) (string, error) {
	v, err := keyring.Get(k.service(context), key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("keyring get: %w", err)
	}
	return v, nil
}

func (k *KeyringStore) Set(context, key, value string) error {
	if err := keyring.Set(k.service(context), key, value); err != nil {
		return fmt.Errorf("keyring set: %w", err)
	}
	return nil
}

func (k *KeyringStore) Delete(context, key string) error {
	err := keyring.Delete(k.service(context), key)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("keyring delete: %w", err)
	}
	return nil
}

// Ref returns the keychain:// URI under which a secret is stored.
func (k *KeyringStore) Ref(context, key string) string {
	return "keychain://" + keyringService + "/" + context + "/" + key
}

// NewBestEffortStore returns a Store that prefers keyring (when available)
// and silently degrades to FileStore on systems without a keyring backend
// (e.g. headless CI, WSL without DBus, agent containers).
//
// Probe is performed by attempting a no-op Get; if the backend signals that
// it is unsupported we return the file store directly.
func NewBestEffortStore() (Store, error) {
	k := NewKeyringStore()
	// Use a sentinel context/key that should not exist; we expect either
	// ErrNotFound (backend works, just empty) or ErrUnsupported (no backend).
	_, err := keyring.Get(k.service("__probe__"), "__probe__")
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return k, nil
	}
	// Anything else (including keyring.ErrUnsupportedPlatform) → file store.
	return NewFileStore()
}
