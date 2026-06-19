package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

type CodexChat struct {
	modelName     string
	modelID       string
	baseURL       string
	authFile      string
	tokenSource   *codexTokenSource
	customHeaders map[string]string
}

func NewCodexChat(config *ChatConfig) (*CodexChat, error) {
	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = provider.OpenAICodexBaseURL
	}
	if err := secutils.ValidateURLForSSRF(baseURL); err != nil {
		return nil, fmt.Errorf("baseURL SSRF check failed: %w", err)
	}
	authFile := ""
	if config.ExtraConfig != nil {
		authFile = config.ExtraConfig["codex_auth_file"]
	}
	authFile = resolveCodexAuthFile(authFile)

	return &CodexChat{
		modelName:     config.ModelName,
		modelID:       config.ModelID,
		baseURL:       baseURL,
		authFile:      authFile,
		tokenSource:   getCodexTokenSource(authFile),
		customHeaders: config.CustomHeaders,
	}, nil
}

func (c *CodexChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	reqBody := buildCodexResponsesRequest(c.modelName, messages, opts, true)
	timeoutCtx, cancel := withLLMTimeout(ctx, defaultChatTimeout)
	defer cancel()
	resp, err := c.doResponsesRequest(timeoutCtx, reqBody)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read codex response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("codex API request failed with status %d: %s", resp.StatusCode, string(body))
	}
	result, err := parseCodexResponsesSSE(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	logUsage(ctx, c.modelName, &result.Usage)
	return result, nil
}

func (c *CodexChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	reqBody := buildCodexResponsesRequest(c.modelName, messages, opts, true)
	timeoutCtx, cancel := withLLMTimeout(ctx, defaultStreamTimeout)
	resp, err := c.doResponsesRequest(timeoutCtx, reqBody)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("codex API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	streamChan := make(chan types.StreamResponse)
	go func() {
		defer cancel()
		defer resp.Body.Close()
		processCodexResponsesStream(c.modelName, resp.Body, streamChan)
	}()
	return streamChan, nil
}

func (c *CodexChat) GetModelName() string {
	return c.modelName
}

func (c *CodexChat) GetModelID() string {
	return c.modelID
}

func (c *CodexChat) endpoint() string {
	baseURL := strings.TrimRight(c.baseURL, "/")
	if strings.HasSuffix(baseURL, "/responses") {
		return baseURL
	}
	return baseURL + "/responses"
}

func (c *CodexChat) doResponsesRequest(ctx context.Context, reqBody codexResponsesRequest) (*http.Response, error) {
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal codex request: %w", err)
	}

	accessToken, accountID, err := c.tokenSource.bearer(ctx)
	if err != nil {
		return nil, err
	}
	endpoint := c.endpoint()
	if err := secutils.ValidateURLForSSRF(endpoint); err != nil {
		return nil, fmt.Errorf("endpoint SSRF check failed: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create codex request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+accessToken)
	httpReq.Header.Set("chatgpt-account-id", accountID)
	httpReq.Header.Set("OpenAI-Beta", "responses=experimental")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	secutils.ApplyCustomHeaders(httpReq, c.customHeaders)
	return rawHTTPClient.Do(httpReq)
}
