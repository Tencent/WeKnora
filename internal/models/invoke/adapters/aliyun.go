package adapters

// aliyun.go — the aliyun (阿里云百炼/DashScope) vendor adapter. 2026-09-12
// user ruling: aliyun speaks the vendor's OWN interface — the native
// DashScope generation wire — not the OpenAI-compatible mode (the native
// side of 裁定 #31: compatibility routes are for vendors whose own protocol
// IS OpenAI-shaped; DashScope has a first-class native one).
//
// Wire map (all paths under the DashScope root):
//   - chat (text):      POST /api/v1/services/aigc/text-generation/generation
//   - chat (vision):    POST /api/v1/services/aigc/multimodal-generation/generation
//                       (native content parts [{"image":...},{"text":...}] —
//                       NOT the OpenAI image_url shape)
//   - embedding (text): POST /api/v1/services/embeddings/text-embedding/text-embedding
//   - embedding (VL):   POST /api/v1/services/embeddings/multimodal-embedding/multimodal-embedding
//   - rerank:           POST /api/v1/services/rerank/text-rerank/text-rerank
//   - listing:          GET  /api/v1/models?capabilities=TG
//
// Streaming sets the X-DashScope-SSE: enable header and
// parameters.incremental_output=true. There is NO [DONE] sentinel: the final
// frame carries finish_reason (+usage) and closes the stream, so the bridge
// emits the Done event itself on that frame.
//
// Deliberate deltas vs the compatible-mode era:
//   - explicit context cache rides the NATIVE wire (2026-09-13 显式缓存方案):
//     `cache_control:{"type":"ephemeral"}` markers on the first system
//     message + the last conversation message, gated to the documented
//     explicit-cache model list (aliyunSupportsExplicitCache). Tool
//     definitions take NO marker (the native wire ignores them) and no TTL
//     is sent (fixed 5-minute validity, refreshed on each hit — the
//     CacheRetention long/short distinction collapses on DashScope).
//   - frequency_penalty is not part of the native schema and is dropped.
//   - ThinkingControl override tokens: only "none" and "enable_thinking"
//     map; anything else falls back to the model-conditional default (the
//     compat wrapper shapes don't exist on the native wire).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/types"
)

// --- adapter + registration ---

// AliyunAdapter serves aliyun over the native DashScope wire: chat (text +
// vision), embedding (text + multimodal), rerank, and the native catalog
// listing.
type AliyunAdapter struct {
	name invoke.ProviderName
}

// compile-time lock #1 (design §6.2).
var (
	_ invoke.ChatAdapter       = (*AliyunAdapter)(nil)
	_ invoke.EmbeddingAdapter  = (*AliyunAdapter)(nil)
	_ invoke.RerankAdapter     = (*AliyunAdapter)(nil)
	_ invoke.ListModelsAdapter = (*AliyunAdapter)(nil)
)

func init() {
	if err := invoke.Default.Register(newAliyunAdapter()); err != nil {
		panic(err) // three-lock #2 fail fast
	}
}

func newAliyunAdapter() *AliyunAdapter {
	return &AliyunAdapter{name: invoke.ProviderAliyun}
}

// Provider returns the canonical provider name.
func (a *AliyunAdapter) Provider() string { return string(a.name) }

// Capabilities reports the served shards (chat/embedding/rerank/listing).
// Registration lock #2: shard set == facet set.
func (a *AliyunAdapter) Capabilities() invoke.Capabilities {
	caps := invoke.Capabilities{
		Common: invoke.CommonCaps{ModelListing: invoke.ModelListingCaps{Supported: true}},
		Chat:   chatCapsFor(a.name),
	}
	if info, ok := providerInfoFor(a.name); ok {
		eff := info.EffectiveCapabilities()
		caps.Embedding = eff.Embedding
		caps.Rerank = eff.Rerank
	}
	return caps
}

// aliyunNativeBaseURL normalizes any stored base onto the DashScope root:
// empty → the official root; a compatible-mode PATH (the pre-native default,
// still on existing records) is cut back to the root; an /api/v1 suffix (the
// SDK-style base) is trimmed so path joining doesn't double it. The strip
// runs ONLY on the URL path (url.Parse) — a host that merely CONTAINS the
// substring (e.g. https://compatible-mode.example.com) passes through
// untouched; a naive whole-URL strip would redirect the request (and its
// Bearer key) to a bogus host.
func aliyunNativeBaseURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return invoke.AliyunBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return base // 不动原始输入，交给执行层的 URL/SSRF 校验报错
	}
	path := u.Path
	if path == "/compatible-mode" || strings.HasPrefix(path, "/compatible-mode/") {
		path = "/"
	}
	u.Path = strings.TrimSuffix(path, "/api/v1")
	return strings.TrimRight(u.String(), "/")
}

// --- chat: the native generation wire ---

const (
	aliyunTextGenerationPath       = "/api/v1/services/aigc/text-generation/generation"
	aliyunMultimodalGenerationPath = "/api/v1/services/aigc/multimodal-generation/generation"
)

// aliyunPart is one native content part: {"text": ...} or {"image": ...}.
type aliyunPart struct {
	Text  string `json:"text,omitempty"`
	Image string `json:"image,omitempty"`
	// CacheControl turns the part into an explicit-cache breakpoint
	// ({"type":"ephemeral"}, 对话 API 文档 §cache_control). nil = no marker.
	CacheControl *aliyunCacheControl `json:"cache_control,omitempty"`
}

// aliyunCacheControl is the native explicit-cache marker. The wire accepts
// ONLY {"type":"ephemeral"} — no ttl field (fixed 5-minute validity, reset on
// each hit), unlike the anthropic-style marker the compat funnel used to send.
type aliyunCacheControl struct {
	Type string `json:"type"`
}

type aliyunFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// aliyunToolCall mirrors the native tool_call object (OpenAI-shaped on the
// wire; index is response/stream-side, omitted on history replay).
type aliyunToolCall struct {
	ID       string             `json:"id,omitempty"`
	Index    int                `json:"index,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function aliyunFunctionCall `json:"function"`
}

// aliyunMessage is one native input message. Content is a plain string on
// the text path and a part array on the vision path (DashScope content
// rules) — `any` keeps both in one struct.
type aliyunMessage struct {
	Role             string           `json:"role"`
	Content          any              `json:"content"`
	ToolCalls        []aliyunToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
}

type aliyunResponseFormat struct {
	Type string `json:"type"`
}

type aliyunFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type aliyunToolDef struct {
	Type     string             `json:"type"`
	Function *aliyunFunctionDef `json:"function"`
}

type aliyunToolChoiceFunction struct {
	Name string `json:"name"`
}

type aliyunToolChoice struct {
	Type     string                   `json:"type"`
	Function aliyunToolChoiceFunction `json:"function"`
}

// aliyunParameters is the native parameters object. DashScope validates
// strictly, so every field is omitted unless the platform decided it.
type aliyunParameters struct {
	ResultFormat        string  `json:"result_format,omitempty"`
	IncrementalOutput   bool    `json:"incremental_output,omitempty"`
	MaxCompletionTokens int     `json:"max_completion_tokens,omitempty"`
	MaxTokens           int     `json:"max_tokens,omitempty"`
	Temperature         float64 `json:"temperature,omitempty"`
	TopP                float64 `json:"top_p,omitempty"`
	// PresencePenalty/Seed follow the platform's value-type ChatOptions
	// contract ("zero = do not send"): the native wire documents
	// presence_penalty [-2,2] (negatives raise repetition) and seed
	// [0, 2³¹−1], but a negative penalty and seed=0 are unreachable until
	// that contract grows opt-in signaling — a platform-level item, shared
	// with the openai funnel (openai_wire.go gates on >0 the same way).
	PresencePenalty   float64               `json:"presence_penalty,omitempty"`
	Seed              int                   `json:"seed,omitempty"`
	EnableThinking    *bool                 `json:"enable_thinking,omitempty"`
	ResponseFormat    *aliyunResponseFormat `json:"response_format,omitempty"`
	Tools             []aliyunToolDef       `json:"tools,omitempty"`
	ToolChoice        any                   `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool                 `json:"parallel_tool_calls,omitempty"`
}

type aliyunInput struct {
	Messages []aliyunMessage `json:"messages"`
}

// aliyunGenerationRequest is the native envelope: model + input +
// parameters — NOT the OpenAI flat shape.
type aliyunGenerationRequest struct {
	Model      string            `json:"model"`
	Input      aliyunInput       `json:"input"`
	Parameters *aliyunParameters `json:"parameters"`
}

