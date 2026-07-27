// sources.go is the source-reference half of the model-context registry:
// request-local cN/dN/bN/wN handles for chunks, documents, knowledge bases
// and web pages, plus the tool-argument codec that maps them back to durable
// identifiers. Request lifecycles use Registry so source and resource handles
// cannot be encoded or decoded out of order.
package modelcontext

import (
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

type ChunkReference struct {
	ChunkID         string
	KnowledgeID     string
	KnowledgeBaseID string
	DocumentTitle   string
	ChunkIndex      int
	ChunkType       string
}

// webMeta is the per-web-page metadata stored next to the raw URL.
type webMeta struct {
	title string
}

// sourceRegistry is scoped to one assistant response (including every Agent tool
// round). Handles are never persisted or accepted across requests.
type sourceRegistry struct {
	citationsEnabled bool

	chunks *handleTable[ChunkReference]
	docs   *handleTable[struct{}]
	kbs    *handleTable[struct{}]
	webs   *handleTable[webMeta]
}

func newSourceRegistry(citationsEnabled ...bool) *sourceRegistry {
	enabled := true
	if len(citationsEnabled) > 0 {
		enabled = citationsEnabled[0]
	}
	return &sourceRegistry{
		citationsEnabled: enabled,
		chunks:           newHandleTable[ChunkReference]("c", 0, 1),
		docs:             newHandleTable[struct{}]("d", 0, 1),
		kbs:              newHandleTable[struct{}]("b", 0, 1),
		webs:             newHandleTable[webMeta]("w", 0, 1),
	}
}

func (r *sourceRegistry) Count() int {
	if r == nil {
		return 0
	}
	return r.chunks.size() + r.webs.size()
}

// knownHandle implements the shared guard for handle-shaped registration
// input: a model-emitted handle is echoed back only when it already exists,
// and is never accepted as a new durable identity.
func knownHandle[M any](table *handleTable[M], id string) string {
	handle := strings.ToLower(id)
	if table.has(handle) {
		return handle
	}
	return ""
}

func (r *sourceRegistry) RegisterChunk(ref ChunkReference) string {
	if r == nil {
		return ""
	}
	ref.ChunkID = strings.TrimSpace(ref.ChunkID)
	if ref.ChunkID == "" {
		return ""
	}
	if shortSourceHandleRE.MatchString(ref.ChunkID) {
		return knownHandle(r.chunks, ref.ChunkID)
	}
	return r.chunks.register(ref.ChunkID, ref.ChunkID, ref, mergeChunkReference)
}

func mergeChunkReference(dst *ChunkReference, src ChunkReference) {
	if dst.KnowledgeID == "" {
		dst.KnowledgeID = src.KnowledgeID
	}
	if dst.KnowledgeBaseID == "" {
		dst.KnowledgeBaseID = src.KnowledgeBaseID
	}
	if dst.DocumentTitle == "" {
		dst.DocumentTitle = src.DocumentTitle
	}
	if dst.ChunkIndex == 0 {
		dst.ChunkIndex = src.ChunkIndex
	}
	if dst.ChunkType == "" {
		dst.ChunkType = src.ChunkType
	}
}

func (r *sourceRegistry) RegisterDocument(id string) string {
	id = strings.TrimSpace(id)
	if r == nil || id == "" {
		return ""
	}
	if shortSourceHandleRE.MatchString(id) {
		return knownHandle(r.docs, id)
	}
	return r.docs.register(id, id, struct{}{}, nil)
}

func (r *sourceRegistry) RegisterKnowledgeBase(id string) string {
	id = strings.TrimSpace(id)
	if r == nil || id == "" {
		return ""
	}
	if shortSourceHandleRE.MatchString(id) {
		return knownHandle(r.kbs, id)
	}
	return r.kbs.register(id, id, struct{}{}, nil)
}

func (r *sourceRegistry) RegisterWeb(rawURL, title string) string {
	rawURL = strings.TrimSpace(rawURL)
	if r == nil || rawURL == "" {
		return ""
	}
	if shortSourceHandleRE.MatchString(rawURL) {
		return knownHandle(r.webs, rawURL)
	}
	// Dedup on the canonical (fragment-stripped) URL while decoding back to
	// the raw URL the model was originally shown.
	return r.webs.register(canonicalWebURL(rawURL), rawURL, webMeta{title: title}, func(dst *webMeta, src webMeta) {
		if dst.title == "" && src.title != "" {
			dst.title = src.title
		}
	})
}

func canonicalWebURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimSpace(raw)
	}
	parsed.Fragment = ""
	return parsed.String()
}

