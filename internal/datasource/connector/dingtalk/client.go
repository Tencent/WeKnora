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
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	dingtalkBaseURL = "https://api.dingtalk.com"
	userAgent       = "WeKnora-DingTalk-Connector/1.0"
)

type client struct {
	clientID     string
	clientSecret string
	userID       string
	unionID      string
	baseURL      string
	mcpURL       string // DingTalk MCP gateway URL (includes key auth param)
	httpClient   *http.Client
	token        string
	tokenExpAt   time.Time
}

func newClient(cfg *Config) *client {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = dingtalkBaseURL
	}
	return &client{
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		userID:       cfg.UserID,
		baseURL:      baseURL,
		mcpURL:       cfg.MCPServerURL,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *client) getAccessToken(ctx context.Context) (string, error) {
	if c.token != "" && time.Now().Before(c.tokenExpAt) {
		return c.token, nil
	}

	body := map[string]string{
		"appKey":    c.clientID,
		"appSecret": c.clientSecret,
	}
	jsonBody, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1.0/oauth2/accessToken",
		bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("dingtalk token response %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		AccessToken string `json:"accessToken"`
		ExpireIn    int64  `json:"expireIn"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	c.token = result.AccessToken
	c.tokenExpAt = time.Now().Add(time.Duration(result.ExpireIn)*time.Second - 5*time.Minute)
	return c.token, nil
}

func (c *client) request(ctx context.Context, method, path string, query map[string]string, body interface{}, response interface{}) error {
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return err
	}

	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", token)
	req.Header.Set("User-Agent", userAgent)

	q := req.URL.Query()
	for k, v := range query {
		q.Add(k, v)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("dingtalk API %s returned %d: %s%s",
			path, resp.StatusCode, string(respBody), dingTalkErrorHint(respBody))
	}

	if response != nil {
		if err := json.NewDecoder(resp.Body).Decode(response); err != nil {
			return err
		}
	}
	return nil
}

// dingTalkErrorHint maps known DingTalk error codes to an actionable hint, so the
// raw API error surfaced in sync logs is easier to act on.
func dingTalkErrorHint(body []byte) string {
	var e struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &e)
	switch e.Code {
	case "orgAuthLevelNotEnough":
		return " (企业认证等级不足：该接口要求组织完成钉钉企业认证，请在钉钉管理后台完成企业认证后重试)"
	case "permissionDenied":
		return " (权限不足：请确认应用已申请并授予知识库文档读/写权限，且操作者对该文件有权限)"
	}
	return ""
}

func (c *client) getUnionID(ctx context.Context) (string, error) {
	if c.unionID != "" {
		return c.unionID, nil
	}

	token, err := c.getAccessToken(ctx)
	if err != nil {
		return "", err
	}

	urlStr := "https://oapi.dingtalk.com/topapi/v2/user/get"
	if c.baseURL != dingtalkBaseURL {
		urlStr = c.baseURL + "/topapi/v2/user/get"
	}

	formData := url.Values{}
	formData.Set("access_token", token)
	formData.Set("userid", c.userID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, strings.NewReader(formData.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("dingtalk query user returned %d: %s", resp.StatusCode, string(respBody))
	}

	var res struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		Result  struct {
			UnionID string `json:"unionid"`
		} `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	// DingTalk API sometimes returns error code inside JSON payload
	if res.ErrCode != 0 {
		return "", fmt.Errorf("dingtalk query user failed: errcode=%d, errmsg=%s", res.ErrCode, res.ErrMsg)
	}

	c.unionID = res.Result.UnionID
	return c.unionID, nil
}

func (c *client) Ping(ctx context.Context) error {
	_, err := c.getUnionID(ctx)
	return err
}

func (c *client) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	unionID, err := c.getUnionID(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve operator unionId: %w", err)
	}

	query := map[string]string{
		"operatorId": unionID,
		"maxResults": "50",
	}

	var resp struct {
		Workspaces []Workspace `json:"workspaces"`
		NextToken  string      `json:"nextToken"`
	}

	err = c.request(ctx, http.MethodGet, "/v2.0/wiki/workspaces", query, nil, &resp)
	if err != nil {
		logger.Warnf(ctx, "[DingTalk] list workspaces failed: %v", err)
		return nil, err
	}

	return resp.Workspaces, nil
}

