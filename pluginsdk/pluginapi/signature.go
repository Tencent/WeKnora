package pluginapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

// MaxClockSkew bounds how old a signed request may be.
const MaxClockSkew = 5 * time.Minute

// Sign returns the signature of a request body at a unix timestamp.
func Sign(secret []byte, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "%d.", timestamp)
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature checks a signed request against the secret and clock.
func VerifySignature(secret []byte, timestamp, signature string, body []byte, now time.Time) error {
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("bad %s", TimestampHeader)
	}
	if d := now.Sub(time.Unix(ts, 0)); d > MaxClockSkew || d < -MaxClockSkew {
		return fmt.Errorf("request timestamp is outside the allowed clock skew")
	}
	want := Sign(secret, ts, body)
	if !hmac.Equal([]byte(want), []byte(signature)) {
		return fmt.Errorf("bad signature")
	}
	return nil
}