func (r *sourceRegistry) RegisterSearchResults(results []*types.SearchResult) {
	for _, result := range results {
		if result == nil {
			continue
		}
		r.RegisterDocument(result.KnowledgeID)
		r.RegisterKnowledgeBase(result.KnowledgeBaseID)
		r.RegisterChunk(ChunkReference{
			ChunkID:         result.ID,
			KnowledgeID:     result.KnowledgeID,
			KnowledgeBaseID: result.KnowledgeBaseID,
			DocumentTitle:   firstNonEmpty(result.KnowledgeTitle, result.KnowledgeFilename),
			ChunkIndex:      result.ChunkIndex,
			ChunkType:       result.ChunkType,
		})
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (r *sourceRegistry) ChunkHandle(id string) string {
	handle, _ := r.chunks.handleForKey(id)
	return handle
}

// toolArgumentPolicy decides whether a source-bearing JSON key belongs to a
// particular tool contract. Request lifecycles always pass the per-tool policy
// (sourceArgumentAllowed); a nil policy allows every key and exists only for
// package-internal replay paths that predate per-tool contracts.
type toolArgumentPolicy func(toolName, key string) bool

// DecodeToolCallsWithPolicy restores handles only for fields explicitly owned
// by the named tool. This prevents dynamic tools with coincidentally named
// fields from inheriting built-in source semantics.
func (r *sourceRegistry) DecodeToolCallsWithPolicy(toolCalls []types.LLMToolCall, policy toolArgumentPolicy) {
	for i := range toolCalls {
		toolName := toolCalls[i].Function.Name
		toolCalls[i].Function.Arguments = r.decodeJSONWithPolicy(
			toolCalls[i].Function.Arguments,
			false,
			func(key string) bool { return policy == nil || policy(toolName, key) },
		)
	}
}

// UnresolvedToolHandlesWithPolicy reports unknown handles only in fields that
// belong to the named tool's declared source contract.
func (r *sourceRegistry) UnresolvedToolHandlesWithPolicy(
	toolName, raw string,
	policy toolArgumentPolicy,
) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var value interface{}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	r.collectUnresolvedToolHandles(
		"", value, seen,
		func(key string) bool { return policy == nil || policy(toolName, key) },
	)
	result := make([]string, 0, len(seen))
	for handle := range seen {
		result = append(result, handle)
	}
	sort.Strings(result)
	return result
}

func (r *sourceRegistry) collectUnresolvedToolHandles(
	key string,
	value interface{},
	seen map[string]struct{},
	allowed func(string) bool,
) {
	switch typed := value.(type) {
	case string:
		key = strings.ToLower(key)
		if _, ok := sourceKeySpaces[key]; !ok || !allowed(key) {
			return
		}
		handle := strings.TrimSpace(typed)
		if shortSourceHandleRE.MatchString(handle) && (r == nil || r.durableForHandle(handle) == "") {
			seen[handle] = struct{}{}
		}
	case []interface{}:
		for _, item := range typed {
			r.collectUnresolvedToolHandles(key, item, seen, allowed)
		}
	case map[string]interface{}:
		for childKey, item := range typed {
			r.collectUnresolvedToolHandles(childKey, item, seen, allowed)
		}
	}
}

// EncodeMessagesWithPolicies compacts known real identifiers in replayed
// messages and gates source processing for tool results by tool name. A nil
// policy retains the legacy generic behavior for package-internal callers.
func (r *sourceRegistry) EncodeMessagesWithPolicies(
	messages []chat.Message,
	argumentPolicy toolArgumentPolicy,
	resultPolicy func(toolName string) bool,
) []chat.Message {
	if r == nil || len(messages) == 0 {
		return messages
	}
	out := make([]chat.Message, len(messages))
	copy(out, messages)
	// First register every durable identifier present in historical tool calls
	// and canonical assistant citations. This two-pass shape lets an early tool
	// message reuse metadata that appears only in the turn's final answer.
	for i := range out {
		processToolResult := out[i].Role == "tool" && (resultPolicy == nil || resultPolicy(out[i].Name))
		if out[i].Role == "assistant" || processToolResult {
			out[i].Content = r.CompactPublicCitations(out[i].Content)
			out[i].ReasoningContent = r.CompactPublicCitations(out[i].ReasoningContent)
		}
		if len(out[i].MultiContent) > 0 {
			out[i].MultiContent = append([]chat.MessageContentPart(nil), out[i].MultiContent...)
			for j := range out[i].MultiContent {
				if out[i].MultiContent[j].Type == "text" && (out[i].Role == "assistant" || processToolResult) {
					out[i].MultiContent[j].Text = r.CompactPublicCitations(out[i].MultiContent[j].Text)
				}
			}
		}
		if len(out[i].ToolCalls) > 0 {
			out[i].ToolCalls = append([]chat.ToolCall(nil), out[i].ToolCalls...)
			for j := range out[i].ToolCalls {
				toolName := out[i].ToolCalls[j].Function.Name
				r.registerToolArguments(
					out[i].ToolCalls[j].Function.Arguments,
					func(key string) bool { return argumentPolicy == nil || argumentPolicy(toolName, key) },
				)
			}
		}
	}
	for i := range out {
		if out[i].Role == "tool" && (resultPolicy == nil || resultPolicy(out[i].Name)) {
			r.registerLegacyToolReferences(out[i].Content)
			out[i].Content = r.CompactKnownText(out[i].Content)
		}
		for j := range out[i].ToolCalls {
			toolName := out[i].ToolCalls[j].Function.Name
			out[i].ToolCalls[j].Function.Arguments = r.decodeJSONWithPolicy(
				out[i].ToolCalls[j].Function.Arguments,
				true,
				func(key string) bool { return argumentPolicy == nil || argumentPolicy(toolName, key) },
			)
		}
	}
	return out
}

var shortSourceHandleRE = regexp.MustCompile(`(?i)^[cdbw][1-9][0-9]*$`)

var shortSourceHandleInTextRE = regexp.MustCompile(`(?i)\b[cdbw][1-9][0-9]*\b`)

// DecodeKnownText restores registered source handles embedded in a structured
// expression such as a built-in SQL tool argument. It must not be used for
// arbitrary prose; modelcontext owns the small tool/key policy that calls it.
func (r *sourceRegistry) DecodeKnownText(text string) string {
	if r == nil || text == "" {
		return text
	}
	return shortSourceHandleInTextRE.ReplaceAllStringFunc(text, func(handle string) string {
		if real := r.durableForHandle(handle); real != "" {
			return real
		}
		return handle
	})
}

// DecodeKnownQuotedText restores source handles only inside single-quoted,
// double-quoted, or backtick-quoted segments. It is intended for structured
// expressions such as SQL, where replacing an unquoted token could corrupt a
// legitimate table/column handle that happens to look like d1 or b2.
func (r *sourceRegistry) DecodeKnownQuotedText(text string) string {
	if r == nil || text == "" {
		return text
	}
	return rewriteQuotedText(text, func(segment string) string {
		return shortSourceHandleInTextRE.ReplaceAllStringFunc(segment, func(handle string) string {
			if real := r.durableForHandle(handle); real != "" {
				return real
			}
			return handle
		})
	})
}

// UnresolvedQuotedTextHandles reports handle-shaped values inside quoted
// structured-text segments that do not exist in this request registry.
func (r *sourceRegistry) UnresolvedQuotedTextHandles(text string) []string {
	if text == "" {
		return nil
	}
	seen := make(map[string]struct{})
	rewriteQuotedText(text, func(segment string) string {
		for _, handle := range shortSourceHandleInTextRE.FindAllString(segment, -1) {
			if r == nil || r.durableForHandle(handle) == "" {
				seen[handle] = struct{}{}
			}
		}
		return segment
	})
	result := make([]string, 0, len(seen))
	for handle := range seen {
		result = append(result, handle)
	}
	sort.Strings(result)
	return result
}

func rewriteQuotedText(text string, rewrite func(string) string) string {
	var out strings.Builder
	out.Grow(len(text))
	for i := 0; i < len(text); {
		quote := text[i]
		if quote != '\'' && quote != '"' && quote != '`' {
			out.WriteByte(text[i])
			i++
			continue
		}
		start := i
		i++
		for i < len(text) {
			if text[i] == '\\' && i+1 < len(text) {
				i += 2
				continue
			}
			if text[i] != quote {
				i++
				continue
			}
			// SQL escapes a quote by doubling it (''). Keep scanning the
			// same literal instead of treating the first quote as its end.
			if i+1 < len(text) && text[i+1] == quote {
				i += 2
				continue
			}
			i++
			break
		}
		out.WriteString(rewrite(text[start:i]))
	}
	return out.String()
}

func (r *sourceRegistry) registerToolArguments(raw string, allowed func(string) bool) {
	if r == nil || strings.TrimSpace(raw) == "" {
		return
	}
	var value interface{}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return
	}
	r.registerToolArgumentValue("", value, allowed)
}

