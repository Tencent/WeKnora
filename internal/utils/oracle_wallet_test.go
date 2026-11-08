package utils

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func walletZip(t *testing.T, files map[string][]byte, symlink string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if name == symlink {
			h.SetMode(fs.ModeSymlink | 0o777)
		}
		w, err := zw.CreateHeader(h)
		require.NoError(t, err)
		_, err = w.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestValidateSSORejectsMalformedWithoutPanic(t *testing.T) {
	for _, in := range [][]byte{{1}, []byte("not a wallet"), append([]byte{161, 248, 78, 54}, make([]byte, 8)...)} {
		err := validateSSO(in)
		assert.ErrorIs(t, err, ErrInvalidOracleWallet)
	}
}

func TestNormalizeOracleWallet(t *testing.T) {
	sso := []byte("fake-sso-content")
	defer SetOracleWalletValidatorForTest(func(b []byte) error {
		if !bytes.Equal(b, sso) {
			return ErrInvalidOracleWallet
		}
		return nil
	})()

	got, err := NormalizeOracleWallet(b64(sso))
	require.NoError(t, err, "bare cwallet.sso")
	assert.Equal(t, b64(sso), got)

	zipped := walletZip(t, map[string][]byte{
		"Wallet_db/cwallet.sso": sso, "Wallet_db/tnsnames.ora": []byte("db_high = (...)"), "README": nil,
	}, "")
	got, err = NormalizeOracleWallet(b64(zipped))
	require.NoError(t, err, "wallet ZIP")
	assert.Equal(t, b64(sso), got, "only cwallet.sso is kept")

	for name, upload := range map[string]string{
		"not base64":      "%%%",
		"empty":           "",
		"too large":       b64(make([]byte, maxOracleWalletUploadBytes+1)),
		"zip without sso": b64(walletZip(t, map[string][]byte{"tnsnames.ora": nil}, "")),
		"symlink member":  b64(walletZip(t, map[string][]byte{"cwallet.sso": []byte("/etc/passwd")}, "cwallet.sso")),
		"zip bomb member": b64(walletZip(t,
			map[string][]byte{"cwallet.sso": make([]byte, maxOracleWalletSSOBytes+1)}, "")),
		"wrong content": b64([]byte("something else")),
	} {
		_, err := NormalizeOracleWallet(upload)
		assert.ErrorIs(t, err, ErrInvalidOracleWallet, name)
	}
}

func TestOracleWalletDir(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	dir, err := OracleWalletDir(b64([]byte("wallet-a")))
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(dir, "cwallet.sso"))
	require.NoError(t, err)
	assert.Equal(t, "wallet-a", string(data))

	st, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o700), st.Mode().Perm())
	st, err = os.Stat(filepath.Join(dir, "cwallet.sso"))
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o600), st.Mode().Perm())

	again, err := OracleWalletDir(b64([]byte("wallet-a")))
	require.NoError(t, err)
	assert.Equal(t, dir, again, "same content reuses the directory")

	rotated, err := OracleWalletDir(b64([]byte("wallet-b")))
	require.NoError(t, err)
	assert.NotEqual(t, dir, rotated, "a rotated wallet gets its own directory")

	_, err = OracleWalletDir("%%%")
	assert.ErrorIs(t, err, ErrInvalidOracleWallet)
	assert.True(t, strings.HasPrefix(dir, os.TempDir()))
}
