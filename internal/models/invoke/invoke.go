package invoke

// Entry surface (design §6.4): the ONLY functions upstream callers see.
// Responsibilities kept here (not in adapters): dispatch, custom-header
// overlay with protection rules, image preprocessing, the multimodal
// degradation retry (needs a rebuilt request body), langfuse tracking, the
// StreamEvent→types.StreamResponse mechanical mapping, and the bare-error
// fallback conversion (design §6.5).

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// LocalImageResolver resolves a stored image reference into inline bytes at
// invoke time. Set by the application layer at startup.
// Full migration of chat/image_resolve.go happens in P1c with the callers.
var LocalImageResolver func(storageURL string) ([]byte, bool)

// defaultExecutor is the process-wide HTTP executor backing every entry call.
var defaultExecutor = NewExecutor()

// resolveAdapter dispatches by provider name. An empty registry or an unknown
// provider returns an explicit error — never a panic (design §6.2/§6.4).
func resolveAdapter(name string) (Adapter, error) {
	if Default.Len() == 0 {
		return nil, &ProviderError{Kind: ErrUnsupportedType, Message: "no model adapters registered"}
	}
	a, ok := Default.Get(name)
	if !ok {
		return nil, &ProviderError{Kind: ErrUnsupportedType, Message: "provider not registered: " + name}
	}
	return a, nil
}

// endpoint extracts the Endpoint view of a ModelConfig and folds the
// per-model overrides adapters cannot see (seam ④, P1c):
// ExtraConfig["api_version"] → APIVersion (azure deployment URL query) and
// ExtraConfig["thinking_control"] → ThinkingControl (v1 parseThinkingOverride
// semantics).
func endpoint(m *ModelConfig) Endpoint {
	ep := Endpoint{BaseURL: m.BaseURL, Credentials: m.Credentials}
	if m.ExtraConfig != nil {
		ep.APIVersion = strings.TrimSpace(m.ExtraConfig["api_version"])
		ep.ThinkingControl = foldThinkingControl(m.ExtraConfig)
		// Rerank-only opt-in (vLLM semantics, issue #2143): NEVER sent unless
		// explicitly configured — providers that honor it keep only the last
		// N tokens of the templated rerank prompt.
		ep.TruncatePromptTokens = foldTruncatePromptTokens(m.ExtraConfig)
	}
	return ep
}