func (c *client) ListNodes(ctx context.Context, workspaceID string, parentNodeID string) ([]Node, error) {
	unionID, err := c.getUnionID(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve operator unionId: %w", err)
	}

	query := map[string]string{
		"operatorId":  unionID,
		"workspaceId": workspaceID,
		"maxResults":  "50",
	}
	if parentNodeID != "" {
		query["parentNodeId"] = parentNodeID
	}

	var resp struct {
		Nodes     []Node `json:"nodes"`
		NextToken string `json:"nextToken"`
	}

	err = c.request(ctx, http.MethodGet, "/v2.0/wiki/nodes", query, nil, &resp)
	if err != nil {
		return nil, err
	}

	return resp.Nodes, nil
}

func (c *client) GetWorkspace(ctx context.Context, workspaceID string) (*Workspace, error) {
	unionID, err := c.getUnionID(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve operator unionId: %w", err)
	}

	query := map[string]string{
		"operatorId": unionID,
	}

	var resp struct {
		Workspace Workspace `json:"workspace"`
	}

	path := fmt.Sprintf("/v2.0/wiki/workspaces/%s", workspaceID)
	err = c.request(ctx, http.MethodGet, path, query, nil, &resp)
	if err != nil {
		return nil, err
	}

	return &resp.Workspace, nil
}

// dentryLocation is the storage coordinate of a knowledge-base file.
type dentryLocation struct {
	SpaceID  string
	DentryID string
}

// QueryDentryID resolves a wiki node's dentryUuid into the storage spaceId and
// dentryId required by the file-download API.
// API: GET /v2.0/doc/dentries/{dentryUuid}/queryDentryId
func (c *client) QueryDentryID(ctx context.Context, dentryUUID string) (dentryLocation, error) {
	unionID, err := c.getUnionID(ctx)
	if err != nil {
		return dentryLocation{}, fmt.Errorf("resolve operator unionId: %w", err)
	}

	query := map[string]string{"operatorId": unionID}

	var resp struct {
		DentryUUID string `json:"dentryUuid"`
		DentryID   string `json:"dentryId"`
		SpaceID    string `json:"spaceId"`
	}

	path := fmt.Sprintf("/v2.0/doc/dentries/%s/queryDentryId", dentryUUID)
	if err := c.request(ctx, http.MethodGet, path, query, nil, &resp); err != nil {
		return dentryLocation{}, err
	}
	if resp.SpaceID == "" || resp.DentryID == "" {
		return dentryLocation{}, fmt.Errorf("queryDentryId returned empty spaceId/dentryId for %s", dentryUUID)
	}
	return dentryLocation{SpaceID: resp.SpaceID, DentryID: resp.DentryID}, nil
}

// DownloadNode resolves a wiki node's dentryUuid to its storage location and
// downloads the raw file bytes.
func (c *client) DownloadNode(ctx context.Context, dentryUUID string) ([]byte, error) {
	loc, err := c.QueryDentryID(ctx, dentryUUID)
	if err != nil {
		return nil, err
	}
	return c.DownloadFile(ctx, loc.SpaceID, loc.DentryID)
}

