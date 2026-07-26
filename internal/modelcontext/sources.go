// sources.go is the source-reference half of the model-context registry:
// request-local cN/dN/bN/wN handles for chunks, documents, knowledge bases
// and web pages, plus the tool-argument codec that maps them back to durable
// identifiers. Request lifecycles use Registry so source and resource handles
// cannot be encoded or decoded out of order.
package modelcontext

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

type ChunkReference struct {
	Alias           string
	ChunkID         string
	KnowledgeID     string
	KnowledgeBaseID string
	DocumentTitle   string
	ChunkIndex      int
	ChunkType       string
}

type WebReference struct {
	Alias string
	URL   string
	Title string
}

// sourceRegistry is scoped to one assistant response (including every Agent tool
// round). Aliases are never persisted or accepted across requests.
type sourceRegistry struct {
	mu sync.RWMutex

	citationsEnabled bool

	chunkByID    map[string]*ChunkReference
	chunkByAlias map[string]*ChunkReference
	docToAlias   map[string]string
	aliasToDoc   map[string]string
	kbToAlias    map[string]string
	aliasToKB    map[string]string
	webByURL     map[string]*WebReference
	webByAlias   map[string]*WebReference
}

func newSourceRegistry(citationsEnabled ...bool) *sourceRegistry {
	enabled := true
	if len(citationsEnabled) > 0 {
		enabled = citationsEnabled[0]
	}
	return &sourceRegistry{
		citationsEnabled: enabled,
		chunkByID:        make(map[string]*ChunkReference),
		chunkByAlias:     make(map[string]*ChunkReference),
		docToAlias:       make(map[string]string),
		aliasToDoc:       make(map[string]string),
		kbToAlias:        make(map[string]string),
		aliasToKB:        make(map[string]string),
		webByURL:         make(map[string]*WebReference),
		webByAlias:       make(map[string]*WebReference),
	}
}

func (r *sourceRegistry) Count() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.chunkByAlias) + len(r.webByAlias)
}

