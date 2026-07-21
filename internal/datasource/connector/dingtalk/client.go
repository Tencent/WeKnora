package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
)

const (
	dingtalkMaxAttempts     = 2
	dingtalkMaxDownloadSize = 100 * 1024 * 1024
)

type client struct {
	cfg        *Config
	httpClient *http.Client

	tokenMu sync.Mutex
	token   string

	unionMu sync.Mutex
	unionID string
}

func newClient(cfg *Config) *client {
	return &client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *client) accessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.token != "" {
		return c.token, nil
	}

	body := map[string]string{
		"appKey":    c.cfg.AppKey,
		"appSecret": c.cfg.AppSecret,
	}
	var resp accessTokenResponse
	if err := c.postJSON(ctx, c.cfg.GetBaseURL(), "/v1.0/oauth2/accessToken", body, &resp); err != nil {
		return "", err
	}
	if resp.ErrCode != 0 {
		return "", fmt.Errorf("%w: dingtalk access token error %d: %s",
			datasource.ErrInvalidCredentials, resp.ErrCode, resp.ErrMsg)
	}
	if resp.Code != "" && resp.Code != "0" {
		return "", fmt.Errorf("%w: dingtalk access token error %s: %s",
			datasource.ErrInvalidCredentials, resp.Code, resp.Message)
	}
	if strings.TrimSpace(resp.AccessToken) == "" {
		return "", fmt.Errorf("%w: dingtalk access token is empty", datasource.ErrInvalidCredentials)
	}

	c.token = resp.AccessToken
	return c.token, nil
}

func (c *client) operatorUnionID(ctx context.Context) (string, error) {
	if strings.TrimSpace(c.cfg.OperatorUnionID) != "" {
		return strings.TrimSpace(c.cfg.OperatorUnionID), nil
	}

	c.unionMu.Lock()
	defer c.unionMu.Unlock()

	if c.unionID != "" {
		return c.unionID, nil
	}

	token, err := c.accessToken(ctx)
	if err != nil {
		return "", err
	}

	form := url.Values{}
	form.Set("access_token", token)
	form.Set("userid", c.cfg.OperatorUserID)
	form.Set("language", "zh_CN")

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.cfg.GetOAPIBaseURL()+"/topapi/v2/user/get",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var resp userDetailResponse
	if err := c.do(req, &resp); err != nil {
		return "", err
	}
	if resp.ErrCode != 0 {
		return "", fmt.Errorf("%w: dingtalk user detail error %d: %s",
			datasource.ErrInvalidCredentials, resp.ErrCode, resp.ErrMsg)
	}
	if strings.TrimSpace(resp.Result.UnionID) == "" {
		return "", fmt.Errorf("%w: dingtalk user detail returned empty unionid", datasource.ErrInvalidCredentials)
	}

	c.unionID = strings.TrimSpace(resp.Result.UnionID)
	return c.unionID, nil
}

func (c *client) ListWorkspaces(ctx context.Context) ([]workspace, error) {
	operatorID, err := c.operatorUnionID(ctx)
	if err != nil {
		return nil, err
	}

	var out []workspace
	nextToken := ""
	for {
		query := url.Values{}
		query.Set("operatorId", operatorID)
		query.Set("maxResults", "30")
		if nextToken != "" {
			query.Set("nextToken", nextToken)
		}

		var resp workspaceListResponse
		if err := c.getAPI(ctx, "/v2.0/wiki/workspaces", query, &resp); err != nil {
			return nil, fmt.Errorf("list dingtalk workspaces: %w", err)
		}
		items, next, hasMore := resp.items()
		out = append(out, items...)
		if !hasMore || next == "" {
			break
		}
		nextToken = next
	}
	return out, nil
}