// DownloadFile obtains a signed download URL for a knowledge-base file and
// returns its raw bytes.
// API: POST /v1.0/storage/spaces/{spaceId}/dentries/{dentryId}/downloadInfos/query
func (c *client) DownloadFile(ctx context.Context, spaceID, dentryID string) ([]byte, error) {
	unionID, err := c.getUnionID(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve operator unionId: %w", err)
	}

	query := map[string]string{"unionId": unionID}
	body := map[string]interface{}{
		"option": map[string]interface{}{"preferIntranet": false},
	}

	var resp struct {
		Protocol            string `json:"protocol"`
		HeaderSignatureInfo struct {
			ResourceUrls []string          `json:"resourceUrls"`
			Headers      map[string]string `json:"headers"`
		} `json:"headerSignatureInfo"`
	}

	path := fmt.Sprintf("/v1.0/storage/spaces/%s/dentries/%s/downloadInfos/query", spaceID, dentryID)
	if err := c.request(ctx, http.MethodPost, path, query, body, &resp); err != nil {
		return nil, err
	}

	if len(resp.HeaderSignatureInfo.ResourceUrls) == 0 {
		return nil, fmt.Errorf("download info returned no resource url for dentry %s", dentryID)
	}

	return c.fetchSignedURL(ctx, resp.HeaderSignatureInfo.ResourceUrls[0], resp.HeaderSignatureInfo.Headers)
}

// fetchSignedURL downloads bytes from a signed resource URL, applying the
// signature headers returned by the download-info API. The URL points at
// DingTalk storage/CDN, so it must not carry the access-token header.
func (c *client) fetchSignedURL(ctx context.Context, resourceURL string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("download returned %d: %s", resp.StatusCode, string(snippet))
	}

	return io.ReadAll(resp.Body)
}

// ExportDocMarkdown exports a DingTalk-native online document to Markdown.
// DingTalk renders the export asynchronously, so this submits an export job and
// polls until the download URL is ready, then downloads the rendered bytes.
// API: POST /v2.0/doc/me/export/submit ; GET /v2.0/doc/me/export/task/query
func (c *client) ExportDocMarkdown(ctx context.Context, dentryUUID string) ([]byte, error) {
	unionID, err := c.getUnionID(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve operator unionId: %w", err)
	}

	// 1. Submit the export job.
	submitBody := map[string]interface{}{
		"dentryUuid":   dentryUUID,
		"operatorId":   unionID,
		"targetFormat": "markdown",
	}
	var submit struct {
		TaskID      string `json:"taskId"`
		DownloadURL string `json:"downloadUrl"`
	}
	if err := c.request(ctx, http.MethodPost, "/v2.0/doc/me/export/submit", nil, submitBody, &submit); err != nil {
		return nil, err
	}

	downloadURL := submit.DownloadURL
	if downloadURL == "" {
		if submit.TaskID == "" {
			return nil, fmt.Errorf("export submit returned neither taskId nor downloadUrl for %s", dentryUUID)
		}
		// 2. Poll until the export task finishes.
		downloadURL, err = c.pollExportTask(ctx, unionID, submit.TaskID)
		if err != nil {
			return nil, err
		}
	}

	// 3. Download the rendered Markdown (plain signed URL, no extra headers).
	return c.fetchSignedURL(ctx, downloadURL, nil)
}

// pollExportTask polls the export task until a download URL is available.
func (c *client) pollExportTask(ctx context.Context, unionID, taskID string) (string, error) {
	const (
		maxAttempts = 30
		interval    = time.Second
	)
	query := map[string]string{"operatorId": unionID, "taskId": taskID}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}

		var task struct {
			Status      string `json:"status"`
			DownloadURL string `json:"downloadUrl"`
		}
		if err := c.request(ctx, http.MethodGet, "/v2.0/doc/me/export/task/query", query, nil, &task); err != nil {
			return "", err
		}
		if task.DownloadURL != "" {
			return task.DownloadURL, nil
		}
		if s := strings.ToUpper(task.Status); strings.Contains(s, "FAIL") || strings.Contains(s, "ERROR") {
			return "", fmt.Errorf("export task %s failed: status=%s", taskID, task.Status)
		}
	}
	return "", fmt.Errorf("export task %s did not finish after %d attempts", taskID, maxAttempts)
}

// ─── MCP-based document reading (DingTalk MCP Gateway) ──────────────────────

