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
// explicit secret is configured, a JWT secret. It never replaces an AES
// encryption key, so upgrading cannot make stored credentials unreadable.
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
	// An existing HMAC (an explicit signing key, or the AES key of an older
	// install) stays the signer exactly as configured: replacing it would
	// invalidate presigned URLs, and copying it into signing.key would leave
	// the AES key in a second file. Sessions then get their own secret.
	if len(utils.SystemHMACKey()) != 0 {
		return ensureDesktopJWTSecret(dir, nil)
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
	if err := os.Setenv("SYSTEM_SIGNING_KEY", string(key)); err != nil {
		return err
	}
	return ensureDesktopJWTSecret(dir, key)
}

// ensureDesktopJWTSecret fills JWT_SECRET when it is unset or still a
// placeholder. A signing key this function generated can also sign sessions.
// A nil signingKey means the signer came from the environment (possibly the
// AES key), so sessions get their own persisted secret.
func ensureDesktopJWTSecret(dir string, signingKey []byte) error {
	jwtSecret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	if jwtSecret != "" && jwtSecret != "weknora-jwt-secret" && jwtSecret != "CHANGE-ME-jwt-secret" {
		return nil
	}
	secret := string(signingKey)
	if signingKey == nil {
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