// foldTruncatePromptTokens parses extra_config["truncate_prompt_tokens"];
// invalid or non-positive values fold to 0 (= not sent). DRIFT vs v1
// (recorded, P3 review finding 6): the v1 factory failed fast on invalid
// values at construction; v2 silently omits the param and the rerank call
// proceeds. The param is opt-in tuning, so an invalid value degrades to the
// default behavior instead of failing the model.
func foldTruncatePromptTokens(extra map[string]string) int {
	raw := strings.TrimSpace(extra["truncate_prompt_tokens"])
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// foldThinkingControl normalizes extra_config.thinking_control into the
// strategy token v1 parseThinkingOverride produced (chat/thinking.go:124-141):
// "" passes through (adapter default strategy applies), the four known tokens
// map to themselves, and any unknown non-empty value falls back to
// chat_template_kwargs (legacy default-mode behavior — never an error).
func foldThinkingControl(extra map[string]string) string {
	switch v := strings.ToLower(strings.TrimSpace(extra["thinking_control"])); v {
	case "", "none", "enable_thinking", "thinking_type", "chat_template_kwargs":
		return v
	default:
		return "chat_template_kwargs"
	}
}

// EffectiveThinkingControl reports the strategy token that will carry the
// thinking flag on the wire for this config (debug-drawer diagnostics, ported
// from v1 chat.EffectiveThinkingControl). A folded extra_config override wins
// (single source: foldThinkingControl). With no override the concrete strategy
// is adapter-private knowledge (§6.2), so it is reported honestly as
// "provider_default" unless the vendor declares thinking unsupported ("none").
func EffectiveThinkingControl(m *ModelConfig) string {
	if m == nil {
		return "none"
	}
	if tc := foldThinkingControl(m.ExtraConfig); tc != "" {
		return tc
	}
	a, err := resolveAdapter(m.Provider)
	if err != nil {
		return "provider_default"
	}
	if cc := a.Capabilities().Chat; cc != nil && !cc.Thinking.Supported {
		return "none"
	}
	return "provider_default"
}

// foldChatOptions ports v1 resolveThinkingLevelOpts (chat/remote_api.go:153-170)
// to the entry (seam ④): the tier chain is folded BEFORE dispatch so
// opts.ThinkingLevel leaves the entry as the single wire winner and adapters
// stay tier-blind. The call level (already session > agent > global from the
// service layer) beats the model record's ThinkingLevel/SelectedLevels, which
// beats the provider's declared DefaultLevel; ResolveThinkingLevel validates
// each tier against both level sets. Copy-on-write — no copy when the call
// level already wins.
func foldChatOptions(m *ModelConfig, opts *ChatOptions) *ChatOptions {
	if opts == nil {
		return opts
	}
	var caps ThinkingCaps
	if a, err := resolveAdapter(m.Provider); err == nil {
		if cc := a.Capabilities().Chat; cc != nil {
			caps = cc.Thinking
		}
	}
	resolved := ResolveThinkingLevel(opts.ThinkingLevel, m.ThinkingLevel, m.SelectedLevels, caps)
	if resolved == opts.ThinkingLevel {
		return opts
	}
	cloned := *opts
	cloned.ThinkingLevel = resolved
	return &cloned
}

// Chat runs one non-stream chat call.
func Chat(ctx context.Context, m *ModelConfig, opts *ChatOptions) (*ChatResponse, error) {
	start := time.Now()
	gen := startLangfuse(ctx, "chat.completion", m, opts)
	resp, err := chatWithFallback(ctx, m, opts)
	gen.finish(resp, err)
	logLLMDebugCall(ctx, m.ModelName, opts.Messages, opts, resp, err, time.Since(start))
	return resp, err
}

// chatWithFallback dispatches and applies the multimodal degradation retry:
// when the provider rejects image input, the request body must be rebuilt
// without images, so the retry re-runs Build→Execute against the stripped
// options (the executor's same-body retry cannot express this; design §6.4).
// Every attempt resolves its thinking level freshly (v1 semantics).
func chatWithFallback(ctx context.Context, m *ModelConfig, opts *ChatOptions) (*ChatResponse, error) {
	resp, err := chatOnce(ctx, m, opts)
	if err == nil || !HasImages(opts.Messages) || !isMultimodalNotSupportedError(err) {
		return resp, err
	}
	logger.Infof(ctx, "provider %s rejected image input, retrying without images", m.Provider)
	stripped := *opts
	stripped.Messages = StripImages(opts.Messages)
	stripped.Stream = false
	return chatOnce(ctx, m, &stripped)
}

func chatOnce(ctx context.Context, m *ModelConfig, opts *ChatOptions) (*ChatResponse, error) {
	opts = foldChatOptions(m, opts)
	result, err := chatExecute(ctx, m, opts)
	if err != nil {
		// v1 wrapInvokeError("create chat completion", …): the action prefix
		// rides the surfaced message while Kind/Status stay authoritative.
		return nil, WrapInvokeError("create chat completion", err)
	}
	a, _ := resolveAdapter(m.Provider)
	ca, _ := a.(ChatAdapter)
	resp, err := ca.ParseChatResponse(result.Status, result.Header, result.Body)
	return resp, normalizeErr(err)
}

// chatExecute is the shared Build→Overlay→Execute half of the chat pipeline.
func chatExecute(ctx context.Context, m *ModelConfig, opts *ChatOptions) (*RawResult, error) {
	a, err := resolveAdapter(m.Provider)
	if err != nil {
		return nil, err
	}
	ca, ok := a.(ChatAdapter) // dispatch lock #3 (design §6.2)
	if !ok {
		return nil, &ProviderError{
			Kind: ErrUnsupportedType, Message: "provider " + m.Provider + " does not implement chat",
		}
	}
	req, err := ca.BuildChatRequest(endpoint(m), m.ModelName, opts)
	if err != nil {
		return nil, ClassifyError(err)
	}
	applyCustomHeaders(req, m.CustomHeaders)
	key := ModelKey{ModelID: m.ModelID, ModelName: m.ModelName, Kind: ModelKindChat, ConcurrencyLimit: m.MaxConcurrency}
	return defaultExecutor.Do(ctx, key, req)
}

// ChatStream runs a streaming chat call and returns the session-level semantic
// stream (types.StreamResponse value channel) — caller semantics unchanged
// from v1. Adapter TranslateStreamEvent yields internal StreamEvents; the
// single mapping switch below carries no vendor knowledge.
func ChatStream(ctx context.Context, m *ModelConfig, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	start := time.Now()
	gen := startLangfuse(ctx, "chat.completion.stream", m, opts)
	streamOpts := *opts
	streamOpts.Stream = true
	streamOpts = *foldChatOptions(m, &streamOpts)
	result, err := chatExecute(ctx, m, &streamOpts) // slot held by the returned stream reader
	if err != nil {
		// v1 wrapInvokeError("create chat completion stream", …).
		err = WrapInvokeError("create chat completion stream", err)
	}
	gen.finish(nil, err)
	if err != nil {
		logLLMDebugStream(ctx, m.ModelName, opts.Messages, opts, "", nil, nil, err, time.Since(start))
		return nil, err
	}
	out := make(chan types.StreamResponse)
	// emit blocks on delivery but always yields to ctx cancellation, so a
	// consumer that abandons the stream can never wedge the producer (v1
	// concurrency_wrapper.go: drain + release on ctx.Done).
	emit := func(sr types.StreamResponse) {
		select {
		case out <- sr:
		case <-ctx.Done():
		}
	}
	streamDone := make(chan struct{})
	go func() {
		defer close(out)
		defer func() { _ = result.Stream.Close() }() // entry guarantees Close (design §6.3)
		// Releases the ctx watcher below when the producer finishes first —
		// a caller with a never-cancelled ctx (context.Background in backend
		// jobs) must not retain one goroutine per stream call.
		defer close(streamDone)
		// llm_debug stream record: one entry per call, accumulated across the
		// emitted chunks exactly as the v1 debugChat wrapper did, written
		// whenever the producer goroutine finishes (done / interrupted /
		// abandoned via ctx).
		var (
			streamContent   strings.Builder
			streamToolCalls []types.LLMToolCall
			streamUsage     *types.TokenUsage
			streamErr       error
		)
		observe := func(sr types.StreamResponse) {
			if sr.ResponseType == types.ResponseTypeAnswer && sr.Content != "" {
				streamContent.WriteString(sr.Content)
			}
			if sr.ResponseType == types.ResponseTypeError {
				streamErr = fmt.Errorf("%s", sr.Content)
			}
			if len(sr.ToolCalls) > 0 {
				streamToolCalls = sr.ToolCalls
			}
			if sr.Usage != nil {
				streamUsage = sr.Usage
			}
		}
		defer func() {
			logLLMDebugStream(ctx, m.ModelName, opts.Messages, opts,
				streamContent.String(), streamToolCalls, streamUsage, streamErr, time.Since(start))
		}()
		// Cancellation also unblocks a pending demux read via Close. The
		// streamDone branch keeps the watcher from outliving the stream.
		go func() {
			select {
			case <-ctx.Done():
				_ = result.Stream.Close()
			case <-streamDone:
			}
		}()
		demux := NewDemuxer(result.Header.Get("Content-Type"), result.Stream)
		state := NewStreamBridgeState()
		a, _ := resolveAdapter(m.Provider)
		ca, _ := a.(ChatAdapter)
		if ca == nil {
			// Registry can shrink (tests unregister); a leaked reader must
			// surface an explicit error chunk, never a nil deref.
			sr := types.StreamResponse{
				ResponseType: types.ResponseTypeError,
				Content:      "provider adapter unavailable: " + m.Provider,
				Done:         true,
			}
			observe(sr)
			emit(sr)
			return
		}
		// pendingUsage ports v1 streamState.usage (seam ②): a usage frame is
		// NOT a standalone client chunk — it rides the final Done chunk, or
		// the interrupted-stream error chunk when the stream breaks first.
		var pendingUsage *Usage
		for {
			chunk, ok := demux.Next()
			if !ok {
				if err := demux.Err(); err != nil && ctx.Err() == nil {
					interrupted := types.StreamResponse{
						ResponseType: types.ResponseTypeError,
						Content:      err.Error(),
						Done:         true,
						FinishReason: types.FinishReasonIncomplete,
					}
					// v1 streamState.buildOrderedToolCalls: the partial tool
					// calls assembled before the break still reach the client.
					if assembler := toolAssembler(state); assembler != nil {
						interrupted.ToolCalls = toLLMToolCalls(assembler.Calls())
					}
					if u := pendingUsage.usageToTypes(); u != nil {
						interrupted.Usage = u
					}
					observe(interrupted)
					emit(interrupted)
				}
				return
			}
			event, err := ca.TranslateStreamEvent(state, chunk)
			if err != nil {
				logger.Errorf(ctx, "translate stream event failed: %v", err)
				continue
			}
			if event == nil {
				continue
			}
			if event.Kind == StreamKindUsage {
				if event.Usage != nil {
					pendingUsage = event.Usage
				}
				continue
			}
			for _, sr := range mapStreamEvent(event) {
				if event.Done != nil {
					if pendingUsage != nil {
						sr.Usage = pendingUsage.usageToTypes()
					} else if event.Usage != nil {
						// usage-in-Done seam (anticipated by the anthropic
						// bridge's final event, activated for gemini P5-1):
						// the terminator frame carries the final usage itself
						// when no separate usage frame preceded it.
						sr.Usage = event.Usage.usageToTypes()
					}
					pendingUsage = nil
				}
				observe(sr)
				emit(sr)
			}
			if event.Done != nil {
				return
			}
		}
	}()
	return out, nil
}

// mapStreamEvent is the mechanical StreamEvent→types.StreamResponse mapping
// (design §6.4): one switch, Kind→ResponseType, no vendor knowledge.
func mapStreamEvent(e *StreamEvent) []types.StreamResponse {
	switch e.Kind {
	case StreamKindAnswer:
		if e.Done != nil {
			var out []types.StreamResponse
			// A final text fragment can ride the Done event (v1 carried
			// content+done in one StreamResponse chunk) — emit it first.
			if e.Delta != nil && e.Delta.Text != "" {
				out = append(out, types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Content:      e.Delta.Text,
				})
			}
			resp := types.StreamResponse{
				ResponseType: types.ResponseTypeAnswer,
				Done:         true,
				FinishReason: e.Done.FinishReason,
			}
			if e.Done.Incomplete {
				resp.FinishReason = types.FinishReasonIncomplete
			}
			resp.ToolCalls = toLLMToolCalls(e.Done.ToolCalls)
			return append(out, resp)
		}
		if e.Delta != nil {
			return []types.StreamResponse{{
				ResponseType: types.ResponseTypeAnswer,
				Content:      e.Delta.Text,
			}}
		}
	case StreamKindThinking:
		if e.Delta != nil {
			return []types.StreamResponse{{
				ResponseType: types.ResponseTypeThinking,
				Content:      e.Delta.Text,
			}}
		}
	case StreamKindToolCall:
		if e.ToolCallDelta != nil {
			return []types.StreamResponse{{
				ResponseType: types.ResponseTypeToolCall,
				ToolCalls:    toLLMToolCalls([]ToolCall{assembleDelta(e.ToolCallDelta)}),
			}}
		}
	case StreamKindUsage:
		if e.Usage != nil {
			return []types.StreamResponse{{ResponseType: types.ResponseTypeAnswer, Usage: e.Usage.usageToTypes()}}
		}
	case StreamKindError:
		msg := "provider error"
		if e.Delta != nil {
			msg = e.Delta.Text
		}
		resp := types.StreamResponse{ResponseType: types.ResponseTypeError, Content: msg, Done: true}
		if e.Done != nil {
			// A broken stream carries whatever the provider had assembled when
			// it broke: the partial tool calls and the finish reason ride along
			// (v1 semantics; the caller logs and reasons about a partial call).
			resp.ToolCalls = toLLMToolCalls(e.Done.ToolCalls)
			if e.Done.Incomplete {
				resp.FinishReason = types.FinishReasonIncomplete
			} else {
				resp.FinishReason = e.Done.FinishReason
			}
		}
		return []types.StreamResponse{resp}
	}
	return nil
}