// mcpEffectiveURL returns the MCP gateway URL to use.
// In tests, baseURL is overridden to the fake server, so we derive the MCP
// endpoint from it; in production, mcpURL must be set from config.
func (c *client) mcpEffectiveURL() (string, error) {
	if c.baseURL != dingtalkBaseURL {
		return c.baseURL + "/mcp/doc", nil
	}
	if c.mcpURL == "" {
		return "", fmt.Errorf("MCP server URL not configured — add mcp_server_url to the data source credentials " +
			"(e.g. https://mcp-gw.dingtalk.com/server/{hash}?key={apikey})")
	}
	return c.mcpURL, nil
}

// mcpToolCall sends a tools/call JSON-RPC request to the MCP gateway and
// returns the first text content block as raw bytes for the caller to parse.
// The DingTalk MCP gateway embeds auth in the URL (key query param), so no
// extra auth header is added here.
func (c *client) mcpToolCall(ctx context.Context, tool string, args map[string]interface{}) ([]byte, error) {
	endpoint, err := c.mcpEffectiveURL()
	if err != nil {
		return nil, err
	}

	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]interface{}{"name": tool, "arguments": args},
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("MCP %s returned %d: %s", tool, resp.StatusCode, snippet)
	}

	var envelope struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("parse MCP response: %w", err)
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", envelope.Error.Code, envelope.Error.Message)
	}
	if envelope.Result.IsError {
		var sb strings.Builder
		for _, blk := range envelope.Result.Content {
			sb.WriteString(blk.Text)
		}
		return nil, fmt.Errorf("MCP tool %s failed: %s", tool, sb.String())
	}
	for _, blk := range envelope.Result.Content {
		if blk.Type == "text" && blk.Text != "" {
			return []byte(blk.Text), nil
		}
	}
	return nil, fmt.Errorf("MCP tool %s returned no text content", tool)
}

// GetDocumentContent fetches a DingTalk-native online document as Markdown via
// the MCP get_document_content tool. Supports adoc, axls, amind, able formats.
func (c *client) GetDocumentContent(ctx context.Context, nodeID string) ([]byte, error) {
	raw, err := c.mcpToolCall(ctx, "get_document_content", map[string]interface{}{
		"nodeId": nodeID,
		"format": "markdown",
	})
	if err != nil {
		return nil, err
	}

	// The MCP gateway returns tool output as a JSON string in content[0].text.
	var out struct {
		Markdown string `json:"markdown"`
		Success  bool   `json:"success"`
		ErrorMsg string `json:"errorMsg"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		// If the text is already plain Markdown (not JSON), return as-is.
		return raw, nil
	}
	if !out.Success {
		return nil, fmt.Errorf("get_document_content failed: %s", out.ErrorMsg)
	}
	return []byte(out.Markdown), nil
}

// DownloadFileMCP fetches an uploaded file via the MCP download_file tool.
// The tool returns a signed resourceUrl + headers; we fetch the file ourselves.
func (c *client) DownloadFileMCP(ctx context.Context, nodeID string) ([]byte, error) {
	raw, err := c.mcpToolCall(ctx, "download_file", map[string]interface{}{
		"nodeId": nodeID,
	})
	if err != nil {
		return nil, err
	}

	var out struct {
		Success      bool              `json:"success"`
		ErrorMsg     string            `json:"errorMsg"`
		ResourceURL  string            `json:"resourceUrl"`
		ResourceURLs []string          `json:"resourceUrls"`
		Headers      map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse download_file output: %w", err)
	}
	if !out.Success {
		return nil, fmt.Errorf("download_file failed: %s", out.ErrorMsg)
	}

	resourceURL := out.ResourceURL
	if resourceURL == "" && len(out.ResourceURLs) > 0 {
		resourceURL = out.ResourceURLs[0]
	}
	if resourceURL == "" {
		return nil, fmt.Errorf("download_file returned no resource URL for node %s", nodeID)
	}
	return c.fetchSignedURL(ctx, resourceURL, out.Headers)
}
