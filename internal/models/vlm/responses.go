package vlm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	modelutils "github.com/Tencent/WeKnora/internal/models/utils"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// predictWithResponses sends image+prompt to POST <baseURL>/responses for
// responses-provider models (#25). Request/response shapes reuse the chat
// builders; VLM keeps its data-URI loop, MaxTokens, timeout and temperature.
func (v *RemoteAPIVLM) predictWithResponses(ctx context.Context, imgBytesList [][]byte, prompt string) (string, error) {
	msg := chat.Message{Role: "user"}
	if prompt != "" {
		msg.MultiContent = append(msg.MultiContent, chat.MessageContentPart{
			Type: "text",
			Text: prompt,
		})
	}
	totalImageSize := 0
	for _, imgBytes := range imgBytesList {
		if len(imgBytes) == 0 {
			continue
		}
		totalImageSize += len(imgBytes)
		mimeType := detectImageMIME(imgBytes)
		dataURI := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(imgBytes)
		msg.MultiContent = append(msg.MultiContent, chat.MessageContentPart{
			Type:     "image_url",
			ImageURL: &chat.ImageURL{URL: dataURI},
		})
	}

	logger.Infof(ctx, "[VLM] Calling Responses API, model=%s, baseURL=%s, numImages=%d, totalImageSize=%d",
		v.modelName, v.baseURL, len(imgBytesList), totalImageSize)

	reqBody := map[string]any{
		"model":             v.modelName,
		"input":             chat.BuildResponsesInputValue([]chat.Message{msg}),
		"max_output_tokens": defaultMaxToks,
		"reasoning":         map[string]any{"effort": v.effort},
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal responses VLM request: %w", err)
	}
	endpoint := modelutils.AppendPathOnce(v.baseURL, "/responses")
	if err := secutils.ValidateURLForSSRF(endpoint); err != nil {
		return "", fmt.Errorf("endpoint SSRF check failed: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+v.apiKey)

	resp, err := v.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("Responses VLM request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Responses VLM request: error, status code: %d, message: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read responses VLM response: %w", err)
	}
	result, err := chat.ParseResponsesBody(body)
	if err != nil {
		return "", fmt.Errorf("Responses VLM request: %w", err)
	}
	if result.FinishReason == "incomplete" {
		// Mirror the length-truncation guard on the chat-completions path:
		// an exhausted budget yields no usable text, which callers record
		// distinctly from imageless content.
		return "", fmt.Errorf(
			"Responses VLM returned no content: completion incomplete at %d tokens",
			defaultMaxToks,
		)
	}
	logger.Infof(ctx, "[VLM] Responses received, len=%d", len(result.Content))
	return result.Content, nil
}