func assembleDelta(d *ToolCallDelta) ToolCall {
	return ToolCall{ID: d.ID, Type: d.Type, Function: FunctionCall{Name: d.Name, Arguments: d.Arguments}}
}

func toLLMToolCalls(calls []ToolCall) []types.LLMToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]types.LLMToolCall, 0, len(calls))
	for _, c := range calls {
		out = append(out, types.LLMToolCall{
			ID:               c.ID,
			Type:             c.Type,
			Function:         types.FunctionCall{Name: c.Function.Name, Arguments: c.Function.Arguments},
			ProviderMetadata: c.ProviderMetadata,
		})
	}
	return out
}

// Embed runs one embedding call. Langfuse tracking and the llm_debug record
// mirror the v1 decorator stack (langfuseEmbedder outermost, debugEmbedder
// below it); vendors whose API yields one vector per request are fanned out
// per input (v1 volcengine loop), so callers always pass the full batch.
func Embed(ctx context.Context, m *ModelConfig, opts *EmbeddingOptions) (*EmbeddingResponse, error) {
	start := time.Now()
	gen := startEmbeddingLangfuse(ctx, m, opts)
	resp, err := embedWithFanOut(ctx, m, opts)
	gen.finishEmbedding(resp, opts.Inputs, err)
	logEmbeddingDebug(ctx, m.ModelName, opts, resp, err, time.Since(start))
	return resp, err
}

