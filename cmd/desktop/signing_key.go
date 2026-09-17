package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Tencent/WeKnora/internal/utils"
)

// ensureDesktopSigningKey persists a signing-only key. It never replaces an
// AES encryption key, so upgrading cannot make stored credentials unreadable.
func ensureDesktopSigningKey() error {
	if len(utils.SystemHMACKey()) != 0 {
		return nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	return ensureDesktopSigningKeyInDir(filepath.Join(dir, "WeKnora Lite"))
}

func ensureDesktopSigningKeyInDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "signing.key")
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		bytes := make([]byte, 32)
		if _, err := rand.Read(bytes); err != nil {
			return err
		}
		key = []byte(base64.RawURLEncoding.EncodeToString(bytes))
		f, createErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if os.IsExist(createErr) {
			key, err = os.ReadFile(path)
		} else if createErr != nil {
			return createErr
		} else {
			_, err = f.Write(key)
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		return err
	}
	if len(key) < 32 {
		return fmt.Errorf("invalid desktop signing key in %s", path)
	}
	return os.Setenv("SYSTEM_SIGNING_KEY", string(key))
}
