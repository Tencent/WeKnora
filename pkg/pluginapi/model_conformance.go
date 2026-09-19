package pluginapi

import (
	"context"
	"fmt"
	"time"

	pluginproto "github.com/Tencent/WeKnora/pkg/pluginapi/proto"
)

// ModelConformanceReport is the result of RunModelConformance. Each capability
// is probed only when the plugin declares it in ModelInfo; a declared
// capability that returns a non-empty error is recorded as failed.
type ModelConformanceReport struct {
	PluginID       string        `json:"plugin_id"`
	HandshakeOK    bool          `json:"handshake_ok"`
	HealthOK       bool          `json:"health_ok"`
	Capabilities   []string      `json:"capabilities,omitempty"`
	ChatOK         bool          `json:"chat_ok,omitempty"`
	EmbeddingOK    bool          `json:"embedding_ok,omitempty"`
	RerankOK       bool          `json:"rerank_ok,omitempty"`
	VLMOK          bool          `json:"vlm_ok,omitempty"`
	ASROK          bool          `json:"asr_ok,omitempty"`
	Duration       time.Duration `json:"duration"`
	Errors         []string      `json:"errors,omitempty"`
}

// RunModelConformance is the repeatable smoke test a third-party model plugin
// author can run without importing WeKnora internals. It verifies the control
// plane (handshake + health), reads the declared capabilities, then probes each
// declared capability with an empty request and asserts the response envelope
// carries no error. Undeclared capabilities are skipped rather than treated as
// failures.
func RunModelConformance(ctx context.Context, control PluginControlClient, client ModelPluginClient) ModelConformanceReport {
	started := time.Now()
	report := ModelConformanceReport{}
	appendError := func(err error) {
		if err != nil {
			report.Errors = append(report.Errors, err.Error())
		}
	}

	// Control plane.
	handshake, err := control.Handshake(ctx, &pluginproto.HandshakeRequest{})
	if err == nil {
		var value HandshakeResponse
		err = DecodeHandshake(handshake, &value)
		report.PluginID = value.PluginID
		report.HandshakeOK = err == nil && value.ProtocolVersion == "v1"
	}
	appendError(err)
	if !report.HandshakeOK && err == nil {
		appendError(fmt.Errorf("invalid handshake response"))
	}

	health, err := control.Health(ctx, &pluginproto.HealthRequest{})
	if err == nil {
		var value HealthResponse
		err = DecodeHealth(health, &value)
		report.HealthOK = err == nil && value.State != ""
	}
	appendError(err)
	if !report.HealthOK && err == nil {
		appendError(fmt.Errorf("invalid health response"))
	}

	// Read declared capabilities.
	info, err := client.ModelInfo(ctx, &pluginproto.ModelInfoRequest{})
	if err != nil {
		appendError(fmt.Errorf("model info: %w", err))
	} else {
		report.Capabilities = info.GetCapabilities()
	}

	has := func(cap string) bool {
		for _, c := range report.Capabilities {
			if c == cap {
				return true
			}
		}
		return false
	}

	// Probe each declared capability with an empty request.
	if has("chat") {
		resp, callErr := client.Chat(ctx, &pluginproto.ModelChatRequest{})
		if callErr == nil && resp.GetError() != "" {
			callErr = fmt.Errorf("plugin response: %s", resp.GetError())
		}
		report.ChatOK = callErr == nil
		appendError(callErr)
	}
	if has("embedding") {
		resp, callErr := client.Embed(ctx, &pluginproto.ModelEmbedRequest{})
		if callErr == nil && resp.GetError() != "" {
			callErr = fmt.Errorf("plugin response: %s", resp.GetError())
		}
		report.EmbeddingOK = callErr == nil
		appendError(callErr)
	}
	if has("rerank") {
		resp, callErr := client.Rerank(ctx, &pluginproto.ModelRerankRequest{})
		if callErr == nil && resp.GetError() != "" {
			callErr = fmt.Errorf("plugin response: %s", resp.GetError())
		}
		report.RerankOK = callErr == nil
		appendError(callErr)
	}
	if has("vllm") {
		resp, callErr := client.PredictVLM(ctx, &pluginproto.ModelVLMRequest{})
		if callErr == nil && resp.GetError() != "" {
			callErr = fmt.Errorf("plugin response: %s", resp.GetError())
		}
		report.VLMOK = callErr == nil
		appendError(callErr)
	}
	if has("asr") {
		resp, callErr := client.Transcribe(ctx, &pluginproto.ModelASRRequest{})
		if callErr == nil && resp.GetError() != "" {
			callErr = fmt.Errorf("plugin response: %s", resp.GetError())
		}
		report.ASROK = callErr == nil
		appendError(callErr)
	}

	report.Duration = time.Since(started)
	return report
}