// embedWithFanOut dispatches one facet call, or — for SingleInputEmbedder
// vendors with a multi-input batch — one facet call per input, serially
// (v1 loop semantics; the sub-batch pooler above this entry already bounds
// provider burst), reassembling vectors in input order with summed usage.
func embedWithFanOut(ctx context.Context, m *ModelConfig, opts *EmbeddingOptions) (*EmbeddingResponse, error) {
	if len(opts.Inputs) <= 1 {
		return embedOnce(ctx, m, opts)
	}
	a, err := resolveAdapter(m.Provider)
	if err != nil {
		return nil, err
	}
	if _, single := a.(SingleInputEmbedder); !single {
		return embedOnce(ctx, m, opts)
	}
	vectors := make([][]float32, len(opts.Inputs))
	var total Usage
	for i, input := range opts.Inputs {
		one := &EmbeddingOptions{
			Inputs:                    []string{input},
			Dimensions:                opts.Dimensions,
			TruncatePromptTokens:      opts.TruncatePromptTokens,
			SupportsDimensionOverride: opts.SupportsDimensionOverride,
		}
		resp, err := embedOnce(ctx, m, one)
		if err != nil {
			return nil, err
		}
		if len(resp.Vectors) != 1 {
			return nil, &ProviderError{
				Kind: ErrProviderUpstream,
				Message: fmt.Sprintf("embedding: single-input vendor returned %d vectors for 1 input",
					len(resp.Vectors)),
			}
		}
		vectors[i] = resp.Vectors[0]
		total.PromptTokens += resp.Usage.PromptTokens
		total.CompletionTokens += resp.Usage.CompletionTokens
		total.TotalTokens += resp.Usage.TotalTokens
	}
	return &EmbeddingResponse{Vectors: vectors, Usage: total}, nil
}

