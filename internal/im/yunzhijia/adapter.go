package yunzhijia

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
)

// textMessageType is the Yunzhijia message type value for plain text messages.
const textMessageType = 2

// markdownFormatType requests Yunzhijia to render Content as Markdown.
const markdownFormatType = "markdown"

// Compile-time check.
var _ im.Adapter = (*Adapter)(nil)
var _ im.FileDownloader = (*Adapter)(nil)

var yunzhijiaAuthURL = "https://yunzhijia.com/api/oauth2_v12/auth/getAppAccessToken"
var yunzhijiaDownloadFileBaseURL = "https://yunzhijia.com/gateway/docrest/doc/file/downloadfileOpen"

var validateDownloadFileURL = func(rawURL string) error {
	_, err := validateEndpointURL(rawURL, "https", "yunzhijia.com")
	return err
}

// maxDownloadFileSize caps the size of a file downloaded from Yunzhijia and read
// into memory, to avoid unbounded memory usage from a large/malicious response.
const maxDownloadFileSize = 32 << 20 // 32 MiB

// maxDownloadRedirects limits how many redirects DownloadFile follows manually.
// The shared httpClient disables automatic redirects (SSRF safety), but the
// Yunzhijia download endpoint may 302 to a signed URL, so we follow a single hop
// while re-validating the target host stays within the allowed suffix.
const maxDownloadRedirects = 1

// Adapter implements im.Adapter for Yunzhijia (云之家).
type Adapter struct {
	sendMsgURL               string
	secret                   string
	appID                    string
	appSecret                string
	httpClient               *http.Client
	allowedWebhookHostSuffix string
	tokenMu                  sync.Mutex
	accessToken              string
	accessTokenExpiresAt     time.Time
}