func (r *sourceRegistry) RegisterChunk(ref ChunkReference) string {
	if r == nil {
		return ""
	}
	ref.ChunkID = strings.TrimSpace(ref.ChunkID)
	if ref.ChunkID == "" {
		return ""
	}
	if shortSourceAliasRE.MatchString(ref.ChunkID) {
		alias := strings.ToLower(ref.ChunkID)
		r.mu.RLock()
		_, known := r.chunkByAlias[alias]
		r.mu.RUnlock()
		if known {
			return alias
		}
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing := r.chunkByID[ref.ChunkID]; existing != nil {
		mergeChunkReference(existing, ref)
		return existing.Alias
	}
	ref.Alias = fmt.Sprintf("c%d", len(r.chunkByAlias)+1)
	copyRef := ref
	r.chunkByID[ref.ChunkID] = &copyRef
	r.chunkByAlias[ref.Alias] = &copyRef
	return ref.Alias
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
	if shortSourceAliasRE.MatchString(id) {
		alias := strings.ToLower(id)
		r.mu.RLock()
		_, known := r.aliasToDoc[alias]
		r.mu.RUnlock()
		if known {
			return alias
		}
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if alias := r.docToAlias[id]; alias != "" {
		return alias
	}
	alias := fmt.Sprintf("d%d", len(r.aliasToDoc)+1)
	r.docToAlias[id] = alias
	r.aliasToDoc[alias] = id
	return alias
}

func (r *sourceRegistry) RegisterKnowledgeBase(id string) string {
	id = strings.TrimSpace(id)
	if r == nil || id == "" {
		return ""
	}
	if shortSourceAliasRE.MatchString(id) {
		alias := strings.ToLower(id)
		r.mu.RLock()
		_, known := r.aliasToKB[alias]
		r.mu.RUnlock()
		if known {
			return alias
		}
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if alias := r.kbToAlias[id]; alias != "" {
		return alias
	}
	alias := fmt.Sprintf("b%d", len(r.aliasToKB)+1)
	r.kbToAlias[id] = alias
	r.aliasToKB[alias] = id
	return alias
}

func (r *sourceRegistry) RegisterWeb(rawURL, title string) string {
	rawURL = strings.TrimSpace(rawURL)
	if r == nil || rawURL == "" {
		return ""
	}
	if shortSourceAliasRE.MatchString(rawURL) {
		alias := strings.ToLower(rawURL)
		r.mu.RLock()
		_, known := r.webByAlias[alias]
		r.mu.RUnlock()
		if known {
			return alias
		}
		return ""
	}
	key := canonicalWebURL(rawURL)
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing := r.webByURL[key]; existing != nil {
		if existing.Title == "" && title != "" {
			existing.Title = title
		}
		return existing.Alias
	}
	ref := &WebReference{
		Alias: fmt.Sprintf("w%d", len(r.webByAlias)+1),
		URL:   rawURL,
		Title: title,
	}
	r.webByURL[key] = ref
	r.webByAlias[ref.Alias] = ref
	return ref.Alias
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

func (r *sourceRegistry) ChunkAlias(id string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if ref := r.chunkByID[id]; ref != nil {
		return ref.Alias
	}
	return ""
}

// DecodeToolCalls restores exact alias-valued JSON strings before tools parse
// or validate their arguments. It never performs substring replacement.
func (r *sourceRegistry) DecodeToolCalls(toolCalls []types.LLMToolCall) {
	r.DecodeToolCallsWithPolicy(toolCalls, nil)
}

// toolArgumentPolicy decides whether a source-bearing JSON key belongs to a
// particular tool contract. A nil policy retains the codec's legacy generic
// behavior; application request lifecycles should provide an explicit policy
// through Registry.
type toolArgumentPolicy func(toolName, key string) bool

// DecodeToolCallsWithPolicy restores aliases only for fields explicitly owned
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
		if _, ok := decodableKeys[key]; !ok || !allowed(key) {
			return
		}
		handle := strings.TrimSpace(typed)
		if shortSourceAliasRE.MatchString(handle) && (r == nil || r.realForAlias(handle) == "") {
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

// EncodeMessages compacts known real identifiers in assistant tool-call replay.
func (r *sourceRegistry) EncodeMessages(messages []chat.Message) []chat.Message {
	return r.EncodeMessagesWithPolicies(messages, nil, nil)
}

// EncodeMessagesWithPolicies additionally gates source processing for tool
// result messages. A nil result policy retains the legacy generic behavior.
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

var shortSourceAliasRE = regexp.MustCompile(`(?i)^[cdbw][1-9][0-9]*$`)

var shortSourceAliasInTextRE = regexp.MustCompile(`(?i)\b[cdbw][1-9][0-9]*\b`)

// DecodeKnownText restores registered source handles embedded in a structured
// expression such as a built-in SQL tool argument. It must not be used for
// arbitrary prose; modelcontext owns the small tool/key policy that calls it.
func (r *sourceRegistry) DecodeKnownText(text string) string {
	if r == nil || text == "" {
		return text
	}
	return shortSourceAliasInTextRE.ReplaceAllStringFunc(text, func(handle string) string {
		if real := r.realForAlias(handle); real != "" {
			return real
		}
		return handle
	})
}

// DecodeKnownQuotedText restores source handles only inside single-quoted,
// double-quoted, or backtick-quoted segments. It is intended for structured
// expressions such as SQL, where replacing an unquoted token could corrupt a
// legitimate table/column alias that happens to look like d1 or b2.
func (r *sourceRegistry) DecodeKnownQuotedText(text string) string {
	if r == nil || text == "" {
		return text
	}
	return rewriteQuotedText(text, func(segment string) string {
		return shortSourceAliasInTextRE.ReplaceAllStringFunc(segment, func(handle string) string {
			if real := r.realForAlias(handle); real != "" {
				return real
			}
			return handle
		})
	})
}

// UnresolvedQuotedTextHandles reports alias-shaped values inside quoted
// structured-text segments that do not exist in this request registry.
func (r *sourceRegistry) UnresolvedQuotedTextHandles(text string) []string {
	if text == "" {
		return nil
	}
	seen := make(map[string]struct{})
	rewriteQuotedText(text, func(segment string) string {
		for _, handle := range shortSourceAliasInTextRE.FindAllString(segment, -1) {
			if r == nil || r.realForAlias(handle) == "" {
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
// arguments, structured tool results, and database rows. Keeping one dispatch
// stops the recognized key set (and the http/https guard for web references)
// from drifting between call sites; it must stay in sync with decodableKeys.
func (r *sourceRegistry) registerSourceIDByKey(key, value string) {
	value = strings.TrimSpace(value)
	if value == "" || shortSourceAliasRE.MatchString(value) {
		return
	}
	switch strings.ToLower(key) {
	case "chunk_id", "faq_id", "chunk_ids", "faq_ids":
		r.RegisterChunk(ChunkReference{ChunkID: value})
	case "knowledge_id", "knowledge_ids", "suspected_knowledge_ids":
		r.RegisterDocument(value)
	case "source_refs":
		// Stored refs use "knowledgeID|title"; only the ID part is durable.
		r.RegisterDocument(strings.TrimSpace(strings.SplitN(value, "|", 2)[0]))
	case "knowledge_base", "knowledge_base_id", "knowledge_base_ids", "kb_id", "kb_ids":
		r.RegisterKnowledgeBase(value)
	case "url", "urls":
		// Only public web pages become web references. Internal schemes
		// (res://, storage providers) must never enter the web alias space,
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

// decodableKeys mirrors the ID-bearing keys recognized by
// registerToolArgumentValue. Alias -> real substitution on decode is restricted
// to these keys so that free-text values (e.g. a grep/search query that happens
// to equal "d1") are never rewritten into internal identifiers.
var decodableKeys = map[string]struct{}{

	"chunk_id": {}, "faq_id": {}, "chunk_ids": {}, "faq_ids": {},
	"knowledge_id": {}, "knowledge_ids": {}, "suspected_knowledge_ids": {}, "source_refs": {},
	"knowledge_base": {}, "knowledge_base_id": {}, "knowledge_base_ids": {}, "kb_id": {}, "kb_ids": {},
	"url": {}, "urls": {},
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
			if alias := r.aliasForRealValue(typed); alias != "" {
				return alias
			}
			return typed
		}
		// Decode only ID-bearing keys, and only when the value is alias-shaped,
		// so ordinary strings that coincidentally equal an alias are preserved.
		if _, ok := decodableKeys[strings.ToLower(key)]; !ok {
			return typed
		}
		if !shortSourceAliasRE.MatchString(strings.TrimSpace(typed)) {
			return typed
		}
		if real := r.realForAlias(typed); real != "" {
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

func (r *sourceRegistry) aliasForRealValue(real string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if ref := r.chunkByID[real]; ref != nil {
		return ref.Alias
	}
	if alias := r.docToAlias[real]; alias != "" {
		return alias
	}
	if alias := r.kbToAlias[real]; alias != "" {
		return alias
	}
	if ref := r.webByURL[canonicalWebURL(real)]; ref != nil {
		return ref.Alias
	}
	return ""
}

func (r *sourceRegistry) realForAlias(alias string) string {
	alias = strings.ToLower(strings.TrimSpace(alias))
	r.mu.RLock()
	defer r.mu.RUnlock()
	if ref := r.chunkByAlias[alias]; ref != nil {
		return ref.ChunkID
	}
	if real := r.aliasToDoc[alias]; real != "" {
		return real
	}
	if real := r.aliasToKB[alias]; real != "" {
		return real
	}
	if ref := r.webByAlias[alias]; ref != nil {
		return ref.URL
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
	type pair struct{ real, alias string }
	r.mu.RLock()
	pairs := make([]pair, 0, len(r.chunkByID)+len(r.docToAlias)+len(r.kbToAlias)+len(r.webByURL))
	for real, ref := range r.chunkByID {
		pairs = append(pairs, pair{real, ref.Alias})
	}
	for real, alias := range r.docToAlias {
		pairs = append(pairs, pair{real, alias})
	}
	for real, alias := range r.kbToAlias {
		pairs = append(pairs, pair{real, alias})
	}
	for _, ref := range r.webByURL {
		pairs = append(pairs, pair{ref.URL, ref.Alias})
	}
	r.mu.RUnlock()
	sort.SliceStable(pairs, func(i, j int) bool { return len(pairs[i].real) > len(pairs[j].real) })
	for _, item := range pairs {
		if item.real != "" {
			text = strings.ReplaceAll(text, item.real, item.alias)
		}
	}
	return text
}