func embedOnce(ctx context.Context, m *ModelConfig, opts *EmbeddingOptions) (*EmbeddingResponse, error) {
	return invokeFacet(ctx, m, ModelKindEmbedding,
		func(a Adapter) bool { _, ok := a.(EmbeddingAdapter); return ok },
		func(a Adapter, ep Endpoint) (*Request, error) {
			return a.(EmbeddingAdapter).BuildEmbeddingRequest(ep, m.ModelName, opts)
		}, func(a Adapter, r *RawResult) (*EmbeddingResponse, error) {
			return a.(EmbeddingAdapter).ParseEmbeddingResponse(r.Status, r.Header, r.Body)
		})
}

// Rerank runs one rerank call with the v1 decorator-stack equivalents
// (langfuse generation + llm_debug record).
func Rerank(ctx context.Context, m *ModelConfig, opts *RerankOptions) (*RerankResponse, error) {
	start := time.Now()
	gen := startRerankLangfuse(ctx, m, opts)
	resp, err := invokeFacet(ctx, m, ModelKindRerank,
		func(a Adapter) bool { _, ok := a.(RerankAdapter); return ok },
		func(a Adapter, ep Endpoint) (*Request, error) {
			return a.(RerankAdapter).BuildRerankRequest(ep, m.ModelName, opts)
		}, func(a Adapter, r *RawResult) (*RerankResponse, error) {
			return a.(RerankAdapter).ParseRerankResponse(r.Status, r.Header, r.Body)
		})
	gen.finishRerank(resp, opts, err)
	logRerankDebug(ctx, m.ModelName, opts, resp, err, time.Since(start))
	return resp, err
}