func (r *sourceRegistry) registerToolArgumentValue(key string, value interface{}, allowed func(string) bool) {
	switch typed := value.(type) {
	case string:
		if allowed(strings.ToLower(key)) {
			r.registerSourceIDByKey(key, typed)
		}
	case []interface{}:
		for _, item := range typed {
			r.registerToolArgumentValue(key, item, allowed)
		}
	case map[string]interface{}:
		for childKey, item := range typed {
			r.registerToolArgumentValue(childKey, item, allowed)
		}
	}
}

// registerSourceIDByKey is the single key→source-space dispatch used for tool
// arguments, structured tool results, and database rows. It is driven by
// sourceKeySpaces — the same table that gates handle decode — so the recognized
// key set (and the http/https guard for web references) cannot drift between
// registration and decoding.
func (r *sourceRegistry) registerSourceIDByKey(key, value string) {
	value = strings.TrimSpace(value)
	if value == "" || shortSourceHandleRE.MatchString(value) {
		return
	}
	space, ok := sourceKeySpaces[strings.ToLower(key)]
	if !ok {
		return
	}
	switch space {
	case spaceChunk:
		r.RegisterChunk(ChunkReference{ChunkID: value})
	case spaceDocument:
		r.RegisterDocument(value)
	case spaceDocumentRef:
		// Stored refs use "knowledgeID|title"; only the ID part is durable.
		r.RegisterDocument(strings.TrimSpace(strings.SplitN(value, "|", 2)[0]))
	case spaceKnowledgeBase:
		r.RegisterKnowledgeBase(value)
	case spaceWeb:
		// Only public web pages become web references. Internal schemes
		// (res://, storage providers) must never enter the web handle space,
		// where CompactKnownText would rewrite them a second time.
		if parsed, err := url.Parse(value); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
			r.RegisterWeb(value, "")
		}
	}
}

