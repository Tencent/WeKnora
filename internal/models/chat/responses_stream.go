package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// Responses SSE streaming (#18). Event catalog per
// docs/research/responses-api-contract.md section 2.

type responsesStreamItem struct {
	Type      string `json:"type,omitempty"`
	ID        string `json:"id,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type responsesStreamResponse struct {
	ID     string                `json:"id,omitempty"`
	Status string                `json:"status,omitempty"`
	Usage  responsesUsage        `json:"usage,omitempty"`
	Error  *responsesError       `json:"error,omitempty"`
	Output []responsesOutputItem `json:"output,omitempty"`
}

type responsesStreamEvent struct {
	Type           string                  `json:"type"`
	Delta          string                  `json:"delta,omitempty"`
	OutputIndex    int                     `json:"output_index,omitempty"`
	Item           responsesStreamItem     `json:"item,omitempty"`
	Arguments      string                  `json:"arguments,omitempty"`
	Response       responsesStreamResponse `json:"response,omitempty"`
	SequenceNumber int                     `json:"sequence_number,omitempty"`
}

type responsesFnCallAcc struct {
	id     string
	callID string
	name   string
	args   strings.Builder
}

// runResponsesStream is the pure SSE core: it folds a Responses event byte
// stream into StreamResponse emissions. Separated from HTTP plumbing so
// transcripts are unit-testable.
func runResponsesStream(r io.Reader, emit func(types.StreamResponse)) error {
	reader := NewSSEReader(r)
	fnCalls := map[int]*responsesFnCallAcc{}
	var usage *types.TokenUsage
	acc := func(idx int) *responsesFnCallAcc {
		a, ok := fnCalls[idx]
		if !ok {
			a = &responsesFnCallAcc{}
			fnCalls[idx] = a
		}
		return a
	}
	flushDone := func(finishReason string) {
		emit(types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer,
			Content:      "",
			Done:         true,
			Usage:        usage,
			FinishReason: finishReason,
		})
	}
	for {
		event, err := reader.ReadEvent()
		if err != nil {
			if err == io.EOF {
				flushDone("")
				return nil
			}
			emit(types.StreamResponse{
				ResponseType: types.ResponseTypeError,
				Content:      err.Error(),
				Done:         true,
				Usage:        usage,
				FinishReason: types.FinishReasonIncomplete,
			})
			return err
		}
		if event == nil {
			continue
		}
		if event.Done {
			flushDone("")
			return nil
		}
		if len(event.Data) == 0 {
			continue
		}
		var ev responsesStreamEvent
		if err := json.Unmarshal(event.Data, &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "response.output_text.delta":
			if ev.Delta != "" {
				emit(types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Content:      ev.Delta,
				})
			}
		case "response.output_item.added":
			if ev.Item.Type == "function_call" {
				a := acc(ev.OutputIndex)
				a.id, a.callID, a.name = ev.Item.ID, ev.Item.CallID, ev.Item.Name
				a.args.WriteString(ev.Item.Arguments)
			}
		case "response.function_call_arguments.delta":
			acc(ev.OutputIndex).args.WriteString(ev.Delta)
		case "response.function_call_arguments.done":
			a := acc(ev.OutputIndex)
			args := a.args.String()
			if ev.Arguments != "" {
				args = ev.Arguments
			}
			id := a.callID
			if id == "" {
				id = a.id
			}
			emit(types.StreamResponse{
				ResponseType: types.ResponseTypeToolCall,
				ToolCalls: []types.LLMToolCall{{
					ID:   id,
					Type: "function",
					Function: types.FunctionCall{
						Name:      a.name,
						Arguments: args,
					},
				}},
			})
			delete(fnCalls, ev.OutputIndex)
		case "response.completed":
			u := types.TokenUsage{
				PromptTokens:     ev.Response.Usage.InputTokens,
				CompletionTokens: ev.Response.Usage.OutputTokens,
			}
			u.TotalTokens = u.PromptTokens + u.CompletionTokens
			usage = &u
			flushDone("stop")
			return nil
		case "response.incomplete":
			flushDone(string(types.FinishReasonIncomplete))
			return nil
		case "response.failed":
			msg := "responses stream failed"
			if ev.Response.Error != nil && ev.Response.Error.Message != "" {
				msg = ev.Response.Error.Message
			}
			emit(types.StreamResponse{
				ResponseType: types.ResponseTypeError,
				Content:      msg,
				Done:         true,
				Usage:        usage,
			})
			return nil
		case "error":
			msg := "responses stream error"
			if ev.Response.Error != nil && ev.Response.Error.Message != "" {
				msg = ev.Response.Error.Message
			}
			emit(types.StreamResponse{
				ResponseType: types.ResponseTypeError,
				Content:      msg,
				Done:         true,
				Usage:        usage,
			})
			return nil
		default:
			// Lifecycle / item-part events carry no consumer payload.
		}
	}
}

// chatStreamWithResponses POSTs a streaming Responses request and folds the
// SSE transcript into the channel.
func (c *RemoteAPIChat) chatStreamWithResponses(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	req := responsesRequest{
		Model:     c.modelName,
		Input:     buildResponsesInputValue(messages),
		Reasoning: &responsesReasoning{Effort: c.responsesEffort},
		Stream:    true,
	}
	req.Tools, req.ToolChoice = buildResponsesTools(opts)
	if opts != nil {
		if opts.MaxCompletionTokens > 0 {
			req.MaxOutputTokens = opts.MaxCompletionTokens
		} else if opts.MaxTokens > 0 {
			req.MaxOutputTokens = opts.MaxTokens
		}
	}
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal responses stream request: %w", err)
	}
	endpoint := responsesEndpoint(c.baseURL)
	if err := secutils.ValidateURLForSSRF(endpoint); err != nil {
		return nil, fmt.Errorf("endpoint SSRF check failed: %w", err)
	}
	logger.Infof(ctx, "[LLM Stream Request] Responses, endpoint=%s, model=%s", endpoint, c.modelName)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.adapter.Auth(httpReq, c.authCreds(), jsonData)
	httpReq.Header.Set("Accept", "text/event-stream")
	secutils.ApplyCustomHeaders(httpReq, c.customHeaders)

	resp, err := rawHTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	streamChan := make(chan types.StreamResponse)
	go func() {
		defer close(streamChan)
		defer resp.Body.Close()
		err := runResponsesStream(resp.Body, func(sr types.StreamResponse) {
			streamChan <- sr
		})
		if err != nil {
			logger.Errorf(ctx, "Responses stream error: %v", err)
		}
	}()
	return streamChan, nil
}
