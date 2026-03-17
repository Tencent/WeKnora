package wecom

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/gin-gonic/gin"
)

// Compile-time checks.
var (
	_ im.Adapter        = (*WSAdapter)(nil)
	_ im.StreamSender   = (*WSAdapter)(nil)
	_ im.FileDownloader = (*WSAdapter)(nil)
)

// WSAdapter implements im.Adapter and im.StreamSender for WeCom in WebSocket
// (long connection) mode. It delegates to the WebSocket LongConnClient.
// The webhook methods (VerifyCallback, ParseCallback, HandleURLVerification) are no-ops
// since messages arrive via WebSocket, not HTTP.
type WSAdapter struct {
	client *LongConnClient
}

// NewWSAdapter creates an adapter backed by a WeCom long connection client.
func NewWSAdapter(client *LongConnClient) *WSAdapter {
	return &WSAdapter{client: client}
}

func (a *WSAdapter) Platform() im.Platform {
	return im.PlatformWeCom
}

func (a *WSAdapter) VerifyCallback(c *gin.Context) error {
	return fmt.Errorf("WeCom bot adapter does not support webhook callbacks")
}

func (a *WSAdapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
	return nil, fmt.Errorf("WeCom bot adapter does not support webhook callbacks")
}

func (a *WSAdapter) HandleURLVerification(c *gin.Context) bool {
	return false
}

func (a *WSAdapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	return a.client.SendReply(ctx, incoming, reply)
}

// ── StreamSender implementation ──

func (a *WSAdapter) StartStream(ctx context.Context, incoming *im.IncomingMessage) (string, error) {
	return a.client.StartStream(ctx, incoming)
}

func (a *WSAdapter) SendStreamChunk(ctx context.Context, incoming *im.IncomingMessage, streamID string, content string) error {
	return a.client.SendStreamChunk(ctx, incoming, streamID, content)
}

func (a *WSAdapter) EndStream(ctx context.Context, incoming *im.IncomingMessage, streamID string) error {
	return a.client.EndStream(ctx, incoming, streamID)
}

// ── FileDownloader implementation ──
// WeCom aibot provides AES-256-CBC encrypted URLs for image/file/video messages.
// Each message carries its own aeskey for decryption.

func (a *WSAdapter) DownloadFile(ctx context.Context, msg *im.IncomingMessage) (io.ReadCloser, string, error) {
	if msg.FileKey == "" {
		return nil, "", fmt.Errorf("no file URL in message")
	}
	fileName := msg.FileName
	if fileName == "" {
		fileName = msg.FileKey
	}

	// Download the (encrypted) file content
	reader, fileName, err := downloadFromURL(ctx, msg.FileKey, fileName)
	if err != nil {
		return nil, "", err
	}

	// If an AES key is provided, the downloaded content is AES-256-CBC encrypted
	// and must be decrypted before use. This is the case for WeCom aibot long
	// connection mode where each file/image message carries a per-message aeskey.
	aesKeyB64 := msg.Extra["aes_key"]
	if aesKeyB64 == "" {
		// No encryption — return raw content (e.g. webhook mode uses media API)
		return reader, fileName, nil
	}

	// Read all encrypted content
	encryptedData, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		return nil, "", fmt.Errorf("read encrypted file: %w", err)
	}

	logger.Debugf(ctx, "[WeCom] Decrypting file: name=%s encrypted_size=%d aes_key_len=%d",
		fileName, len(encryptedData), len(aesKeyB64))

	// Decrypt
	decrypted, err := decryptAESCBC(encryptedData, aesKeyB64)
	if err != nil {
		return nil, "", fmt.Errorf("decrypt file: %w", err)
	}

	logger.Debugf(ctx, "[WeCom] File decrypted: name=%s decrypted_size=%d", fileName, len(decrypted))

	return io.NopCloser(bytes.NewReader(decrypted)), fileName, nil
}

// decryptAESCBC decrypts data encrypted with AES-256-CBC using PKCS#7 padding.
// The aesKeyB64 is the base64-encoded AES key provided per-message by WeCom.
// IV is the first 16 bytes of the decoded AES key.
func decryptAESCBC(ciphertext []byte, aesKeyB64 string) ([]byte, error) {
	// WeCom's per-message aeskey is base64-encoded (43 chars → 32 bytes after decode)
	aesKey, err := base64.StdEncoding.DecodeString(aesKeyB64 + "=")
	if err != nil {
		// Try without padding
		aesKey, err = base64.RawStdEncoding.DecodeString(aesKeyB64)
		if err != nil {
			return nil, fmt.Errorf("base64 decode aes key: %w", err)
		}
	}

	if len(aesKey) < 16 {
		return nil, fmt.Errorf("aes key too short: %d bytes", len(aesKey))
	}

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf("new aes cipher: %w", err)
	}

	if len(ciphertext) < aes.BlockSize {
		return nil, fmt.Errorf("ciphertext too short: %d bytes", len(ciphertext))
	}
	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext not a multiple of block size: %d bytes", len(ciphertext))
	}

	// IV = first 16 bytes of the AES key
	iv := aesKey[:aes.BlockSize]
	mode := cipher.NewCBCDecrypter(block, iv)
	plaintext := make([]byte, len(ciphertext))
	mode.CryptBlocks(plaintext, ciphertext)

	// Remove PKCS#7 padding
	if len(plaintext) == 0 {
		return nil, fmt.Errorf("empty plaintext after decryption")
	}
	padLen := int(plaintext[len(plaintext)-1])
	if padLen > aes.BlockSize || padLen == 0 || padLen > len(plaintext) {
		// No valid PKCS#7 padding — return as-is (some implementations may not pad)
		return plaintext, nil
	}
	// Verify padding bytes
	for i := 0; i < padLen; i++ {
		if plaintext[len(plaintext)-1-i] != byte(padLen) {
			// Invalid padding — return as-is
			return plaintext, nil
		}
	}
	return plaintext[:len(plaintext)-padLen], nil
}