func (c *client) ListNodes(ctx context.Context, parentNodeID string) ([]wikiNode, error) {
	operatorID, err := c.operatorUnionID(ctx)
	if err != nil {
		return nil, err
	}

	var out []wikiNode
	nextToken := ""
	for {
		query := url.Values{}
		query.Set("operatorId", operatorID)
		query.Set("parentNodeId", parentNodeID)
		query.Set("maxResults", "50")
		if nextToken != "" {
			query.Set("nextToken", nextToken)
		}

		var resp nodeListResponse
		if err := c.getAPI(ctx, "/v2.0/wiki/nodes", query, &resp); err != nil {
			return nil, fmt.Errorf("list dingtalk nodes under %s: %w", parentNodeID, err)
		}
		items, next, hasMore := resp.items()
		out = append(out, items...)
		if !hasMore || next == "" {
			break
		}
		nextToken = next
	}
	return out, nil
}

func (c *client) GetNode(ctx context.Context, nodeID string) (wikiNode, error) {
	operatorID, err := c.operatorUnionID(ctx)
	if err != nil {
		return wikiNode{}, err
	}

	query := url.Values{}
	query.Set("operatorId", operatorID)
	var resp nodeDetailResponse
	if err := c.getAPI(ctx, "/v2.0/wiki/nodes/"+url.PathEscape(nodeID), query, &resp); err != nil {
		return wikiNode{}, fmt.Errorf("get dingtalk node %s: %w", nodeID, err)
	}
	node := resp.item()
	if node.NodeID == "" {
		return wikiNode{}, fmt.Errorf("get dingtalk node %s: empty response", nodeID)
	}
	return node, nil
}

func (c *client) QueryDocBlocks(ctx context.Context, docKey string) ([]docBlock, error) {
	operatorID, err := c.operatorUnionID(ctx)
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	query.Set("operatorId", operatorID)
	var resp blockListResponse
	if err := c.getAPI(ctx, "/v1.0/doc/suites/documents/"+url.PathEscape(docKey)+"/blocks", query, &resp); err != nil {
		return nil, fmt.Errorf("query dingtalk doc blocks %s: %w", docKey, err)
	}
	return resp.blocks(), nil
}

func (c *client) SubmitMarkdownExport(ctx context.Context, dentryUUID string) (string, error) {
	if strings.TrimSpace(dentryUUID) == "" {
		return "", fmt.Errorf("dingtalk export dentry uuid is empty")
	}

	query := url.Values{}
	query.Set("targetFormat", "markdown")
	var resp exportTaskResponse
	path := "/v1.0/doc/" + url.PathEscape(dentryUUID) + "/export"
	if err := c.postAPI(ctx, path, query, map[string]interface{}{}, &resp); err != nil {
		return "", fmt.Errorf("submit dingtalk markdown export %s: %w", dentryUUID, err)
	}
	taskID := resp.taskID()
	if taskID == "" {
		return "", fmt.Errorf("submit dingtalk markdown export %s: empty task id", dentryUUID)
	}
	return taskID, nil
}

func (c *client) GetDentryIDByUUID(ctx context.Context, dentryUUID string) (dentryIdentity, error) {
	operatorID, err := c.operatorUnionID(ctx)
	if err != nil {
		return dentryIdentity{}, err
	}

	query := url.Values{}
	query.Set("operatorId", operatorID)
	var resp dentryIdentityResponse
	path := "/v2.0/doc/dentries/" + url.PathEscape(dentryUUID) + "/queryDentryId"
	if err := c.getAPI(ctx, path, query, &resp); err != nil {
		return dentryIdentity{}, fmt.Errorf("get dingtalk dentry id by uuid %s: %w", dentryUUID, err)
	}
	identity := resp.identity()
	if strings.TrimSpace(identity.SpaceID) == "" || strings.TrimSpace(identity.DentryID) == "" {
		return dentryIdentity{}, fmt.Errorf("get dingtalk dentry id by uuid %s: empty spaceId or dentryId", dentryUUID)
	}
	return identity, nil
}

