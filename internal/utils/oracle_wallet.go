package utils

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sijms/go-ora/v2/configurations"
)

// Oracle wallet upload limits. An Autonomous Database wallet ZIP is ~20 KB and
// cwallet.sso a few KB, so 1 MiB is generous while bounding memory and JSON
// body size.
const (
	maxOracleWalletUploadBytes = 1 << 20
	maxOracleWalletSSOBytes    = 1 << 20
	oracleWalletFileName       = "cwallet.sso"
)

// ErrInvalidOracleWallet is returned for uploads that are not a usable wallet.
var ErrInvalidOracleWallet = errors.New("invalid oracle wallet")

// NormalizeOracleWallet validates an uploaded wallet and returns the base64 of
// its auto-login cwallet.sso, which is all go-ora needs for mTLS.
//
// upload is the base64 of either a wallet ZIP (as downloaded from the OCI
// console) or a bare cwallet.sso. ZIP members are only read in memory, never
// extracted to disk, so path traversal (Zip Slip) and symlink members cannot
// reach the filesystem; symlink members are rejected anyway.
func NormalizeOracleWallet(upload string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(upload))
	if err != nil {
		return "", fmt.Errorf("%w: not base64", ErrInvalidOracleWallet)
	}
	if len(raw) == 0 || len(raw) > maxOracleWalletUploadBytes {
		return "", fmt.Errorf("%w: size must be between 1 byte and %d bytes", ErrInvalidOracleWallet,
			maxOracleWalletUploadBytes)
	}
	sso := raw
	if bytes.HasPrefix(raw, []byte("PK\x03\x04")) {
		if sso, err = ssoFromZip(raw); err != nil {
			return "", err
		}
	}
	if err := oracleSSOValidator(sso); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sso), nil
}

func ssoFromZip(raw []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("%w: unreadable ZIP", ErrInvalidOracleWallet)
	}
	for _, f := range zr.File {
		if !strings.EqualFold(path.Base(strings.ReplaceAll(f.Name, "\\", "/")), oracleWalletFileName) {
			continue
		}
		if f.Mode()&fs.ModeSymlink != 0 || f.FileInfo().IsDir() {
			return nil, fmt.Errorf("%w: %s must be a regular file", ErrInvalidOracleWallet, oracleWalletFileName)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidOracleWallet, err)
		}
		defer func() { _ = rc.Close() }()
		// Read one byte past the limit to detect oversized (zip bomb) members.
		data, err := io.ReadAll(io.LimitReader(rc, maxOracleWalletSSOBytes+1))
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidOracleWallet, err)
		}
		if len(data) > maxOracleWalletSSOBytes {
			return nil, fmt.Errorf("%w: %s is too large", ErrInvalidOracleWallet, oracleWalletFileName)
		}
		return data, nil
	}
	return nil, fmt.Errorf("%w: ZIP does not contain %s (auto-login wallet)", ErrInvalidOracleWallet,
		oracleWalletFileName)
}

// oracleSSOValidator is swappable only via SetOracleWalletValidatorForTest.
var oracleSSOValidator = validateSSO

// SetOracleWalletValidatorForTest replaces the cwallet.sso parser check so
// tests can use synthetic wallets. NOT for production use. Returns a restore func.
func SetOracleWalletValidatorForTest(fn func([]byte) error) (restore func()) {
	prev := oracleSSOValidator
	oracleSSOValidator = fn
	return func() { oracleSSOValidator = prev }
}

// validateSSO parses the wallet with go-ora itself, so an accepted upload is
// guaranteed to be readable at connect time. go-ora indexes the file without
// bounds checks, so malformed input panics; that is turned into an error here
// instead of crashing the server on a bad upload.
func validateSSO(sso []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %s is malformed", ErrInvalidOracleWallet, oracleWalletFileName)
		}
	}()
	dir, err := os.MkdirTemp("", "weknora-wallet-check-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	p := filepath.Join(dir, oracleWalletFileName)
	if err := os.WriteFile(p, sso, 0o600); err != nil {
		return err
	}
	if _, err := configurations.NewWallet(p); err != nil {
		return fmt.Errorf("%w: %s cannot be parsed (%v)", ErrInvalidOracleWallet, oracleWalletFileName, err)
	}
	return nil
}

// OracleWalletDir materializes a normalized wallet (base64 cwallet.sso) into a
// private directory and returns it for go-ora's WALLET option. The directory
// is keyed by content hash, so repeated engine builds reuse it and a rotated
// wallet gets a fresh directory. Modes are 0700/0600.
//
// The shared temp dir may be writable by other local users, so both
// directories are checked before use (see ensurePrivateDir) and an existing
// cwallet.sso is only reused when it is a private regular file.
func OracleWalletDir(wallet string) (string, error) {
	sso, err := base64.StdEncoding.DecodeString(wallet)
	if err != nil || len(sso) == 0 {
		return "", fmt.Errorf("%w: stored wallet is not valid base64", ErrInvalidOracleWallet)
	}
	sum := sha256.Sum256(sso)
	base := filepath.Join(os.TempDir(), "weknora-oracle-wallets")
	dir := filepath.Join(base, hex.EncodeToString(sum[:16]))
	for _, d := range []string{base, dir} {
		if err := ensurePrivateDir(d); err != nil {
			return "", err
		}
	}
	target := filepath.Join(dir, oracleWalletFileName)
	if fi, err := os.Lstat(target); err == nil && fi.Mode().IsRegular() && privateToCurrentUser(fi) {
		if existing, err := os.ReadFile(target); err == nil && bytes.Equal(existing, sso) {
			return dir, nil
		}
	}
	// Write then rename so a concurrent reader never sees a partial file.
	// Rename replaces whatever entry was there (even a symlink) atomically.
	tmp, err := os.CreateTemp(dir, ".cwallet-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(sso); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return "", err
	}
	return dir, nil
}

// ensurePrivateDir creates p (mode 0700) or verifies an existing p: it must
// be a real directory (not a symlink) owned by the current user. Group/other
// permission bits on a directory we own are removed rather than refused.
func ensurePrivateDir(p string) error {
	created := true
	if err := os.Mkdir(p, 0o700); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		created = false
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("oracle wallet directory %s is not a directory", p)
	}
	if !ownedByCurrentUser(fi) {
		return fmt.Errorf("oracle wallet directory %s is not owned by the current user", p)
	}
	if created || (runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0) {
		return os.Chmod(p, 0o700)
	}
	return nil
}

// privateToCurrentUser reports whether fi is owned by the current user with
// no group/other permission bits (always true on Windows: no POSIX modes).
func privateToCurrentUser(fi fs.FileInfo) bool {
	return ownedByCurrentUser(fi) && (runtime.GOOS == "windows" || fi.Mode().Perm()&0o077 == 0)
}