func (r *sourceRegistry) decodeJSONWithPolicy(raw string, encode bool, allowed func(string) bool) string {
	if r == nil || strings.TrimSpace(raw) == "" {
		return raw
	}
	var value interface{}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	value = r.walkJSON("", value, encode, allowed)
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return string(encoded)
}

func (r *sourceRegistry) walkJSON(key string, value interface{}, encode bool, allowed func(string) bool) interface{} {
	switch typed := value.(type) {
	case string:
		if !allowed(strings.ToLower(key)) {
			return typed
		}
		if encode {
			// Encode matches on exact real identifiers (UUIDs/URLs), which do
			// not collide with prose, so it stays key-agnostic.
			if handle := r.handleForDurable(typed); handle != "" {
				return handle
			}
			return typed
		}
		// Decode only ID-bearing keys, and only when the value is handle-shaped,
		// so ordinary strings that coincidentally equal an handle are preserved.
		if _, ok := sourceKeySpaces[strings.ToLower(key)]; !ok {
			return typed
		}
		if !shortSourceHandleRE.MatchString(strings.TrimSpace(typed)) {
			return typed
		}
		if real := r.durableForHandle(typed); real != "" {
			return real
		}
		return typed
	case []interface{}:
		for i := range typed {
			typed[i] = r.walkJSON(key, typed[i], encode, allowed)
		}
	case map[string]interface{}:
		for childKey, item := range typed {
			typed[childKey] = r.walkJSON(childKey, item, encode, allowed)
		}
	}
	return value
}

func (r *sourceRegistry) handleForDurable(real string) string {
	if handle, ok := r.chunks.handleForKey(real); ok {
		return handle
	}
	if handle, ok := r.docs.handleForKey(real); ok {
		return handle
	}
	if handle, ok := r.kbs.handleForKey(real); ok {
		return handle
	}
	if handle, ok := r.webs.handleForKey(canonicalWebURL(real)); ok {
		return handle
	}
	return ""
}

func (r *sourceRegistry) durableForHandle(handle string) string {
	handle = strings.ToLower(strings.TrimSpace(handle))
	if real, _, ok := r.chunks.resolve(handle); ok {
		return real
	}
	if real, _, ok := r.docs.resolve(handle); ok {
		return real
	}
	if real, _, ok := r.kbs.resolve(handle); ok {
		return real
	}
	if real, _, ok := r.webs.resolve(handle); ok {
		return real
	}
	return ""
}

// CompactKnownText is intentionally limited to identifiers already registered
// from structured runtime/tool data. It is used for metadata envelopes, not
// arbitrary retrieved prose.
func (r *sourceRegistry) CompactKnownText(text string) string {
	if r == nil || text == "" {
		return text
	}
	// The snapshot spans all four source tables and is sorted longest-value
	// first GLOBALLY: a web URL may contain a registered document UUID as a
	// substring, so per-table passes could corrupt the longer value.
	pairs := r.chunks.pairs()
	pairs = append(pairs, r.docs.pairs()...)
	pairs = append(pairs, r.kbs.pairs()...)
	pairs = append(pairs, r.webs.pairs()...)
	sort.SliceStable(pairs, func(i, j int) bool { return len(pairs[i].value) > len(pairs[j].value) })
	for _, item := range pairs {
		if item.value != "" {
			text = strings.ReplaceAll(text, item.value, item.handle)
		}
	}
	return text
}
