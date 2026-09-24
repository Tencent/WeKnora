package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/utils"
)

// ensureDesktopSigningKey persists the desktop signing key and, when no
// explicit secret is configured, a separate JWT secret. It never replaces an
// AES encryption key, so upgrading cannot make stored credentials unreadable.
// The JWT secret is never that AES key: a leaked session secret must not
// decrypt stored credentials.
func ensureDesktopSigningKey() error {
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
	// An existing HMAC (AES key, or a signing key already in the environment)
	// must stay the signer. Generating a fresh file here would invalidate
	// presigned URLs on upgrade.
	prior := append([]byte(nil), utils.SystemHMACKey()...)
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if len(prior) >= 32 {
			key = prior
		} else {
			bytes := make([]byte, 32)
			if _, err := rand.Read(bytes); err != nil {
				return err
			}
			key = []byte(base64.RawURLEncoding.EncodeToString(bytes))
		}
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
	if err := os.Setenv("SYSTEM_SIGNING_KEY", string(key)); err != nil {
		return err
	}
	return ensureDesktopJWTSecret(dir, key)
}

// ensureDesktopJWTSecret fills JWT_SECRET when it is unset or still a
// placeholder. A freshly generated signing key can also sign sessions. The
// AES encryption key cannot: it stays the HMAC signer for existing presigned
// URLs, and sessions get their own persisted secret.
func ensureDesktopJWTSecret(dir string, signingKey []byte) error {
	jwtSecret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	if jwtSecret != "" && jwtSecret != "weknora-jwt-secret" && jwtSecret != "CHANGE-ME-jwt-secret" {
		return nil
	}
	secret := string(signingKey)
	if aes := os.Getenv("SYSTEM_AES_KEY"); aes != "" && secret == aes {
		var err error
		secret, err = desktopJWTSecret(dir)
		if err != nil {
			return err
		}
	}
	return os.Setenv("JWT_SECRET", secret)
}

func desktopJWTSecret(dir string) (string, error) {
	path := filepath.Join(dir, "jwt.key")
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		bytes := make([]byte, 32)
		if _, err := rand.Read(bytes); err != nil {
			return "", err
		}
		key = []byte(base64.RawURLEncoding.EncodeToString(bytes))
		f, createErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if os.IsExist(createErr) {
			key, err = os.ReadFile(path)
		} else if createErr != nil {
			return "", createErr
		} else {
			_, err = f.Write(key)
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		return "", err
	}
	if len(key) < 32 {
		return "", fmt.Errorf("invalid desktop jwt secret in %s", path)
	}
	return string(key), nil
}