// Transcribe runs one ASR call with the v1 langfuse equivalent (v1 ASR had
// no llm_debug wrapper).
func Transcribe(ctx context.Context, m *ModelConfig, opts *ASROptions) (*ASRResponse, error) {
	gen := startASRLangfuse(ctx, m, opts)
	resp, err := invokeFacet(ctx, m, ModelKindASR,
		func(a Adapter) bool { _, ok := a.(ASRAdapter); return ok },
		func(a Adapter, ep Endpoint) (*Request, error) {
			return a.(ASRAdapter).BuildASRRequest(ep, m.ModelName, opts)
		}, func(a Adapter, r *RawResult) (*ASRResponse, error) {
			return a.(ASRAdapter).ParseASRResponse(r.Status, r.Header, r.Body)
		})
	gen.finishASR(resp, err)
	return resp, err
}

// invokeFacet is the shared Build→Execute→Parse pipeline for the simple
// facets (embedding/rerank/ASR) which differ only in their option/response
// types.
func invokeFacet[R any](
	ctx context.Context, m *ModelConfig, kind ModelKind,
	facet func(Adapter) bool,
	build func(Adapter, Endpoint) (*Request, error),
	parse func(Adapter, *RawResult) (*R, error),
) (*R, error) {
	a, err := resolveAdapter(m.Provider)
	if err != nil {
		return nil, err
	}
	// Dispatch lock #3 (design §6.2): the facet assertion is guarded — a
	// registered adapter without this facet yields ErrUnsupportedType, never
	// a panic (e.g. a rerank model record on a chat-only provider; P3 review
	// finding 3).
	if !facet(a) {
		return nil, &ProviderError{
			Kind:    ErrUnsupportedType,
			Message: "provider " + m.Provider + " does not implement the " + string(kind) + " facet",
		}
	}
	req, err := build(a, endpoint(m))
	if err != nil {
		return nil, ClassifyError(err)
	}
	applyCustomHeaders(req, m.CustomHeaders)
	key := ModelKey{ModelID: m.ModelID, ModelName: m.ModelName, Kind: kind, ConcurrencyLimit: m.MaxConcurrency}
	result, err := defaultExecutor.Do(ctx, key, req)
	if err != nil {
		return nil, err
	}
	resp, err := parse(a, result)
	return resp, normalizeErr(err)
}