// NewAdapter creates a Yunzhijia adapter.
func NewAdapter(sendMsgURL, secret, appID, appSecret string, timeoutSeconds int, allowedHostSuffix string) *Adapter {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 10
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = safeDialContext
	return &Adapter{
		sendMsgURL:               strings.TrimSpace(sendMsgURL),
		secret:                   strings.TrimSpace(secret),
		appID:                    strings.TrimSpace(appID),
		appSecret:                strings.TrimSpace(appSecret),
		allowedWebhookHostSuffix: strings.TrimSpace(allowedHostSuffix),
		httpClient: &http.Client{
			Timeout:   time.Duration(timeoutSeconds) * time.Second,
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (a *Adapter) Platform() im.Platform {
	return im.PlatformYunzhijia
}

func (a *Adapter) HandleURLVerification(c *gin.Context) bool {
	return false
}

// VerifyCallback verifies the Yunzhijia callback signature (HmacSHA1).
// If secret is not configured, verification is skipped.
func (a *Adapter) VerifyCallback(c *gin.Context) error {
	if a.secret == "" {
		return nil
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	var msg callbackMessage
	if err := json.Unmarshal(bodyBytes, &msg); err != nil {
		return fmt.Errorf("parse callback for verification: %w", err)
	}

	// Read the sign header (case-insensitive via gin's GetHeader).
	sign := c.GetHeader("sign")
	if sign == "" {
		sign = c.GetHeader("Sign")
	}
	if sign == "" {
		sign = c.GetHeader("SIGN")
	}
	if sign == "" {
		return fmt.Errorf("missing sign header")
	}

	expected := computeSignature(a.secret, &msg)
	if !hmac.Equal([]byte(sign), []byte(expected)) {
		return fmt.Errorf("invalid signature")
	}

	return nil
}

// ParseCallback parses a Yunzhijia webhook callback into an IncomingMessage.
// Returns nil for non-text messages or empty content.
func (a *Adapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	var msg callbackMessage
	if err := json.Unmarshal(bodyBytes, &msg); err != nil {
		return nil, fmt.Errorf("parse callback: %w", err)
	}

	return toIncomingMessage(c.Request.Context(), &msg), nil
}

func toIncomingMessage(ctx context.Context, msg *callbackMessage) *im.IncomingMessage {
	if msg.Type != textMessageType {
		logger.Infof(ctx,
			"[Yunzhijia] Skip non-text message: type=%d msgId=%s", msg.Type, msg.MsgID)
		return nil
	}

	param, err := parseMessageParam(msg.MsgParam)
	if err != nil {
		logger.Warnf(ctx, "[Yunzhijia] Failed to parse msgParam: msgId=%s err=%v", msg.MsgID, err)
	}
	image, hasImage := param.firstImage()

	content := strings.TrimSpace(msg.Content)
	if content == "" && !hasImage {
		logger.Infof(ctx,
			"[Yunzhijia] Skip empty content: msgId=%s", msg.MsgID)
		return nil
	}

	// Conversation bots should only receive messages explicitly addressed to them.
	var mentioned bool
	content, mentioned = cleanAtMention(content, msg.RobotName)
	if !mentioned {
		mentioned = messageParamMentionsRobot(param, msg.RobotID)
	}
	if !mentioned {
		logger.Infof(ctx, "[Yunzhijia] Skip message without robot mention: msgId=%s", msg.MsgID)
		return nil
	}

	if content == "" && !hasImage {
		logger.Infof(ctx,
			"[Yunzhijia] Skip after cleaning @mention: msgId=%s", msg.MsgID)
		return nil
	}

	userID := firstNonEmpty(msg.OperatorOpenid, msg.OperatorOID, msg.OpenID, msg.SenderID, msg.OperatorID, msg.OperatorUserID)
	userName := firstNonEmpty(msg.OperatorName, msg.SenderName)
	chatType := im.ChatTypeGroup
	chatID := firstNonEmpty(msg.GroupID, msg.RobotID)

	incoming := &im.IncomingMessage{
		Platform:    im.PlatformYunzhijia,
		MessageType: im.MessageTypeText,
		UserID:      userID,
		UserName:    userName,
		ChatID:      chatID,
		ChatType:    chatType,
		Content:     content,
		MessageID:   msg.MsgID,
		Extra: map[string]string{
			"robot_id":      msg.RobotID,
			"robot_name":    msg.RobotName,
			"group_id":      msg.GroupID,
			"group_type":    fmt.Sprintf("%d", msg.GroupType),
			"operator_name": userName,
			"time":          fmt.Sprintf("%d", msg.Time),
		},
	}
	if hasImage {
		incoming.MessageType = im.MessageTypeImage
		incoming.FileKey = image.Data
		incoming.FileName = defaultImageFileName(msg.MsgID)
		incoming.Extra["yunzhijia_image_width"] = fmt.Sprintf("%d", image.Width)
		incoming.Extra["yunzhijia_image_height"] = fmt.Sprintf("%d", image.Height)
	}
	return incoming
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func messageParamMentionsRobot(param *messageParam, robotID string) bool {
	if param == nil || robotID == "" {
		return false
	}
	for _, notifyTo := range param.NotifyTo {
		if notifyTo == robotID {
			return true
		}
	}
	for _, desc := range param.Desc {
		if desc.Type == "at" && desc.Data == robotID {
			return true
		}
	}
	return false
}

func defaultImageFileName(msgID string) string {
	if msgID == "" {
		return "yunzhijia-image.png"
	}
	return msgID + ".png"
}

// cleanAtMention removes @RobotName from the beginning of user content.
func cleanAtMention(content, robotName string) (string, bool) {
	if robotName == "" {
		return content, false
	}
	prefix := "@" + robotName
	trimmed := strings.TrimLeft(content, " \t")
	if !strings.HasPrefix(trimmed, prefix) {
		return content, false
	}
	rest := trimmed[len(prefix):]
	if rest == "" {
		return "", true
	}
	separator, _ := utf8.DecodeRuneInString(rest)
	if !unicode.IsSpace(separator) && !strings.ContainsRune(":：,，", separator) {
		return content, false
	}
	return strings.TrimLeftFunc(rest, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(":：,，", r)
	}), true
}

// SendReply sends a reply to Yunzhijia via the configured sendMsgUrl.
func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	if a.sendMsgURL == "" {
		return fmt.Errorf("yunzhijia send_msg_url is not configured")
	}

	// Validate the send URL to prevent SSRF.
	if err := a.validateSendURL(); err != nil {
		return err
	}

	payload := sendMessagePayload{
		MsgType: textMessageType,
		Content: reply.Content,
		// WeKnora replies are authored in Markdown by default (see im.ReplyMessage.Content),
		// so request Markdown rendering from Yunzhijia unless explicitly overridden via
		// reply.Extra["yunzhijia_format_type"] (empty string disables the param entirely).
		Param: &sendMessageParam{FormatType: markdownFormatType},
	}
	if reply.Extra != nil {
		if formatType, ok := reply.Extra["yunzhijia_format_type"]; ok {
			if formatType == "" {
				payload.Param = nil
			} else {
				payload.Param = &sendMessageParam{FormatType: formatType}
			}
		}
	}

	// When groupType == 3, don't set notifyParams (per reference implementation).
	groupType := ""
	if incoming.Extra != nil {
		groupType = incoming.Extra["group_type"]
	}
	if groupType != "3" && incoming.UserID != "" {
		payload.NotifyParams = []notifyParam{
			{
				Type:   "openIds",
				Values: []string{incoming.UserID},
			},
		}
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal reply: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.sendMsgURL, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send reply: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("yunzhijia sendMsgUrl returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// DownloadFile downloads a file/image resource sent by Yunzhijia.
func (a *Adapter) DownloadFile(ctx context.Context, msg *im.IncomingMessage) (io.ReadCloser, string, error) {
	if msg.FileKey == "" {
		return nil, "", fmt.Errorf("yunzhijia file id is required")
	}
	fileID := strings.TrimSpace(msg.FileKey)
	if err := validateFileID(fileID); err != nil {
		return nil, "", fmt.Errorf("invalid yunzhijia file id: %w", err)
	}

	accessToken, err := a.getAppAccessToken(ctx)
	if err != nil {
		return nil, "", err
	}

	downloadURL := buildDownloadFileURL(fileID)
	if err := validateDownloadFileURL(downloadURL); err != nil {
		return nil, "", fmt.Errorf("download url rejected: %w", err)
	}

	resp, err := a.fetchDownload(ctx, downloadURL, accessToken)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, "", fmt.Errorf("download file returned %d: %s", resp.StatusCode, string(body))
	}

	fileName := msg.FileName
	if fileName == "" {
		fileName = fileID
	}
	fileName = resolveDownloadFileName(fileName, resp)
	return newLimitedReadCloser(resp.Body, maxDownloadFileSize), fileName, nil
}

// fetchDownload performs the download request, following at most maxDownloadRedirects
// redirects. Each redirect target is re-validated against the allowed host suffix so
// the request cannot be redirected off the trusted domain. The bearer token is only
// sent on the initial request and never forwarded across a redirect.
func (a *Adapter) fetchDownload(ctx context.Context, downloadURL, accessToken string) (*http.Response, error) {
	currentURL := downloadURL
	for redirects := 0; ; redirects++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, currentURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create download request: %w", err)
		}
		if redirects == 0 {
			req.Header.Set("Authorization", "Bearer "+accessToken)
		}
		resp, err := a.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("download file: %w", err)
		}
		if resp.StatusCode < http.StatusMultipleChoices || resp.StatusCode >= http.StatusBadRequest {
			return resp, nil
		}

		// Redirect (3xx): re-validate the target before following.
		location := strings.TrimSpace(resp.Header.Get("Location"))
		resp.Body.Close()
		if redirects >= maxDownloadRedirects {
			return nil, fmt.Errorf("download file: too many redirects")
		}
		if location == "" {
			return nil, fmt.Errorf("download file: redirect without Location")
		}
		resolved, err := resolveRedirectURL(currentURL, location)
		if err != nil {
			return nil, fmt.Errorf("download file: invalid redirect location: %w", err)
		}
		if err := validateDownloadFileURL(resolved); err != nil {
			return nil, fmt.Errorf("download redirect rejected: %w", err)
		}
		currentURL = resolved
	}
}

func resolveRedirectURL(base, location string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	locURL, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	return baseURL.ResolveReference(locURL).String(), nil
}

// newLimitedReadCloser wraps rc so that reading more than limit bytes returns an
// error instead of silently truncating, while Close still closes the underlying body.
func newLimitedReadCloser(rc io.ReadCloser, limit int64) io.ReadCloser {
	return &limitedReadCloser{r: io.LimitReader(rc, limit+1), body: rc, limit: limit}
}

type limitedReadCloser struct {
	r     io.Reader
	body  io.Closer
	limit int64
	read  int64
}

func (l *limitedReadCloser) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.read += int64(n)
	if l.read > l.limit {
		return n, fmt.Errorf("yunzhijia file exceeds max download size of %d bytes", l.limit)
	}
	return n, err
}

func (l *limitedReadCloser) Close() error {
	return l.body.Close()
}

func buildDownloadFileURL(fileID string) string {
	u, _ := url.Parse(yunzhijiaDownloadFileBaseURL)
	query := url.Values{}
	query.Set("fileId", fileID)
	u.RawQuery = query.Encode()
	return u.String()
}

func validateFileID(fileID string) error {
	if fileID == "" {
		return fmt.Errorf("empty")
	}
	if len(fileID) > 256 {
		return fmt.Errorf("too long")
	}
	if strings.ContainsAny(fileID, `/\?#&`) {
		return fmt.Errorf("contains path or query separators")
	}
	for _, r := range fileID {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return fmt.Errorf("contains whitespace or control character")
		}
	}
	return nil
}

