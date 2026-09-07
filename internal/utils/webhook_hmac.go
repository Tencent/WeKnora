package utils

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

const webhookSignaturePrefix = "sha256="

// SignWebhookTimestampBody signs timestamp + "." + raw body with HMAC-SHA256.
// Do not reuse SignEmbedWebhookBody (body-only) for workspace webhooks.
func SignWebhookTimestampBody(secret string, unixTs int64, raw []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(unixTs, 10)))
	mac.Write([]byte("."))
	mac.Write(raw)
	return webhookSignaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// VerifyWebhookTimestampBody checks signature with constant-time compare and a
// ±window clock skew. window <= 0 defaults to 300 seconds.
func VerifyWebhookTimestampBody(secret, headerSig, headerTs string, raw []byte, now time.Time, window time.Duration) error {
	if window <= 0 {
		window = 300 * time.Second
	}
	ts, err := strconv.ParseInt(headerTs, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp")
	}
	if now.IsZero() {
		now = time.Now()
	}
	delta := now.Unix() - ts
	if delta < 0 {
		delta = -delta
	}
	if delta > int64(window.Seconds()) {
		return fmt.Errorf("stale timestamp")
	}
	expected := SignWebhookTimestampBody(secret, ts, raw)
	if !hmac.Equal([]byte(headerSig), []byte(expected)) {
		return fmt.Errorf("invalid signature")
	}
	return nil
}
