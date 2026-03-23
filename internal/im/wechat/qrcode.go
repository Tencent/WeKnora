// QR code login flow for WeChat iLink Bot API.
//
// API endpoints (all relative to the iLink base URL):
//   GET  /ilink/bot/get_bot_qrcode?bot_type=3   → returns {qrcode, qrcode_img_content}
//   GET  /ilink/bot/get_qrcode_status?qrcode=xxx → returns {status, bot_token, ilink_bot_id, ...}
//
// Flow:
//  1. Call GetLoginQRCode to obtain a QR code URL and opaque qrcode token
//  2. Display qrcode_img_content URL to user (in frontend)
//  3. Long-poll PollQRCodeStatus until user scans and confirms
//  4. On success, receive bot_token + ilink_bot_id + ilink_user_id + baseurl
package wechat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// QRCodeResult holds the result of requesting a login QR code.
type QRCodeResult struct {
	// QRCodeURL is the URL to render as a QR code image (qrcode_img_content).
	QRCodeURL string `json:"qrcode_url"`
	// QRCode is the opaque token used to poll for scan status.
	QRCode string `json:"qrcode"`
}

// LoginResult holds the credentials returned after successful QR code scan.
type LoginResult struct {
	Status      string `json:"status"`        // "wait", "scaned", "confirmed", "expired"
	BotToken    string `json:"bot_token"`     // Bearer token for API calls
	ILinkBotID  string `json:"ilink_bot_id"`  // Bot identifier
	ILinkUserID string `json:"ilink_user_id"` // User identifier
	BaseURL     string `json:"baseurl"`       // API base URL (may override default)
}

// QRCodeService handles WeChat QR code login operations.
type QRCodeService struct {
	client *http.Client
}

// NewQRCodeService creates a new QR code service.
func NewQRCodeService() *QRCodeService {
	return &QRCodeService{
		client: &http.Client{Timeout: pollTimeout},
	}
}

// GetLoginQRCode requests a new login QR code from iLink API.
// GET /ilink/bot/get_bot_qrcode?bot_type=3
func (s *QRCodeService) GetLoginQRCode(ctx context.Context) (*QRCodeResult, error) {
	u := ilinkBaseURL + "/ilink/bot/get_bot_qrcode?bot_type=" + url.QueryEscape(defaultBotType)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request qrcode: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qrcode API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		QRCode          string `json:"qrcode"`
		QRCodeImgContent string `json:"qrcode_img_content"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, string(body))
	}

	if result.QRCode == "" {
		return nil, fmt.Errorf("empty qrcode in response: %s", string(body))
	}

	return &QRCodeResult{
		QRCodeURL: result.QRCodeImgContent,
		QRCode:    result.QRCode,
	}, nil
}

// pollTimeout is the client-side timeout for the long-poll get_qrcode_status
// request. The iLink server may hold the request up to 35s, so we set a
// slightly longer timeout. We use a DETACHED context (not the gin request
// context) to avoid "context canceled" when gin's own timeout fires first.
const pollTimeout = 38 * time.Second

// PollQRCodeStatus checks the scan status of a QR code.
// GET /ilink/bot/get_qrcode_status?qrcode=xxx
// This is a long-poll endpoint: the server holds the connection until there
// is a status change or ~35 seconds elapse.
// Status values: "wait", "scaned", "confirmed", "expired"
func (s *QRCodeService) PollQRCodeStatus(ctx context.Context, qrcode string) (*LoginResult, error) {
	u := ilinkBaseURL + "/ilink/bot/get_qrcode_status?qrcode=" + url.QueryEscape(qrcode)

	// Use a DETACHED context with our own timeout so we are not bound by the
	// caller's (gin) request context which may be shorter than the iLink
	// long-poll hold time.
	pollCtx, cancel := context.WithTimeout(context.Background(), pollTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(pollCtx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("iLink-App-ClientVersion", "1")

	resp, err := s.client.Do(req)
	if err != nil {
		// Client-side timeout is normal for long-poll; return "wait" status
		if pollCtx.Err() != nil {
			return &LoginResult{Status: "wait"}, nil
		}
		return nil, fmt.Errorf("request qrcode status: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qrcode status API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status      string `json:"status"` // "wait", "scaned", "confirmed", "expired"
		BotToken    string `json:"bot_token"`
		ILinkBotID  string `json:"ilink_bot_id"`
		ILinkUserID string `json:"ilink_user_id"`
		BaseURL     string `json:"baseurl"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, string(body))
	}

	return &LoginResult{
		Status:      result.Status,
		BotToken:    result.BotToken,
		ILinkBotID:  result.ILinkBotID,
		ILinkUserID: result.ILinkUserID,
		BaseURL:     result.BaseURL,
	}, nil
}
