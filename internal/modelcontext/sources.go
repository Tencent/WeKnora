// sources.go is the source-reference half of the model-context registry:
// request-local chunk://N, doc://N, kb://N and url://N handles for chunks,
// documents, knowledge bases and web pages, plus the tool-argument codec that
// maps them back to durable
// identifiers. Request lifecycles use Registry so source and resource handles
// cannot be encoded or decoded out of order.
package modelcontext

import (
	"encoding/json"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"

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
	// Addressable IDs from history, directory entries, and tool arguments are
	// not evidence until a current tool result supplies the source.
	citable sync.Map // handle -> true; registrations may run concurrently

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
		chunks:           newHandleTable[ChunkReference](chunkHandlePrefix, 0, 1),
		docs:             newHandleTable[struct{}](docHandlePrefix, 0, 1),
		kbs:              newHandleTable[struct{}](kbHandlePrefix, 0, 1),
		webs:             newHandleTable[webMeta](webHandlePrefix, 0, 1),
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
	handle := canonicalSourceHandle(id)
	if table.has(handle) {
		return handle
	}
	return ""
}

func (r *sourceRegistry) RegisterChunk(ref ChunkReference) string {
	return r.registerChunk(ref, true)
}

func (r *sourceRegistry) registerChunk(ref ChunkReference, evidence bool) string {
	if r == nil {
		return ""
	}
	ref.ChunkID = strings.TrimSpace(ref.ChunkID)
	if ref.ChunkID == "" {
		return ""
	}
	if isSourceHandleShaped(ref.ChunkID) {
		return knownHandle(r.chunks, ref.ChunkID)
	}
	handle := r.chunks.register(ref.ChunkID, ref.ChunkID, ref, mergeChunkReference)
	if evidence {
		r.citable.Store(handle, true)
	}
	return handle
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
	if isSourceHandleShaped(id) {
		return knownHandle(r.docs, id)
	}
	return r.docs.register(id, id, struct{}{}, nil)
}

func (r *sourceRegistry) RegisterKnowledgeBase(id string) string {
	id = strings.TrimSpace(id)
	if r == nil || id == "" {
		return ""
	}
	if isSourceHandleShaped(id) {
		return knownHandle(r.kbs, id)
	}
	return r.kbs.register(id, id, struct{}{}, nil)
}

func (r *sourceRegistry) RegisterWeb(rawURL, title string) string {
	return r.registerWeb(rawURL, title, true)
}