// normalizeErr enforces the error contract (design §6.5): Parse* results may
// only be a response or a *ProviderError; any bare error falls back to
// ErrProviderUpstream and is logged so nothing leaks past the contract.
func normalizeErr(err error) error {
	if err == nil {
		return nil
	}
	var pe *ProviderError
	if asProviderError(err, &pe) {
		return pe
	}
	logger.Errorf(context.Background(), "adapter returned non-ProviderError (coerced to provider_upstream): %v", err)
	return &ProviderError{Kind: ErrProviderUpstream, Message: err.Error(), Err: err}
}

func asProviderError(err error, target **ProviderError) bool {
	for err != nil {
		if pe, ok := err.(*ProviderError); ok {
			*target = pe
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// applyCustomHeaders overlays user custom headers after Build (design §6.4):
// user headers MAY override adapter defaults (e.g. a self-hosted gateway
// changing Content-Type), EXCEPT ① adapter-declared protected headers
// (signature/multipart-boundary critical), ② transport management headers,
// and ③ vendor auth headers (isAuthHeader, always protected — v1
// reservedHeaderKeys behavior).
func applyCustomHeaders(req *Request, custom map[string]string) {
	if len(custom) == 0 {
		return
	}
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	protected := make(map[string]struct{}, len(req.ProtectedHeaders))
	for _, h := range req.ProtectedHeaders {
		protected[strings.ToLower(strings.TrimSpace(h))] = struct{}{}
	}
	for k, v := range custom {
		name := strings.TrimSpace(k)
		if name == "" || isTransportHeader(name) || isAuthHeader(name) {
			continue
		}
		if _, keepOut := protected[strings.ToLower(name)]; keepOut {
			continue
		}
		req.Header.Set(name, v)
	}
}

// isAuthHeader is the always-protected vendor auth set (v1
// reservedHeaderKeys minus content-type — P2 review finding 3): the entry is
// the single funnel for every facet, so adapters cannot forget to declare
// their own auth header (anthropic/weknoracloud keep explicit
// ProtectedHeaders for vendor-specific signature headers). Content-Type is
// deliberately overridable (design §6.4 self-hosted-gateway ruling).
func isAuthHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "api-key", "x-api-key", "x-goog-api-key":
		return true
	}
	return false
}

// isTransportHeader covers the hop-by-hop / transport-management set formerly
// handled by reservedHeaderKeys (v1 internal/utils/extraheaders.go).
func isTransportHeader(name string) bool {
	switch strings.ToLower(name) {
	case "host", "content-length", "connection", "transfer-encoding", "accept-encoding":
		return true
	}
	return false
}

// isMultimodalNotSupportedError reports whether the error indicates the model
// does not accept image input (v1 image_resolve.go semantics).
func isMultimodalNotSupportedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return (strings.Contains(msg, "multimodal") || strings.Contains(msg, "image") || strings.Contains(msg, "vision")) &&
		(strings.Contains(msg, "not support") || strings.Contains(msg, "unsupported") || strings.Contains(msg, "400"))
}

// ImageDataURI encodes raw image bytes as a base64 data URI for an
// ImageRef.URL (design §6.6: image base64-inlining is single-homed in the
// entry's preprocessing). MIME is sniffed from the bytes; undetectable
// payloads fall back to image/png (v1 vlm.detectImageMIME semantics).
func ImageDataURI(data []byte) string {
	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(mimeType, "image/") {
		mimeType = "image/png"
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}
