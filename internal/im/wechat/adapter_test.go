package wechat

import (
	"bytes"
	"context"
	"crypto/aes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/im"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// A WeChat attachment may be up to maxIMAttachmentBytes, which is larger than
// the callback-body cap. The encrypted download has to be bounded separately;
// otherwise a valid attachment between the two limits is rejected.
func TestDownloadFileAcceptsEncryptedAttachmentAboveCallbackCap(t *testing.T) {
	secutils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() { secutils.SetSSRFWhitelistFromRaw("") })

	const plaintextSize = 5 << 20
	plaintext := bytes.Repeat([]byte("a"), plaintextSize)
	key := []byte("0123456789abcdef")
	ciphertext := encryptAES128ECB(t, plaintext, key)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write(ciphertext); err != nil {
			t.Errorf("write ciphertext: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	adapter := &Adapter{}
	reader, fileName, err := adapter.DownloadFile(context.Background(), &im.IncomingMessage{
		FileKey:  server.URL,
		FileName: "large.pdf",
		FileSize: plaintextSize,
		Extra:    map[string]string{"aes_key": base64.StdEncoding.EncodeToString(key)},
	})
	if err != nil {
		t.Fatalf("DownloadFile() = %v, want nil for a %d byte attachment", err, plaintextSize)
	}
	defer func() { _ = reader.Close() }()

	if fileName != "large.pdf" {
		t.Errorf("fileName = %q, want %q", fileName, "large.pdf")
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() = %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("decrypted attachment differs from the %d byte plaintext", plaintextSize)
	}
}

func encryptAES128ECB(t *testing.T, plaintext, key []byte) []byte {
	t.Helper()

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher() = %v", err)
	}
	paddedSize := (len(plaintext)/block.BlockSize() + 1) * block.BlockSize()
	padded := make([]byte, paddedSize)
	copy(padded, plaintext)
	for i := len(plaintext); i < paddedSize; i++ {
		padded[i] = byte(paddedSize - len(plaintext))
	}

	ciphertext := make([]byte, paddedSize)
	for i := 0; i < paddedSize; i += block.BlockSize() {
		block.Encrypt(ciphertext[i:i+block.BlockSize()], padded[i:i+block.BlockSize()])
	}
	return ciphertext
}