func (a *Adapter) getAppAccessToken(ctx context.Context) (string, error) {
	if a.appID == "" || a.appSecret == "" {
		return "", fmt.Errorf("yunzhijia app_id and app_secret are required to download files")
	}

	a.tokenMu.Lock()
	if a.accessToken != "" && time.Now().Before(a.accessTokenExpiresAt) {
		token := a.accessToken
		a.tokenMu.Unlock()
		return token, nil
	}
	a.tokenMu.Unlock()

	payload := map[string]any{
		"appId":     a.appID,
		"secret":    a.appSecret,
		"timestamp": time.Now().UnixMilli(),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal yunzhijia token request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, yunzhijiaAuthURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create yunzhijia token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request yunzhijia app access token: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read yunzhijia token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("yunzhijia token endpoint returned %d: %s", resp.StatusCode, string(respBody))
	}

	var tokenResp appAccessTokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return "", fmt.Errorf("parse yunzhijia token response: %w", err)
	}
	if !tokenResp.Success || tokenResp.ErrorCode != 0 || tokenResp.Data.AccessToken == "" {
		return "", fmt.Errorf("yunzhijia token response failed: errorCode=%d error=%v", tokenResp.ErrorCode, tokenResp.Error)
	}

	expiresIn := tokenResp.Data.ExpireIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	expiresAt := time.Now().Add(time.Duration(expiresIn) * time.Second)
	if expiresIn > 120 {
		expiresAt = expiresAt.Add(-60 * time.Second)
	}

	a.tokenMu.Lock()
	a.accessToken = tokenResp.Data.AccessToken
	a.accessTokenExpiresAt = expiresAt
	a.tokenMu.Unlock()
	return tokenResp.Data.AccessToken, nil
}

func resolveDownloadFileName(fallback string, resp *http.Response) string {
	fileName := fallback
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			if name := strings.TrimSpace(params["filename"]); name != "" {
				fileName = name
			}
		}
	}
	if filepath.Ext(fileName) == "" {
		switch strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])) {
		case "image/jpeg":
			fileName += ".jpg"
		case "image/png":
			fileName += ".png"
		case "image/gif":
			fileName += ".gif"
		}
	}
	return fileName
}

// validateSendURL checks that sendMsgUrl is safe to call (HTTPS, no internal IPs, allowed host).
func (a *Adapter) validateSendURL() error {
	_, err := validateEndpointURL(a.sendMsgURL, "https", a.allowedWebhookHostSuffix)
	if err != nil {
		return fmt.Errorf("invalid send_msg_url: %w", err)
	}
	return nil
}