func (c *client) GetDownloadInfo(ctx context.Context, spaceID, dentryID string) (downloadInfo, error) {
	operatorID, err := c.operatorUnionID(ctx)
	if err != nil {
		return downloadInfo{}, err
	}

	query := url.Values{}
	query.Set("unionId", operatorID)
	var resp downloadInfoResponse
	body := map[string]interface{}{
		"option": map[string]interface{}{
			"preferIntranet": false,
		},
	}
	path := "/v1.0/storage/spaces/" + url.PathEscape(spaceID) + "/dentries/" + url.PathEscape(dentryID) + "/downloadInfos/query"
	if err := c.postAPI(ctx, path, query, body, &resp); err != nil {
		return downloadInfo{}, fmt.Errorf("get dingtalk download info %s/%s: %w", spaceID, dentryID, err)
	}
	info := resp.info()
	if strings.TrimSpace(info.downloadURL()) == "" {
		return downloadInfo{}, fmt.Errorf("get dingtalk download info %s/%s: empty download url", spaceID, dentryID)
	}
	return info, nil
}

func (c *client) DownloadFile(ctx context.Context, info downloadInfo) ([]byte, error) {
	endpoint := info.downloadURL()
	if strings.TrimSpace(endpoint) == "" {
		return nil, fmt.Errorf("dingtalk download url is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range info.Headers {
		if strings.TrimSpace(key) == "" || value == nil {
			continue
		}
		req.Header.Set(key, fmt.Sprint(value))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, dingtalkStatusError(resp, body)
	}
	content, err := io.ReadAll(io.LimitReader(resp.Body, dingtalkMaxDownloadSize+1))
	if err != nil {
		return nil, fmt.Errorf("read dingtalk file download: %w", err)
	}
	if len(content) > dingtalkMaxDownloadSize {
		return nil, fmt.Errorf("dingtalk file download exceeds %d bytes", dingtalkMaxDownloadSize)
	}
	return content, nil
}

func (c *client) getAPI(ctx context.Context, path string, query url.Values, out interface{}) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}

	endpoint := c.cfg.GetBaseURL() + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-acs-dingtalk-access-token", token)
	return c.do(req, out)
}

func (c *client) postAPI(ctx context.Context, path string, query url.Values, body interface{}, out interface{}) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}

	endpoint := c.cfg.GetBaseURL() + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", token)
	return c.do(req, out)
}

func (c *client) postJSON(ctx context.Context, baseURL, path string, body interface{}, out interface{}) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

func (c *client) do(req *http.Request, out interface{}) error {
	for attempt := 1; attempt <= dingtalkMaxAttempts; attempt++ {
		resp, err := c.httpClient.Do(req)
		if err != nil {
			if req.Context().Err() != nil {
				return err
			}
			if attempt < dingtalkMaxAttempts && resetRequestBody(req) {
				continue
			}
			return err
		}

		body, err := io.ReadAll(resp.Body)
		if closeErr := resp.Body.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		if err != nil {
			return fmt.Errorf("read dingtalk response: %w", err)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			err := dingtalkStatusError(resp, body)
			if isRetryableStatus(resp.StatusCode) && attempt < dingtalkMaxAttempts && resetRequestBody(req) {
				continue
			}
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				return fmt.Errorf("%w: %v", datasource.ErrInvalidCredentials, err)
			}
			return err
		}
		if len(body) == 0 || out == nil {
			return nil
		}
		if err := checkBusinessError(body); err != nil {
			return err
		}
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decode dingtalk response: %w; body=%s", err, truncate(string(body), 500))
		}
		return nil
	}
	return nil
}

func resetRequestBody(req *http.Request) bool {
	if req.Body == nil || req.Body == http.NoBody {
		return true
	}
	if req.GetBody == nil {
		return false
	}
	body, err := req.GetBody()
	if err != nil {
		return false
	}
	req.Body = body
	return true
}

func isRetryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

func dingtalkStatusError(resp *http.Response, body []byte) error {
	requestID := requestIDFromResponse(resp, body)
	code, message := dingtalkErrorDetailsFromBody(body)
	suggestion := dingtalkErrorSuggestion(code, message)
	suggestionPart := ""
	if suggestion != "" {
		suggestionPart = "; suggestion=" + suggestion
	}
	if requestID != "" {
		return fmt.Errorf("dingtalk api status=%d request_id=%s body=%s%s",
			resp.StatusCode, requestID, truncate(string(body), 500), suggestionPart)
	}
	return fmt.Errorf("dingtalk api status=%d body=%s%s", resp.StatusCode, truncate(string(body), 500), suggestionPart)
}