func (r *sourceRegistry) registerWeb(rawURL, title string, evidence bool) string {
	rawURL = strings.TrimSpace(rawURL)
	if r == nil || rawURL == "" {
		return ""
	}
	if isSourceHandleShaped(rawURL) {
		return knownHandle(r.webs, rawURL)
	}
	// Dedup on the canonical (fragment-stripped) URL while decoding back to
	// the raw URL the model was originally shown.
	handle := r.webs.register(canonicalWebURL(rawURL), rawURL, webMeta{title: title}, func(dst *webMeta, src webMeta) {
		if dst.title == "" && src.title != "" {
			dst.title = src.title
		}
	})
	if evidence {
		r.citable.Store(handle, true)
	}
	return handle
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

// DecodeToolCallsWithPolicy restores handles in every string argument. Fields
// explicitly owned by the named tool receive the bare durable ID, so dynamic
// tools with coincidentally named fields do not inherit built-in source
// semantics; bareText fields (SQL) receive bare IDs inside the expression;
// all other text receives durable text forms (URLs and weknora:// URIs).
func (r *sourceRegistry) DecodeToolCallsWithPolicy(
	toolCalls []types.LLMToolCall,
	policy toolArgumentPolicy,
	bareText toolArgumentPolicy,
) {
	for i := range toolCalls {
		toolName := toolCalls[i].Function.Name
		toolCalls[i].Function.Arguments = r.decodeJSONWithPolicy(
			toolCalls[i].Function.Arguments,
			false,
			func(key string) bool { return policy == nil || policy(toolName, key) },
			func(key string) bool { return bareText != nil && bareText(toolName, key) },
		)
	}
}

// UnresolvedToolHandlesWithPolicy reports handles left in a decoded call: any
// scheme-shaped handle in any string, plus legacy handles in ID fields that
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
		for _, handle := range r.UnresolvedTextHandles(typed) {
			seen[handle] = struct{}{}
		}
		key = strings.ToLower(key)
		if _, ok := sourceKeySpaces[key]; !ok || !allowed(key) {
			return
		}
		handle := strings.TrimSpace(typed)
		if isSourceHandleShaped(handle) && (r == nil || r.durableForHandle(handle) == "") {
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
		// Stored text (instructions, prompts, earlier answers) carries
		// workspace objects as weknora:// URIs in every role.
		reasoningBefore := out[i].ReasoningContent
		if out[i].Role == "assistant" || processToolResult {
			out[i].Content = r.CompactPublicCitations(out[i].Content, false)
			out[i].ReasoningContent = r.CompactPublicCitations(out[i].ReasoningContent, false)
		}
		out[i].Content = r.EncodeDurableURIs(out[i].Content)
		out[i].ReasoningContent = r.EncodeDurableURIs(out[i].ReasoningContent)
		dropStaleReasoningSignature(&out[i], reasoningBefore)
		if len(out[i].MultiContent) > 0 {
			out[i].MultiContent = append([]chat.MessageContentPart(nil), out[i].MultiContent...)
			for j := range out[i].MultiContent {
				if out[i].MultiContent[j].Type != "text" {
					continue
				}
				if out[i].Role == "assistant" || processToolResult {
					out[i].MultiContent[j].Text = r.CompactPublicCitations(out[i].MultiContent[j].Text, false)
				}
				out[i].MultiContent[j].Text = r.EncodeDurableURIs(out[i].MultiContent[j].Text)
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
			r.registerLegacyToolReferences(out[i].Content, false)
			out[i].Content = r.CompactKnownText(out[i].Content)
			if out[i].Name == "web_search" || out[i].Name == "web_fetch" {
				// Citation IDs and fetch targets are distinct: expose the actual URL after
				// compaction so models need not infer new sequential IDs for page links.
				out[i].Content = webPageHeaderRE.ReplaceAllStringFunc(out[i].Content, func(header string) string {
					match := webPageHeaderRE.FindStringSubmatch(header)
					header = webPageURLAttrRE.ReplaceAllString(header, "")
					rawURL := r.durableForHandle(match[1])
					if rawURL == "" {
						return header
					}
					return strings.TrimSuffix(header, ">") + ` url="` + html.EscapeString(rawURL) + `">`
				})
			}
		}
		for j := range out[i].ToolCalls {
			toolName := out[i].ToolCalls[j].Function.Name
			out[i].ToolCalls[j].Function.Arguments = r.decodeJSONWithPolicy(
				out[i].ToolCalls[j].Function.Arguments,
				true,
				func(key string) bool { return argumentPolicy == nil || argumentPolicy(toolName, key) },
				nil,
			)
		}
	}
	return out
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
			r.registerSourceIDByKey(key, typed, false)
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
func (r *sourceRegistry) registerSourceIDByKey(key, value string, evidence bool) {
	value = strings.TrimSpace(value)
	if _, id, ok := parseDurableURI(value); ok {
		value = id
	}
	if value == "" || isSourceHandleShaped(value) {
		return
	}
	space, ok := sourceKeySpaces[strings.ToLower(key)]
	if !ok {
		return
	}
	switch space {
	case spaceChunk:
		r.registerChunk(ChunkReference{ChunkID: value}, evidence)
	case spaceDocument:
		r.RegisterDocument(value)
	case spaceDocumentRef:
		// Stored refs use "knowledgeID|title"; only the ID part is durable.
		r.RegisterDocument(types.WikiSourceKnowledgeID(value))
	case spaceKnowledgeBase:
		r.RegisterKnowledgeBase(value)
	case spaceWeb:
		// Only public web pages become web references. Internal schemes
		// (res://, storage providers) must never enter the web handle space,
		// where CompactKnownText would rewrite them a second time.
		if parsed, err := url.Parse(value); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
			r.registerWeb(value, "", evidence)
		}
	case spaceAnySource:
		// Decode-only: a bare "id" never registers a new source.
	}
}

func (r *sourceRegistry) decodeJSONWithPolicy(
	raw string,
	encode bool,
	allowed func(string) bool,
	bareText func(string) bool,
) string {
	if r == nil || strings.TrimSpace(raw) == "" {
		return raw
	}
	var value interface{}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	if bareText == nil {
		bareText = func(string) bool { return false }
	}
	value = r.walkJSON("", value, encode, allowed, bareText)
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return string(encoded)
}

func (r *sourceRegistry) walkJSON(
	key string,
	value interface{},
	encode bool,
	allowed func(string) bool,
	bareText func(string) bool,
) interface{} {
	switch typed := value.(type) {
	case string:
		lower := strings.ToLower(key)
		_, idKey := sourceKeySpaces[lower]
		idField := idKey && allowed(lower)
		if encode {
			// Encode matches on exact real identifiers (UUIDs/URLs), which do
			// not collide with prose, so it stays key-agnostic.
			if allowed(lower) {
				if handle := r.handleForDurable(typed); handle != "" {
					return handle
				}
			}
			return r.EncodeDurableURIs(typed)
		}
		if idField {
			trimmed := strings.TrimSpace(typed)
			if isSourceHandleShaped(trimmed) {
				if durable := r.durableForHandle(trimmed); durable != "" {
					return durable
				}
				return typed
			}
			if _, id, ok := parseDurableURI(trimmed); ok {
				return id
			}
		}
		if bareText(lower) {
			return r.DecodeBareText(typed)
		}
		return r.DecodeDurableText(typed)
	case []interface{}:
		for i := range typed {
			typed[i] = r.walkJSON(key, typed[i], encode, allowed, bareText)
		}
	case map[string]interface{}:
		for childKey, item := range typed {
			typed[childKey] = r.walkJSON(childKey, item, encode, allowed, bareText)
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
	_, durable, _, ok := r.resolveSourceHandle(handle)
	if !ok {
		return ""
	}
	return durable
}

// CompactKnownText is intentionally limited to identifiers already registered
// from structured runtime/tool data, plus explicit weknora:// URIs. It is used
// for metadata envelopes, not arbitrary retrieved prose.
func (r *sourceRegistry) CompactKnownText(text string) string {
	if r == nil || text == "" {
		return text
	}
	// URIs first: their embedded IDs must become one handle, not "weknora://kb/kb://1".
	text = r.EncodeDurableURIs(text)
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

var webPageHeaderRE = regexp.MustCompile(`<page id="(url://[1-9][0-9]*)"[^>]*>`)

var webPageURLAttrRE = regexp.MustCompile(` url="[^"]*"`)
