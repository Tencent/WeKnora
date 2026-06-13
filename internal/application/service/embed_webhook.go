package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

const embedWebhookTimeout = 5 * time.Second

// ErrEmbedWebhookURLInvalid is returned when a webhook URL fails format or SSRF checks.
var ErrEmbedWebhookURLInvalid = errors.New("invalid embed webhook URL")

// ValidateEmbedWebhookURL checks an optional outbound webhook URL. Empty is allowed.
func ValidateEmbedWebhookURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%w: webhook URL must be a valid http(s) URL", ErrEmbedWebhookURLInvalid)
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("%w: webhook URL must use http or https", ErrEmbedWebhookURLInvalid)
	}
	if err := secutils.ValidateURLForSSRF(trimmed); err != nil {
		if hint := secutils.FormatSSRFError("Webhook URL", trimmed, err); hint != "" {
			return fmt.Errorf("%w: %s", ErrEmbedWebhookURLInvalid, hint)
		}
		return fmt.Errorf("%w: %v", ErrEmbedWebhookURLInvalid, err)
	}
	return nil
}

// DispatchEmbedWebhook POSTs an event to the channel webhook URL (best-effort, async).
func DispatchEmbedWebhook(ch *types.EmbedChannel, eventType, sessionID string, payload map[string]any) {
	if ch == nil {
		return
	}
	url := strings.TrimSpace(ch.WebhookURL)
	if url == "" {
		return
	}
	if err := ValidateEmbedWebhookURL(url); err != nil {
		logger.Warnf(context.Background(), "[embed_webhook] skip dispatch %s: %v", eventType, err)
		return
	}
	body := map[string]any{
		"type":       eventType,
		"channel_id": ch.ID,
		"session_id": sessionID,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	}
	for k, v := range payload {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return
	}
	secret := strings.TrimSpace(ch.WebhookSecret)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), embedWebhookTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "WeKnora-Embed-Webhook/1.0")
		if secret != "" {
			mac := hmac.New(sha256.New, []byte(secret))
			_, _ = mac.Write(raw)
			req.Header.Set("X-WeKnora-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			logger.Warnf(context.Background(), "[embed_webhook] dispatch %s failed: %v", eventType, err)
			return
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode >= 300 {
			logger.Warnf(context.Background(), "[embed_webhook] dispatch %s HTTP %d", eventType, resp.StatusCode)
		}
	}()
}

// SignEmbedWebhookBody returns the hex HMAC signature for tests.
func SignEmbedWebhookBody(secret string, raw []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}