func checkBusinessError(body []byte) error {
	envelope, err := decodeDingTalkErrorEnvelope(body)
	if err != nil {
		return nil
	}
	if envelope.Success == nil || *envelope.Success {
		return nil
	}
	code, message := dingtalkErrorDetails(envelope)
	if message == "" {
		message = "request failed"
	}
	requestID := firstNonEmpty(envelope.RequestID, envelope.RequestIDCamel, envelope.RequestIDLower)
	requestPart := ""
	if requestID != "" {
		requestPart = " request_id=" + requestID
	}
	if suggestion := dingtalkErrorSuggestion(code, message); suggestion != "" {
		return fmt.Errorf("dingtalk api error code=%s message=%s%s; suggestion=%s",
			code, message, requestPart, suggestion)
	}
	return fmt.Errorf("dingtalk api error code=%s message=%s%s", code, message, requestPart)
}

type dingtalkErrorEnvelope struct {
	Success        *bool       `json:"success"`
	Code           interface{} `json:"code"`
	Message        string      `json:"message"`
	Msg            string      `json:"msg"`
	RequestID      string      `json:"request_id"`
	RequestIDCamel string      `json:"requestId"`
	RequestIDLower string      `json:"requestid"`
}

func decodeDingTalkErrorEnvelope(body []byte) (dingtalkErrorEnvelope, error) {
	var envelope dingtalkErrorEnvelope
	err := json.Unmarshal(body, &envelope)
	return envelope, err
}

func dingtalkErrorDetailsFromBody(body []byte) (string, string) {
	envelope, err := decodeDingTalkErrorEnvelope(body)
	if err != nil {
		return "", ""
	}
	return dingtalkErrorDetails(envelope)
}

func dingtalkErrorDetails(envelope dingtalkErrorEnvelope) (string, string) {
	message := strings.TrimSpace(envelope.Message)
	if message == "" {
		message = strings.TrimSpace(envelope.Msg)
	}
	return businessCodeString(envelope.Code), message
}

func businessCodeString(code interface{}) string {
	switch v := code.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	case float64:
		return fmt.Sprintf("%.0f", v)
	case nil:
	default:
		return fmt.Sprint(v)
	}
	return "unknown"
}

func dingtalkErrorSuggestion(code, message string) string {
	normalized := strings.ToLower(strings.TrimSpace(code + " " + message))
	if strings.Contains(normalized, "orgauthlevelnotenough") ||
		strings.Contains(normalized, "auth level of org is not enough") {
		return "DingTalk organization enterprise verification level is not enough; complete or upgrade enterprise verification before using uploaded file download"
	}
	if strings.Contains(normalized, "document.aiassistant.read") {
		return "DingTalk Markdown export requires the gray/allowlisted Document.AIAssistant.Read scope; ask DingTalk to enable it for the app, or use uploaded file download / block reading fallback"
	}
	if strings.Contains(normalized, "99991672") ||
		strings.Contains(normalized, "access denied") ||
		strings.Contains(normalized, "action_scope_required") ||
		strings.Contains(normalized, "scope") {
		return "check DingTalk app API permissions and make sure the app is published and authorized"
	}
	return ""
}

func requestIDFromResponse(resp *http.Response, body []byte) string {
	if resp != nil {
		if requestID := firstNonEmpty(
			resp.Header.Get("x-acs-request-id"),
			resp.Header.Get("x-acs-trace-id"),
		); requestID != "" {
			return requestID
		}
	}
	return requestIDFromBody(body)
}

func requestIDFromBody(body []byte) string {
	var envelope struct {
		RequestID      string `json:"request_id"`
		RequestIDCamel string `json:"requestId"`
		RequestIDLower string `json:"requestid"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}
	return firstNonEmpty(envelope.RequestID, envelope.RequestIDCamel, envelope.RequestIDLower)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