// convertAliyunMessages maps the neutral messages onto the native content
// rules. The text path degrades single-text messages to plain strings and
// uses part arrays only for multi-part text; the vision path uses part
// arrays with {"image": ...} refs (pure-text messages stay strings).
// Assistant reasoning_content replays for strict multi-turn exactly as the
// openai funnel replays it (qwen3.8 preserve_thinking requires it intact).
func convertAliyunMessages(messages []invoke.Message, vision bool) []aliyunMessage {
	out := make([]aliyunMessage, 0, len(messages))
	for _, msg := range messages {
		m := aliyunMessage{Role: string(msg.Role)}
		if len(msg.ToolCalls) > 0 {
			m.ToolCalls = make([]aliyunToolCall, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				m.ToolCalls = append(m.ToolCalls, aliyunToolCall{
					ID:       tc.ID,
					Type:     tc.Type,
					Function: aliyunFunctionCall{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
				})
			}
		}
		if msg.Role == "tool" {
			m.ToolCallID = msg.ToolCallID
		}
		if msg.Role == "assistant" {
			m.ReasoningContent = msg.ReasoningContent
		}
		m.Content = aliyunMessageContent(msg, vision)
		out = append(out, m)
	}
	return out
}

// aliyunMessageContent encodes one message's parts per the path rules.
// Empty text parts are skipped — a part with both fields empty serializes
// to {} and DashScope's strict validation rejects it.
func aliyunMessageContent(msg invoke.Message, vision bool) any {
	if vision {
		parts := make([]aliyunPart, 0, len(msg.Content))
		for _, p := range msg.Content {
			if p.Image != nil {
				parts = append(parts, aliyunPart{Image: p.Image.URL})
				continue
			}
			if p.Text == "" {
				continue
			}
			parts = append(parts, aliyunPart{Text: p.Text})
		}
		switch {
		case len(parts) == 0:
			return ""
		case len(parts) == 1 && parts[0].Image == "":
			// Pure-text messages (system prompts, tool results) accept plain
			// strings everywhere on the vision path.
			return parts[0].Text
		default:
			return parts
		}
	}
	texts := make([]string, 0, len(msg.Content))
	for _, p := range msg.Content {
		if p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	switch len(texts) {
	case 1:
		return texts[0]
	case 0:
		return ""
	default:
		parts := make([]aliyunPart, 0, len(texts))
		for _, t := range texts {
			parts = append(parts, aliyunPart{Text: t})
		}
		return parts
	}
}

// --- 模型名谓词（2026-09-13 自 providers.go 迁入并私有化：唯一消费者是本
// 适配器，平台层不留厂商知识；providers.go 保留的是 openai funnel 共享系） ---

// aliyunIsQwenThinkingModel 检查模型名是否为支持思维链的 Qwen 模型
// （qwen3/plus/max/turbo 前缀）——enable_thinking alwaysSend + 非流式钉 false。
func aliyunIsQwenThinkingModel(modelName string) bool {
	lowerName := strings.ToLower(modelName)
	return strings.HasPrefix(lowerName, "qwen3") ||
		strings.HasPrefix(lowerName, "qwen-plus") ||
		strings.HasPrefix(lowerName, "qwen-max") ||
		strings.HasPrefix(lowerName, "qwen-turbo")
}

// aliyunIsDashScopeHybridThinkingModel 检查 DashScope 托管的混合思考模型
// （enable_thinking 适用面，对话 API 文档 §enable_thinking，2026-09-12 裁定④扩容）：
// Qwen 思考族之外，还包括 DeepSeek-V4/V3.2/V3.1 系列（含 siliconflow/ 直供
// 前缀）、Kimi-K2.6/K2.5 系列（含 kimi/ 直供前缀）、GLM 系列（阿里云直供 glm-*
// 与智谱直供 ZHIPU/GLM-*）。子串匹配以同时覆盖直供前缀形态。
func aliyunIsDashScopeHybridThinkingModel(modelName string) bool {
	if aliyunIsQwenThinkingModel(modelName) {
		return true
	}
	lower := strings.ToLower(modelName)
	return strings.Contains(lower, "deepseek-v4") ||
		strings.Contains(lower, "deepseek-v3.2") ||
		strings.Contains(lower, "deepseek-v3.1") ||
		strings.Contains(lower, "kimi-k2.6") ||
		strings.Contains(lower, "kimi-k2.5") ||
		strings.Contains(lower, "glm-") ||
		strings.Contains(lower, "zhipu/glm")
}

// aliyunIsDashScopeAlwaysThinkingModel 检查「始终开启思考」的 DashScope 模型：
// enable_thinking 仅支持 true，传入 false 会导致 API 请求失败（文档原文）。
// 覆盖智谱直供 ZHIPU/GLM-5.3(-Flash) 与 kimi-k3（含 kimi/kimi-k3 直供前缀）。
// 这类模型不发 enable_thinking（服务端默认即开）——模型级 CanDisable=false
// 的运行时落点；目录（models.json）预填时同样标 can_disable=false。
func aliyunIsDashScopeAlwaysThinkingModel(modelName string) bool {
	lower := strings.ToLower(modelName)
	return strings.Contains(lower, "zhipu/glm-5.3") ||
		strings.Contains(lower, "kimi-k3")
}

// aliyunSupportsExplicitCache 检查模型是否在 DashScope 显式缓存（上下文缓存）
// 的文档支持名单内（官方「上下文缓存」页，北京地域快照，2026-09-13 方案 §三.3）：
// qwen3.8-/3.7-/3.6- 新代系、qwen3-max、qwen3-coder、qwen3-vl-plus 前缀；
// 第三方 deepseek-v3.2、kimi-k2.5/2.6/2.7、glm-5.1。名单随地域漂移（美区
// 带后缀变体）且厂商会扩容，保守起见只放文档点名的型号——不在名单内的
// 模型不发 cache_control：未文档化的不支持行为不可依赖。真机验证后再放宽。
func aliyunSupportsExplicitCache(model string) bool {
	lower := strings.ToLower(model)
	for _, prefix := range []string{
		"qwen3.8-", "qwen3.7-", "qwen3.6-", "qwen3-max", "qwen3-coder", "qwen3-vl-plus",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return strings.Contains(lower, "deepseek-v3.2") ||
		strings.Contains(lower, "kimi-k2.5") ||
		strings.Contains(lower, "kimi-k2.6") ||
		strings.Contains(lower, "kimi-k2.7") ||
		strings.Contains(lower, "glm-5.1")
}

// applyAliyunThinking ports the qwenThinkingProvider semantics onto the
// native parameters object, extended per the 2026-09-12 ruling ④ to the
// full DashScope hybrid family (enable_thinking 适用面, see
// aliyunIsDashScopeHybridThinkingModel):
//   - qwen thinking family: enable_thinking on EVERY request (v1
//     alwaysSend), pinned false on non-stream calls (Qwen3 rejects thinking
//     in non-stream mode);
//   - the non-Qwen hybrids (deepseek-v4/v3.2/v3.1, kimi-k2.6/k2.5, glm):
//     the field rides ONLY an explicit platform decision (Thinking != nil
//     or the ThinkingControl override) — no alwaysSend, no non-stream pin
//     (those constraints are Qwen-specific);
//   - always-on models (ZHIPU/GLM-5.3*, kimi-k3 — CanDisable=false): the
//     field is NEVER sent; the vendor rejects enable_thinking=false
//     outright and the server default is thinking-on.
//
// ThinkingControl override tokens: "none" suppresses the field,
// "enable_thinking" forces the strategy on any model; anything else falls
// back to the model-conditional default (documented delta above).
func applyAliyunThinking(
	params *aliyunParameters, control string, model string, opts *invoke.ChatOptions, isStream bool,
) {
	if aliyunIsDashScopeAlwaysThinkingModel(model) {
		return // 始终思考族：不发字段（false 必被拒，默认即开）
	}
	qwen := aliyunIsQwenThinkingModel(model)
	hybrid := qwen || aliyunIsDashScopeHybridThinkingModel(model)
	switch control {
	case "none":
		return
	case "enable_thinking":
		hybrid = true
	}
	if !hybrid {
		return
	}
	// 非 qwen 混合族仅在显式决策时发声（Thinking 或 override）。
	if !qwen && (opts == nil || opts.Thinking == nil) && control != "enable_thinking" {
		return
	}
	thinking := false
	if opts != nil && opts.Thinking != nil {
		thinking = *opts.Thinking
	}
	if qwen && !isStream {
		thinking = false
	}
	params.EnableThinking = &thinking
}

// appendAliyunSchemaHint appends the structured-output hint to the last
// message (same semantics as the openai funnel's json_object hint).
func appendAliyunSchemaHint(msg *aliyunMessage, schema string) {
	hint := fmt.Sprintf("\nUse this JSON schema: %s", schema)
	if s, ok := msg.Content.(string); ok {
		msg.Content = s + hint
		return
	}
	if parts, ok := msg.Content.([]aliyunPart); ok && len(parts) > 0 {
		parts[len(parts)-1].Text += hint
	}
}

// applyAliyunCacheBreakpoints injects explicit-cache markers onto the typed
// request (2026-09-13 显式缓存方案 — the native-wire successor of the retired
// compat-mode JSON rewrite in openai_wire.go). Placement: the first system
// message + the last non-system message — two breakpoints, under the native
// 4-marker cap, and the trailing one advances turn-by-turn over append-only
// agent histories so the stable prefix keeps hitting. Tool definitions are
// deliberately NOT marked (the native wire ignores markers there), and no
// TTL rides the marker (server-fixed 5-minute validity: CacheRetentionLong
// degrades to the same rolling window on DashScope).
//
// Gating: CacheRetentionNone opts out (one-shot compaction summaries), and
// only documented explicit-cache models are marked — the wire behavior for
// unsupported models is undocumented, so it is never exercised. Runs AFTER
// appendAliyunSchemaHint so the marker lands on the final body form.
func applyAliyunCacheBreakpoints(msgs []aliyunMessage, model string, retention string) {
	if retention == invoke.CacheRetentionNone || !aliyunSupportsExplicitCache(model) {
		return
	}
	marker := &aliyunCacheControl{Type: "ephemeral"}
	for i := range msgs {
		if msgs[i].Role == "system" && markAliyunMessage(&msgs[i], marker) {
			break
		}
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "system" && markAliyunMessage(&msgs[i], marker) {
			return
		}
	}
}

// markAliyunMessage rewrites one message's content to carry the marker and
// reports whether anything was marked. A plain string becomes a one-part
// array (the explicit-cache convention reads markers off content parts); a
// part array takes the marker on its last non-empty part. Empty content
// returns false so the caller falls back to an earlier message.
func markAliyunMessage(msg *aliyunMessage, marker *aliyunCacheControl) bool {
	switch content := msg.Content.(type) {
	case string:
		if content == "" {
			return false
		}
		msg.Content = []aliyunPart{{Text: content, CacheControl: marker}}
		return true
	case []aliyunPart:
		for i := len(content) - 1; i >= 0; i-- {
			if content[i].Text != "" || content[i].Image != "" {
				content[i].CacheControl = marker
				return true
			}
		}
		return false
	default:
		return false
	}
}

// aliyunSupportsTools reports whether the model family accepts function
// calling. Per the generation doc, tools 适用于除 qwen-vl / qwen-audio 系列
// 外的全部模型 — including vision-capable NEW-generation models driven
// through the multimodal-generation endpoint (the official tools example
// runs qwen3.8-max there), so the gate is by model NAME, not by whether the
// request carries images.
func aliyunSupportsTools(model string) bool {
	lower := strings.ToLower(model)
	return !strings.Contains(lower, "qwen-vl") && !strings.Contains(lower, "qwen-audio")
}

// BuildChatRequest builds the native generation call. The vision branch
// (any image part) targets multimodal-generation with native content parts;
// the text branch targets text-generation. Streaming rides the
// X-DashScope-SSE header — there is no parameters.stream on the HTTP path.
func (a *AliyunAdapter) BuildChatRequest(
	ep invoke.Endpoint, model string, opts *invoke.ChatOptions,
) (*invoke.Request, error) {
	isStream := opts != nil && opts.Stream
	vision := opts != nil && invoke.HasImages(opts.Messages)

	params := &aliyunParameters{ResultFormat: "message"}
	var messages []invoke.Message
	if opts != nil {
		messages = opts.Messages
		params.Temperature = opts.Temperature
		if opts.TopP > 0 {
			params.TopP = opts.TopP
		}
		if opts.PresencePenalty > 0 {
			params.PresencePenalty = opts.PresencePenalty
		}
		if opts.Seed != 0 {
			params.Seed = opts.Seed
		}
		// 完成预算（平台单一字段 MaxCompletionTokens）：思考模型（qwen3/plus/
		// max/turbo 前缀——均为文档标注支持 max_completion_tokens 的新一代）
		// 发同名参数；其余文本模型与视觉分支发全量支持的 max_tokens（原生
		// 严格校验下，老模型对 max_completion_tokens 会 400 或静默忽略）。
		if opts.MaxCompletionTokens > 0 {
			switch {
			case vision, !aliyunIsQwenThinkingModel(model):
				params.MaxTokens = opts.MaxCompletionTokens
			default:
				params.MaxCompletionTokens = opts.MaxCompletionTokens
			}
		}
		if len(opts.Tools) > 0 && aliyunSupportsTools(model) {
			params.Tools = make([]aliyunToolDef, 0, len(opts.Tools))
			for _, tool := range opts.Tools {
				params.Tools = append(params.Tools, aliyunToolDef{
					Type: "function",
					Function: &aliyunFunctionDef{
						Name:        tool.Name,
						Description: tool.Description,
						Parameters:  tool.Parameters,
					},
				})
			}
			if opts.ParallelToolCalls != nil {
				params.ParallelToolCalls = opts.ParallelToolCalls
			}
			if opts.ToolChoice != "" {
				switch opts.ToolChoice {
				case "none", "auto":
					params.ToolChoice = opts.ToolChoice
				case "required":
					// 裁定⑥（2026-09-12）：DashScope 只有 auto/none/{function}
					// 三种取值，required 无等价物——不传（服务端默认 auto），
					// 比 400 或语义弱化都安全。
				default:
					params.ToolChoice = aliyunToolChoice{
						Type:     "function",
						Function: aliyunToolChoiceFunction{Name: opts.ToolChoice},
					}
				}
			}
		}
		// 结构化输出（裁定 B4，2026-09-13）：v1 兼容层对带图请求照发
		// response_format + schema hint（openai_request.go:172-177 无 vision
		// 门控——v1 时代阿里云文本/视觉同走 compatible-mode 单端点）；官方
		// 文档 §请求体 的 parameters 表为两端点共用，response_format
		// （json_object/json_schema）无 VL 排除条款且明示"放入 parameters"。
		// 原生切换时加的 !vision 门控无文档依据，恢复 v1 parity——视觉分支
		// 照发；若个别 VL 型号拒收，真机清单（方案 doc §六.7）兜底。
		if len(opts.Format) > 0 {
			params.ResponseFormat = &aliyunResponseFormat{Type: "json_object"}
		}
		applyAliyunThinking(params, ep.ThinkingControl, model, opts, isStream)
		// 对话 API 文档：「思考模式的模型不支持强制调用某个工具」——
		// enable_thinking=true 与具名 tool_choice 同发必 400（2026-09-13
		// 裁定 B3）。仅在真正发 true 时降级：非流式钉 false / 始终思考族
		// 不发字段的组合不受影响。
		if params.EnableThinking != nil && *params.EnableThinking {
			if _, named := params.ToolChoice.(aliyunToolChoice); named {
				params.ToolChoice = "auto"
			}
		}
	}
	if isStream {
		params.IncrementalOutput = true
	}

	msgs := convertAliyunMessages(messages, vision)
	// schema hint 恢复视觉分支（裁定 B4）：纯文本追加，appendAliyunSchemaHint
	// 对 part 数组形态已处理（末个 part 追加），与 response_format 无耦合。
	if opts != nil && len(opts.Format) > 0 && len(msgs) > 0 {
		appendAliyunSchemaHint(&msgs[len(msgs)-1], string(opts.Format))
	}
	applyAliyunCacheBreakpoints(msgs, model, resolveCacheRetention(opts))

	data, err := json.Marshal(aliyunGenerationRequest{
		Model:      model,
		Input:      aliyunInput{Messages: msgs},
		Parameters: params,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	header.Set("Authorization", "Bearer "+ep.Credentials.APIKey)
	path := aliyunTextGenerationPath
	if vision {
		path = aliyunMultimodalGenerationPath
	}
	if isStream {
		header.Set("X-DashScope-SSE", "enable")
		header.Set("Accept", "text/event-stream")
	} else {
		header.Set("Accept", "application/json")
	}
	return &invoke.Request{
		Method: http.MethodPost,
		URL:    aliyunNativeBaseURL(ep.BaseURL) + path,
		Header: header,
		Body:   data,
		Stream: isStream,
	}, nil
}

// --- chat parse: native response objects ---

type aliyunUsage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails struct {
		// cached_tokens（命中 Cache 的 Token 数）挂在 prompt_tokens_details
		// 下——input_tokens_details 里是 text/image/video_tokens 细分。
		CachedTokens int `json:"cached_tokens"`
		// cache_creation 是嵌套对象（对话 API 文档 §usage 字段表：对象行 +
		// 后随字段表），cache_creation_input_tokens 挂在其下——顶层直读
		// 恒解不出，CacheWriteTokens 会静默归零（2026-09-13 审查发现）。
		CacheCreation struct {
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"cache_creation"`
	} `json:"prompt_tokens_details"`
	// 顶层回落：文档的嵌套解读待真机核对（方案 §六.2），核对前双读兜底，
	// 防个别版本/网关扁平化输出——核对后收敛为单路径。
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

func (u *aliyunUsage) usage() invoke.Usage {
	out := invoke.Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.TotalTokens,
	}
	if u.PromptTokensDetails.CachedTokens > 0 {
		out.CacheReadTokens = u.PromptTokensDetails.CachedTokens
		out.CacheReported = true
	}
	cacheWrite := u.PromptTokensDetails.CacheCreation.CacheCreationInputTokens
	if cacheWrite == 0 {
		cacheWrite = u.CacheCreationInputTokens
	}
	if cacheWrite > 0 {
		out.CacheWriteTokens = cacheWrite
		out.CacheReported = true
	}
	return out
}

type aliyunResponseMessage struct {
	Content          json.RawMessage  `json:"content"`
	ReasoningContent string           `json:"reasoning_content"`
	ToolCalls        []aliyunToolCall `json:"tool_calls"`
}

// aliyunContentText decodes message content: a plain string on the text
// path or a part array on the vision path (text parts joined).
func aliyunContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []aliyunPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}

// ParseChatResponse decodes the native message-format response: first
// choice, reasoning_content (the multi-turn replay channel — the compat
// parse dropped it), tool calls, and the input/output/total_tokens usage
// (cached_tokens folds into the prompt-cache detail).
func (a *AliyunAdapter) ParseChatResponse(_ int, _ http.Header, body []byte) (*invoke.ChatResponse, error) {
	var resp struct {
		Output struct {
			Choices []struct {
				FinishReason string                `json:"finish_reason"`
				Message      aliyunResponseMessage `json:"message"`
			} `json:"choices"`
		} `json:"output"`
		Usage *aliyunUsage `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, invoke.ClassifyError(fmt.Errorf("decode response: %w", err))
	}
	if len(resp.Output.Choices) == 0 {
		return nil, invoke.ClassifyError(fmt.Errorf("no response from API"))
	}
	choice := resp.Output.Choices[0]
	out := &invoke.ChatResponse{
		Content:          aliyunContentText(choice.Message.Content),
		FinishReason:     choice.FinishReason,
		ReasoningContent: choice.Message.ReasoningContent,
	}
	if resp.Usage != nil {
		out.Usage = resp.Usage.usage()
	}
	if len(choice.Message.ToolCalls) > 0 {
		out.ToolCalls = make([]invoke.ToolCall, 0, len(choice.Message.ToolCalls))
		for _, tc := range choice.Message.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, invoke.ToolCall{
				ID:       tc.ID,
				Type:     tc.Type,
				Function: invoke.FunctionCall{Name: tc.Function.Name, Arguments: tc.Function.Arguments},
			})
		}
	}
	return out, nil
}

// --- chat stream: the DashScope SSE bridge ---
// Native SSE frames are complete response objects (result_format=message +
// incremental_output=true): each frame's message fields ARE the deltas, the
// final frame carries finish_reason + usage, and there is no [DONE]
// sentinel — the bridge emits Done itself on the finish frame (usage rides
// the same event; the entry's usage-in-Done seam carries it through).

type aliyunStreamFrame struct {
	Output struct {
		Choices []struct {
			FinishReason string                `json:"finish_reason"`
			Message      aliyunResponseMessage `json:"message"`
		} `json:"choices"`
	} `json:"output"`
	Usage *aliyunUsage `json:"usage"`
	// Native in-band error envelope: {"code","message","request_id"} with an
	// empty output (HTTP stays 200 — streaming requests fail in-band, not at
	// the status line). Without these fields the frame decoded all-zero and
	// the bridge dropped it: a failing stream closed with ZERO client-visible
	// events (2026-09-14 glm-5.2 report).
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

const (
	stateAliyunFinish = "aliyun.finish_reason"
	stateAliyunUsage  = "aliyun.usage"
)

// isAliyunDoneSentinel tolerates proxies that inject a bare "data:[DONE]"
// frame (no SSE event name) into the native stream — same posture as the
// openai bridge's isDoneSentinel.
func isAliyunDoneSentinel(data []byte) bool {
	return string(bytes.TrimSpace(data)) == "[DONE]"
}

// TranslateStreamEvent implements the DashScope native bridge.
func (a *AliyunAdapter) TranslateStreamEvent(
	state *invoke.StreamBridgeState, chunk invoke.StreamChunk,
) ([]*invoke.StreamEvent, error) {
	if chunk.Event == "done" || isAliyunDoneSentinel(chunk.Data) {
		// No native sentinel; tolerate proxies that inject one — flush the
		// accumulated finish state (mirrors the openai bridge semantics).
		return []*invoke.StreamEvent{aliyunFlushDone(state, nil)}, nil
	}
	var f aliyunStreamFrame
	if err := decodeChunk(chunk.Data, &f); err != nil {
		return nil, err
	}
	// Native in-band error: surface it as a terminal Error event instead of
	// dropping the frame — the entry maps it to an error chunk the QA
	// pipeline forwards to the client (and logs as stream_error).
	if f.Code != "" {
		msg := f.Message
		if msg == "" {
			msg = f.Code
		}
		if f.RequestID != "" {
			msg += " (request_id: " + f.RequestID + ")"
		}
		return []*invoke.StreamEvent{{
			Kind:  invoke.StreamKindError,
			Delta: &invoke.ContentDelta{Text: msg},
			Done:  &invoke.FinishInfo{Incomplete: true},
		}}, nil
	}
	if f.Usage != nil {
		state.Set(stateAliyunUsage, f.Usage.usage())
	}
	if len(f.Output.Choices) == 0 {
		// Usage-only frame (some models report usage ahead of the choices
		// terminator).
		if f.Usage != nil {
			u := f.Usage.usage()
			return []*invoke.StreamEvent{{Kind: invoke.StreamKindUsage, Usage: &u}}, nil
		}
		return nil, nil
	}
	choice := f.Output.Choices[0]
	finish := choice.FinishReason
	// DashScope quirk (2026-09-14 glm-5.2 report): intermediate streaming
	// frames carry finish_reason as the STRING "null" — not JSON null. The
	// decode makes it "null" != "" and the FIRST frame terminated the stream
	// with an empty answer. Treat the literal as absent.
	if finish == "null" {
		finish = ""
	}
	if finish != "" {
		state.Set(stateAliyunFinish, finish)
		// Shared key: the entry's clean-EOF synthesis reads the vendor's
		// recorded finish reason (see StreamStateFinishReason).
		state.Set(invoke.StreamStateFinishReason, finish)
		return []*invoke.StreamEvent{aliyunFlushDone(state, &choice.Message)}, nil
	}
	// Multi-event bridge (2026-09-13 裁定): a mixed message emits EVERY
	// payload it carries, in the same order as the openai bridge —
	// tool_calls (one event per delta), reasoning, content. Nothing is
	// dropped; conforming incremental_output vendors keep the fields on
	// separate frames, so this only changes mixed-frame behavior.
	d := choice.Message
	var out []*invoke.StreamEvent
	if len(d.ToolCalls) > 0 {
		assembler := invoke.ToolCallAssemblerFrom(state)
		if assembler == nil {
			assembler = invoke.NewToolCallAssembler()
			state.Set(invoke.StreamStateToolCalls, assembler)
		}
		for _, tc := range d.ToolCalls {
			delta := invoke.ToolCallDelta{
				Index: tc.Index, ID: tc.ID, Type: tc.Type,
				Name: tc.Function.Name, Arguments: tc.Function.Arguments,
			}
			assembler.Add(delta)
			out = append(out, &invoke.StreamEvent{Kind: invoke.StreamKindToolCall, ToolCallDelta: &delta})
		}
	}
	if d.ReasoningContent != "" {
		out = append(out, &invoke.StreamEvent{
			Kind:  invoke.StreamKindThinking,
			Delta: &invoke.ContentDelta{Text: d.ReasoningContent},
		})
	}
	if text := aliyunContentText(d.Content); text != "" {
		out = append(out, &invoke.StreamEvent{
			Kind:  invoke.StreamKindAnswer,
			Delta: &invoke.ContentDelta{Text: text},
		})
	}
	return out, nil
}

// aliyunFlushDone emits the terminating Done event from the accumulated
// state (finish reason + assembled tool calls + last observed usage). tail
// is the finish frame's message when one arrived: under incremental_output
// the LAST content fragment rides the SAME frame as finish_reason, so it is
// folded into the Done event's Delta (the entry emits it before the
// terminator — mapStreamEvent's Done+Delta seam). A reasoning tail has no
// entry mapping (Thinking+Done) and is dropped — qwq-class models emit
// reasoning strictly before the content frames. Tool calls arriving on the
// finish frame are fed to the shared assembler first so the terminator
// carries them complete.
func aliyunFlushDone(state *invoke.StreamBridgeState, tail *aliyunResponseMessage) *invoke.StreamEvent {
	finish, _ := state.Get(stateAliyunFinish)
	reason, _ := finish.(string)
	if tail != nil && len(tail.ToolCalls) > 0 {
		assembler := invoke.ToolCallAssemblerFrom(state)
		if assembler == nil {
			assembler = invoke.NewToolCallAssembler()
			state.Set(invoke.StreamStateToolCalls, assembler)
		}
		for _, tc := range tail.ToolCalls {
			assembler.Add(invoke.ToolCallDelta{
				Index: tc.Index, ID: tc.ID, Type: tc.Type,
				Name: tc.Function.Name, Arguments: tc.Function.Arguments,
			})
		}
	}
	var calls []invoke.ToolCall
	if assembler := invoke.ToolCallAssemblerFrom(state); assembler != nil {
		calls = assembler.Calls()
	}
	ev := &invoke.StreamEvent{
		Kind: invoke.StreamKindAnswer,
		Done: &invoke.FinishInfo{FinishReason: reason, ToolCalls: calls},
	}
	if tail != nil {
		if text := aliyunContentText(tail.Content); text != "" {
			ev.Delta = &invoke.ContentDelta{Text: text}
		}
	}
	if raw, ok := state.Get(stateAliyunUsage); ok {
		if u, ok := raw.(invoke.Usage); ok {
			usage := u
			ev.Usage = &usage
		}
	}
	return ev
}

// --- listing: the native catalog facet ---

const aliyunModelListPath = "/api/v1/models"

// aliyunListPageSize is the single source for the catalog page size: the
// request-side page_size literal and the entry pagination loop's
// ListPageSize() (whose "short page" check terminates the loop) MUST agree —
// a drifted pair either under- or over-pulls and can silently truncate the
// remote catalog to the first page (2026-09-13 review).
const aliyunListPageSize = 100

// ListPageSize opts the adapter into the entry's pagination loop
// (invoke.PaginatedLister): the catalog paginates by total, and the probe
// must not stop at page one (2026-09-12 ruling ③).
func (a *AliyunAdapter) ListPageSize() int { return aliyunListPageSize }

// aliyunCapabilityFilter maps the model type being edited onto the catalog's
// capability codes (查询模型列表.md §capabilities): TG=文本生成,
// TR=文本向量, ME=多模态向量. Types without a code (rerank/ASR — the
// catalog has no such filter, aliyun serves no ASR) list unfiltered; the
// caller-side UX still shows whatever the vendor returns.
func aliyunCapabilityFilter(modelType types.ModelType) []string {
	switch modelType {
	case types.ModelTypeKnowledgeQA, types.ModelTypeVLLM:
		return []string{"TG"}
	case types.ModelTypeEmbedding:
		return []string{"TR", "ME"}
	default:
		return nil
	}
}

// BuildListRequest targets the native model catalog filtered by the model
// type being edited (ruling ③: EVERY remote list load filters by the edited
// type — the handler forwards it via ListOptions.ModelType); pagination is
// entry-driven via ListPageSize above.
func (a *AliyunAdapter) BuildListRequest(ep invoke.Endpoint, opts invoke.ListOptions) (*invoke.Request, error) {
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	header.Set("Authorization", "Bearer "+ep.Credentials.APIKey)
	q := url.Values{}
	for _, cap := range aliyunCapabilityFilter(opts.ModelType) {
		q.Add("capabilities", cap)
	}
	pageNo := opts.PageNo
	if pageNo <= 0 {
		pageNo = 1
	}
	q.Set("page_no", strconv.Itoa(pageNo))
	q.Set("page_size", strconv.Itoa(aliyunListPageSize))
	return &invoke.Request{
		Method: http.MethodGet,
		URL:    aliyunNativeBaseURL(ep.BaseURL) + aliyunModelListPath + "?" + q.Encode(),
		Header: header,
	}, nil
}

// ParseListResponse decodes the DashScope catalog envelope
// (output.models[], context metadata from model_info).
func (a *AliyunAdapter) ParseListResponse(_ int, _ http.Header, body []byte) ([]invoke.RemoteModel, error) {
	var parsed struct {
		Output struct {
			Models []struct {
				Model     string `json:"model"`
				Name      string `json:"name"`
				ModelInfo struct {
					ContextWindow   int `json:"context_window"`
					MaxOutputTokens int `json:"max_output_tokens"`
				} `json:"model_info"`
			} `json:"models"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, invoke.ClassifyError(fmt.Errorf("unmarshal response: %w", err))
	}
	out := make([]invoke.RemoteModel, 0, len(parsed.Output.Models))
	for _, m := range parsed.Output.Models {
		if m.Model == "" {
			continue
		}
		out = append(out, invoke.RemoteModel{
			ID:              m.Model,
			DisplayName:     m.Name,
			ContextWindow:   m.ModelInfo.ContextWindow,
			MaxOutputTokens: m.ModelInfo.MaxOutputTokens,
		})
	}
	return out, nil
}

// --- embedding: native text-embedding (multimodal branch unchanged) ---

// BuildEmbeddingRequest dispatches the native dual-path builder (text
// models → text-embedding; vision-named models → multimodal-embedding).
func (a *AliyunAdapter) BuildEmbeddingRequest(
	ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions,
) (*invoke.Request, error) {
	return buildAliyunEmbedding(ep, model, opts)
}

// ParseEmbeddingResponse parses the native output.embeddings envelope.
func (a *AliyunAdapter) ParseEmbeddingResponse(
	status int, header http.Header, body []byte,
) (*invoke.EmbeddingResponse, error) {
	return parseAliyunEmbedding(status, header, body)
}

const aliyunTextEmbeddingPath = "/api/v1/services/embeddings/text-embedding/text-embedding"

type aliyunTextEmbedInput struct {
	Texts []string `json:"texts"`
}

type aliyunTextEmbedParams struct {
	Dimension int `json:"dimension,omitempty"`
}

type aliyunTextEmbedRequest struct {
	Model      string                 `json:"model"`
	Input      aliyunTextEmbedInput   `json:"input"`
	Parameters *aliyunTextEmbedParams `json:"parameters,omitempty"`
}

// buildAliyunEmbedding serves the native text-embedding endpoint
// ({model, input:{texts}, parameters:{dimension}}); vision-named models
// branch to the native multimodal endpoint. v1 rode the compatible-mode
// openai shape for text — the 2026-09-12 native ruling moves it onto the
// vendor's own wire. The 60s v1 timeout posture is kept.
func buildAliyunEmbedding(ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions) (*invoke.Request, error) {
	if model == "" {
		return nil, fmt.Errorf("model name is required")
	}
	if isMultimodalEmbeddingModel(model) {
		return buildAliyunMultimodalEmbedding(ep, model, opts)
	}
	reqBody := aliyunTextEmbedRequest{
		Model: model,
		Input: aliyunTextEmbedInput{Texts: opts.Inputs},
	}
	if opts.SupportsDimensionOverride && opts.Dimensions > 0 {
		reqBody.Parameters = &aliyunTextEmbedParams{Dimension: opts.Dimensions}
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Authorization", "Bearer "+ep.Credentials.APIKey)
	return &invoke.Request{
		Method:  http.MethodPost,
		URL:     aliyunNativeBaseURL(ep.BaseURL) + aliyunTextEmbeddingPath,
		Header:  header,
		Body:    data,
		Timeout: embeddingRequestTimeout,
	}, nil
}

// --- embedding (vision): the native multimodal endpoint (P2 port) ---

const aliyunMultimodalEmbeddingPath = "/api/v1/services/embeddings/multimodal-embedding/multimodal-embedding"

type aliyunEmbedRequest struct {
	Model      string             `json:"model"`
	Input      aliyunEmbedInput   `json:"input"`
	Parameters *aliyunEmbedParams `json:"parameters,omitempty"`
}

type aliyunEmbedInput struct {
	Contents []aliyunEmbedContent `json:"contents"`
}

type aliyunEmbedContent struct {
	Text string `json:"text,omitempty"`
}

type aliyunEmbedParams struct {
	Dimension int `json:"dimension,omitempty"`
}

type aliyunEmbedResponse struct {
	Output struct {
		Embeddings []struct {
			Embedding []float32 `json:"embedding"`
			TextIndex int       `json:"text_index"`
		} `json:"embeddings"`
	} `json:"output"`
}

func isMultimodalEmbeddingModel(model string) bool {
	lower := strings.ToLower(model)
	return strings.Contains(lower, "vision") || strings.Contains(lower, "multimodal")
}

func buildAliyunMultimodalEmbedding(
	ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions,
) (*invoke.Request, error) {
	// v1 multimodal path: default to the DashScope root and strip any
	// compatible-mode suffix the user pointed at the text endpoint with
	// (trailing slash trimmed first — v1 aliyun.go constructor; P2 review
	// finding 6). aliyunNativeBaseURL keeps the same stripping.
	base := aliyunNativeBaseURL(ep.BaseURL)
	contents := make([]aliyunEmbedContent, 0, len(opts.Inputs))
	for _, text := range opts.Inputs {
		contents = append(contents, aliyunEmbedContent{Text: text})
	}
	reqBody := aliyunEmbedRequest{
		Model: model,
		Input: aliyunEmbedInput{Contents: contents},
	}
	if opts.SupportsDimensionOverride && opts.Dimensions > 0 {
		reqBody.Parameters = &aliyunEmbedParams{Dimension: opts.Dimensions}
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("Authorization", "Bearer "+ep.Credentials.APIKey)
	return &invoke.Request{
		Method:  http.MethodPost,
		URL:     base + aliyunMultimodalEmbeddingPath,
		Header:  header,
		Body:    data,
		Timeout: embeddingRequestTimeout,
	}, nil
}

// parseAliyunEmbedding places vectors by text_index (DashScope may return
// them out of order), sized by the returned count — the caller-side pooler
// validates the count against the inputs (v1 batchEmbedder did the same).
func parseAliyunEmbedding(_ int, _ http.Header, body []byte) (*invoke.EmbeddingResponse, error) {
	var resp aliyunEmbedResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, invoke.ClassifyError(fmt.Errorf("unmarshal response: %w", err))
	}
	embeddings := make([][]float32, len(resp.Output.Embeddings))
	for _, emb := range resp.Output.Embeddings {
		if emb.TextIndex >= 0 && emb.TextIndex < len(embeddings) {
			embeddings[emb.TextIndex] = emb.Embedding
		}
	}
	return &invoke.EmbeddingResponse{Vectors: embeddings}, nil
}

// --- rerank: the native DashScope wires (P3 port; dual protocol since the
// 2026-09-12 native ruling) ---
// 排序文档（2026-09）：qwen3-rerank 走扁平的 /compatible-api/v1/reranks
// （{model, query, documents, top_n?} 顶层、results 在响应顶层）；其余
// rerank 模型（gte-rerank-v2 / qwen3.7-text-rerank / qwen3-vl-rerank）走
// text-rerank 端点的 {model, input, parameters} 信封（results 在
// output.results 下）。

const (
	aliyunTextRerankPath = "/api/v1/services/rerank/text-rerank/text-rerank"
	// aliFlatRerankPath 挂在 DashScope 根即可达（用户 2026-09-12 确认
	// dashscope.aliyuncs.com 代理 compatible-api；文档示例域名为
	// {WorkspaceId}.cn-beijing.maas.aliyuncs.com，两者同源）。
	aliyunFlatRerankPath = "/compatible-api/v1/reranks"
)

type aliyunRerankRequest struct {
	Model      string             `json:"model"`
	Input      aliyunRerankInput  `json:"input"`
	Parameters aliyunRerankParams `json:"parameters"`
}

type aliyunRerankInput struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

type aliyunRerankParams struct {
	// return_documents 仅 gte-rerank（含 v2）与 qwen3-vl-rerank 支持（文档
	// 参数表）；其余模型携带会被严格校验拒收——omitempty + 门控。
	ReturnDocuments bool `json:"return_documents,omitempty"`
	TopN            int  `json:"top_n,omitempty"`
}

// aliyunFlatRerankRequest is the qwen3-rerank flat shape: query/documents on
// the TOP level, no parameters wrapper. top_n is omitted (vendor default =
// return all — the platform's "return every document" posture), and
// return_documents stays off (the caller re-derives document text from the
// input — the facet-wide convention).
type aliyunFlatRerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}

// aliyunSupportsReturnDocuments gates the return_documents parameter by
// model family (排序 API 参数表)：仅 gte-rerank（含 v2）与 qwen3-vl-rerank
// 支持；qwen3.7-text-rerank 等携带即被拒。
func aliyunSupportsReturnDocuments(model string) bool {
	lower := strings.ToLower(model)
	return strings.Contains(lower, "gte-rerank") || strings.Contains(lower, "qwen3-vl-rerank")
}

// aliyunFlatRerank reports the flat-protocol model family (exactly the
// qwen3-rerank naming — qwen3.7-text-rerank / qwen3-vl-rerank stay on the
// text-rerank envelope per the doc's endpoint table).
func aliyunFlatRerank(model string) bool {
	return strings.HasPrefix(strings.ToLower(model), "qwen3-rerank")
}

// aliyunRerankRoot reduces any stored base to the DashScope root for rerank
// URL joining: rerank SERVICE paths (/api/v1/services/...,
// /compatible-api/...) are cut FIRST, then the standard normalization runs —
// that order matters, cutting a text-rerank endpoint leaves a bare /api/v1
// suffix for the standard trim to remove.
func aliyunRerankRoot(base string) string {
	if idx := strings.Index(base, "/services/"); idx != -1 {
		base = base[:idx]
	}
	if idx := strings.Index(base, "/compatible-api/"); idx != -1 {
		base = base[:idx]
	}
	return aliyunNativeBaseURL(base)
}

// aliyunRerankURL resolves the endpoint for the model's protocol:
//   - a base already pointing at a full endpoint OF THAT PROTOCOL
//     (contains /reranks for the flat family, /rerank for the envelope
//     family) passes through untouched — the v1 full-endpoint posture;
//   - anything else (empty, the DashScope root, a compatible-mode legacy
//     record, or the OTHER protocol's full endpoint — e.g. the text-rerank
//     prefill copied onto a qwen3-rerank record, which could never be
//     valid, or vice versa) is reduced to the root and the protocol path
//     appended.
func aliyunRerankURL(base, model string) string {
	if aliyunFlatRerank(model) {
		if strings.Contains(base, "/reranks") {
			return base
		}
		return aliyunRerankRoot(base) + aliyunFlatRerankPath
	}
	if strings.Contains(base, "/rerank") && !strings.Contains(base, "/reranks") {
		return base
	}
	return aliyunRerankRoot(base) + aliyunTextRerankPath
}

func buildAliyunRerank(ep invoke.Endpoint, model string, opts *invoke.RerankOptions) (*invoke.Request, error) {
	var body any
	if aliyunFlatRerank(model) {
		body = aliyunFlatRerankRequest{
			Model:     model,
			Query:     opts.Query,
			Documents: opts.Documents,
		}
	} else {
		body = aliyunRerankRequest{
			Model: model,
			Input: aliyunRerankInput{
				Query:     opts.Query,
				Documents: opts.Documents,
			},
			Parameters: aliyunRerankParams{
				ReturnDocuments: aliyunSupportsReturnDocuments(model),
				TopN:            len(opts.Documents), // v1: return all documents
			},
		}
	}
	return buildRerankRequestShared(aliyunRerankURL(ep.BaseURL, model), ep, body, "")
}

// parseAliyunRerank 解析两种响应信封：text-rerank 的 results 在
// output.results 下（P3 review finding 2——只读顶层会静默得到零结果），
// qwen3-rerank 扁平协议的 results 在顶层。ParseRerankResponse 契约不带模型
// 名，故按信封形态宽容解析：顶层优先，回落 output 信封。
func parseAliyunRerank(_ int, _ http.Header, body []byte) (*invoke.RerankResponse, error) {
	var top struct {
		Results []rankResultWire `json:"results"`
	}
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, invoke.ClassifyError(fmt.Errorf("unmarshal response: %w", err))
	}
	wire := top.Results
	if wire == nil {
		var env struct {
			Output struct {
				Results []rankResultWire `json:"results"`
			} `json:"output"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, invoke.ClassifyError(fmt.Errorf("unmarshal response: %w", err))
		}
		wire = env.Output.Results
	}
	out := make([]invoke.RerankResult, 0, len(wire))
	for _, r := range wire {
		out = append(out, invoke.RerankResult{Index: r.Index, Score: r.Score})
	}
	return &invoke.RerankResponse{Results: out}, nil
}

// BuildRerankRequest serves the native DashScope rerank wire.
func (a *AliyunAdapter) BuildRerankRequest(
	ep invoke.Endpoint, model string, opts *invoke.RerankOptions,
) (*invoke.Request, error) {
	return buildAliyunRerank(ep, model, opts)
}

// ParseRerankResponse serves the native DashScope rerank envelope.
func (a *AliyunAdapter) ParseRerankResponse(
	status int, header http.Header, body []byte,
) (*invoke.RerankResponse, error) {
	return parseAliyunRerank(status, header, body)
}
