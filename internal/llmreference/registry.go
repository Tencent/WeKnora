// Package llmreference keeps internal source identifiers out of LLM-facing
// context. It assigns request-local aliases to chunks, documents, knowledge
// bases and web pages, decodes aliases used in tool arguments, and expands the
// model's compact <ref/> citations before application code consumes them.
package llmreference

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const sourceAliasProtocolPrompt = `

## Source handling protocol (system-owned)
Retrieved content uses request-local source handles: cN identifies a knowledge chunk, wN a web page, dN a document, and bN a knowledge base.
- Use dN and bN only as tool arguments when a tool requests a document or knowledge base.
- Never reveal raw chunk IDs, knowledge IDs, knowledge-base IDs, or private source handles in user-visible output. This does not change separate instructions to preserve retrieved Markdown image URLs.`

const citationEnabledProtocolPrompt = `
- Source citations are enabled for this answer. Cite a knowledge chunk with exactly <ref id="cN"/> and a web page with exactly <ref id="wN"/>.
- Copy only cN/wN handles that appeared in supplied context or tool results. Never cite dN/bN.
- Never output <kb> or <web> tags yourself; the system expands valid <ref/> tags after generation.
- Keep each <ref/> inline on the same line as the claim it supports. Do not group citations at the end.
- These rules supersede earlier, saved, or custom prompt instructions about citation syntax.`

const citationDisabledProtocolPrompt = `
- Source citations are disabled for this answer. Do not output <ref>, <kb>, <web>, raw source URLs, or source-handle citations.
- These rules supersede earlier, saved, or custom prompt instructions that require source citations.`

// ProtocolPrompt returns the internal, non-user-editable source protocol for a
// model call. Citation formatting stays out of custom and template prompts.
func ProtocolPrompt(citationsEnabled bool) string {
	if citationsEnabled {
		return sourceAliasProtocolPrompt + citationEnabledProtocolPrompt
	}
	return sourceAliasProtocolPrompt + citationDisabledProtocolPrompt
}

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

// Registry is scoped to one assistant response (including every Agent tool
// round). Aliases are never persisted or accepted across requests.
type Registry struct {
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

func NewRegistry(citationsEnabled ...bool) *Registry {
	enabled := true
	if len(citationsEnabled) > 0 {
		enabled = citationsEnabled[0]
	}
	return &Registry{
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

func (r *Registry) Count() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.chunkByAlias) + len(r.webByAlias)
}

func (r *Registry) RegisterChunk(ref ChunkReference) string {
	if r == nil || strings.TrimSpace(ref.ChunkID) == "" {
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

func (r *Registry) RegisterDocument(id string) string {
	if r == nil || strings.TrimSpace(id) == "" {
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

func (r *Registry) RegisterKnowledgeBase(id string) string {
	if r == nil || strings.TrimSpace(id) == "" {
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

func (r *Registry) RegisterWeb(rawURL, title string) string {
	if r == nil || strings.TrimSpace(rawURL) == "" {
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

func (r *Registry) RegisterSearchResults(results []*types.SearchResult) {
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

func (r *Registry) ChunkAlias(id string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if ref := r.chunkByID[id]; ref != nil {
		return ref.Alias
	}
	return ""
}

func (r *Registry) DocumentAlias(id string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.docToAlias[id]
}

func (r *Registry) KnowledgeBaseAlias(id string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.kbToAlias[id]
}

// DecodeToolCalls restores exact alias-valued JSON strings before tools parse
// or validate their arguments. It never performs substring replacement.
func (r *Registry) DecodeToolCalls(toolCalls []types.LLMToolCall) {
	for i := range toolCalls {
		toolCalls[i].Function.Arguments = r.decodeJSON(toolCalls[i].Function.Arguments, false)
	}
}

// EncodeMessages compacts known real identifiers in assistant tool-call replay.
func (r *Registry) EncodeMessages(messages []chat.Message) []chat.Message {
	if r == nil || len(messages) == 0 {
		return messages
	}
	out := make([]chat.Message, len(messages))
	copy(out, messages)
	// First register every durable identifier present in historical tool calls
	// and canonical assistant citations. This two-pass shape lets an early tool
	// message reuse metadata that appears only in the turn's final answer.
	for i := range out {
		if out[i].Role == "assistant" || out[i].Role == "tool" {
			out[i].Content = r.CompactPublicCitations(out[i].Content)
			out[i].ReasoningContent = r.CompactPublicCitations(out[i].ReasoningContent)
		}
		if len(out[i].MultiContent) > 0 {
			out[i].MultiContent = append([]chat.MessageContentPart(nil), out[i].MultiContent...)
			for j := range out[i].MultiContent {
				if out[i].MultiContent[j].Type == "text" && (out[i].Role == "assistant" || out[i].Role == "tool") {
					out[i].MultiContent[j].Text = r.CompactPublicCitations(out[i].MultiContent[j].Text)
				}
			}
		}
		if len(out[i].ToolCalls) > 0 {
			out[i].ToolCalls = append([]chat.ToolCall(nil), out[i].ToolCalls...)
			for j := range out[i].ToolCalls {
				r.registerToolArguments(out[i].ToolCalls[j].Function.Arguments)
			}
		}
	}
	for i := range out {
		if out[i].Role == "tool" {
			r.registerLegacyToolReferences(out[i].Content)
			out[i].Content = r.CompactKnownText(out[i].Content)
		}
		for j := range out[i].ToolCalls {
			out[i].ToolCalls[j].Function.Arguments = r.decodeJSON(out[i].ToolCalls[j].Function.Arguments, true)
		}
	}
	return out
}

var shortSourceAliasRE = regexp.MustCompile(`(?i)^[cdbw][1-9][0-9]*$`)

func (r *Registry) registerToolArguments(raw string) {
	if r == nil || strings.TrimSpace(raw) == "" {
		return
	}
	var value interface{}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return
	}
	r.registerToolArgumentValue("", value)
}

func (r *Registry) registerToolArgumentValue(key string, value interface{}) {
	switch typed := value.(type) {
	case string:
		value := strings.TrimSpace(typed)
		if value == "" || shortSourceAliasRE.MatchString(value) {
			return
		}
		switch strings.ToLower(key) {
		case "chunk_id", "faq_id", "chunk_ids", "faq_ids":
			r.RegisterChunk(ChunkReference{ChunkID: value})
		case "knowledge_id", "knowledge_ids", "suspected_knowledge_ids", "source_refs":
			r.RegisterDocument(value)
		case "knowledge_base", "knowledge_base_id", "knowledge_base_ids", "kb_id", "kb_ids":
			r.RegisterKnowledgeBase(value)
		case "url", "urls":
			if parsed, err := url.Parse(value); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
				r.RegisterWeb(value, "")
			}
		}
	case []interface{}:
		for _, item := range typed {
			r.registerToolArgumentValue(key, item)
		}
	case map[string]interface{}:
		for childKey, item := range typed {
			r.registerToolArgumentValue(childKey, item)
		}
	}
}

var (
	publicKBTagRE        = regexp.MustCompile(`(?is)<kb\b[^>]*>`)
	publicWebTagRE       = regexp.MustCompile(`(?is)<web\b[^>]*>`)
	docAttrRE            = regexp.MustCompile(`(?i)\bdoc\s*=\s*"([^"]*)"`)
	chunkAttrRE          = regexp.MustCompile(`(?i)\bchunk_id\s*=\s*"([^"]+)"`)
	publicKBAttrRE       = regexp.MustCompile(`(?i)\bkb_id\s*=\s*"([^"]*)"`)
	urlAttrRE            = regexp.MustCompile(`(?i)\burl\s*=\s*"([^"]+)"`)
	titleAttrRE          = regexp.MustCompile(`(?i)\btitle\s*=\s*"([^"]*)"`)
	legacyChunkRE        = regexp.MustCompile(`(?is)<(?:chunk|faq)\b[^>]*>`)
	faqAttrRE            = regexp.MustCompile(`(?i)\bfaq_id\s*=\s*"([^"]+)"`)
	knowledgeTitleAttrRE = regexp.MustCompile(`(?i)\bknowledge_title\s*=\s*"([^"]*)"`)
)

func (r *Registry) registerLegacyToolReferences(text string) {
	if r == nil || text == "" {
		return
	}
	r.registerLabeledReferences(text)
	for _, tag := range legacyChunkRE.FindAllString(text, -1) {
		chunkID := firstNonEmpty(publicAttr(chunkAttrRE, tag), publicAttr(faqAttrRE, tag))
		if chunkID == "" {
			continue
		}
		r.RegisterChunk(ChunkReference{
			ChunkID:         chunkID,
			KnowledgeID:     publicAttr(documentAttrRE, tag),
			KnowledgeBaseID: firstNonEmpty(publicAttr(kbAttrRE, tag), publicAttr(publicKBAttrRE, tag)),
			DocumentTitle:   firstNonEmpty(publicAttr(knowledgeTitleAttrRE, tag), publicAttr(docAttrRE, tag)),
		})
	}
}

// CompactPublicCitations folds canonical citations from prior assistant turns
// back into this request's private protocol. This prevents durable chunk IDs
// and web URLs in conversation history from becoming model-visible again.
func (r *Registry) CompactPublicCitations(text string) string {
	if r == nil || text == "" {
		return text
	}
	text = publicKBTagRE.ReplaceAllStringFunc(text, func(tag string) string {
		chunkID := publicAttr(chunkAttrRE, tag)
		if chunkID == "" {
			return tag
		}
		alias := r.RegisterChunk(ChunkReference{
			ChunkID:         chunkID,
			KnowledgeBaseID: publicAttr(publicKBAttrRE, tag),
			DocumentTitle:   publicAttr(docAttrRE, tag),
		})
		return `<ref id="` + alias + `"/>`
	})
	return publicWebTagRE.ReplaceAllStringFunc(text, func(tag string) string {
		rawURL := publicAttr(urlAttrRE, tag)
		if rawURL == "" {
			return tag
		}
		alias := r.RegisterWeb(rawURL, publicAttr(titleAttrRE, tag))
		return `<ref id="` + alias + `"/>`
	})
}

func publicAttr(expression *regexp.Regexp, tag string) string {
	match := expression.FindStringSubmatch(tag)
	if len(match) != 2 {
		return ""
	}
	return html.UnescapeString(match[1])
}

func (r *Registry) decodeJSON(raw string, encode bool) string {
	if r == nil || strings.TrimSpace(raw) == "" {
		return raw
	}
	var value interface{}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	value = r.walkJSON("", value, encode)
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

func (r *Registry) walkJSON(key string, value interface{}, encode bool) interface{} {
	switch typed := value.(type) {
	case string:
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
			typed[i] = r.walkJSON(key, typed[i], encode)
		}
	case map[string]interface{}:
		for childKey, item := range typed {
			typed[childKey] = r.walkJSON(childKey, item, encode)
		}
	}
	return value
}

func (r *Registry) aliasForRealValue(real string) string {
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

func (r *Registry) realForAlias(alias string) string {
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
func (r *Registry) CompactKnownText(text string) string {
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

var (
	refTagRE       = regexp.MustCompile(`(?i)<ref\s+id\s*=\s*"([^"]+)"\s*/?>`)
	refCandidateRE = regexp.MustCompile(`(?is)<ref(?:\s|$)[^>]*(?:>|$)`)
	modelKBTagRE   = regexp.MustCompile(`(?is)<kb(?:\s|$)[^>]*(?:>|$)`)
	modelWebTagRE  = regexp.MustCompile(`(?is)<web(?:\s|$)[^>]*(?:>|$)`)
)

var (
	documentAttrRE    = regexp.MustCompile(`(?i)\bknowledge_id\s*=\s*"([^"]+)"`)
	documentElementRE = regexp.MustCompile(`(?is)<knowledge_id>\s*([^<]+?)\s*</knowledge_id>`)
	kbAttrRE          = regexp.MustCompile(`(?i)\b(?:knowledge_base_id|kb_id)\s*=\s*"([^"]+)"`)
	kbElementRE       = regexp.MustCompile(`(?is)<(?:knowledge_base_id|kb_id)>\s*([^<]+?)\s*</(?:knowledge_base_id|kb_id)>`)
)

// registerLabeledReferences covers metadata-oriented tools that do not have a
// dedicated compact renderer. Only explicit ID labels are recognized; UUID-like
// text in retrieved content is never guessed to be a source identifier.
func (r *Registry) registerLabeledReferences(text string) {
	if r == nil || text == "" {
		return
	}
	for _, expression := range []*regexp.Regexp{documentAttrRE, documentElementRE} {
		for _, match := range expression.FindAllStringSubmatch(text, -1) {
			if len(match) == 2 {
				r.RegisterDocument(strings.TrimSpace(match[1]))
			}
		}
	}
	for _, expression := range []*regexp.Regexp{kbAttrRE, kbElementRE} {
		for _, match := range expression.FindAllStringSubmatch(text, -1) {
			if len(match) == 2 {
				r.RegisterKnowledgeBase(strings.TrimSpace(match[1]))
			}
		}
	}
}

// ExpandText converts the private model protocol into the existing public
// <kb/> / <web/> contract. Unknown aliases fail closed and disappear.
func (r *Registry) ExpandText(text string) string {
	if r == nil || text == "" {
		return text
	}
	// Public citation tags are output-only. Drop any instance written directly
	// by the model, then create canonical tags solely from registered aliases.
	text = modelKBTagRE.ReplaceAllString(text, "")
	text = modelWebTagRE.ReplaceAllString(text, "")
	if !r.citationsEnabled {
		return refCandidateRE.ReplaceAllString(text, "")
	}
	return refCandidateRE.ReplaceAllStringFunc(text, func(tag string) string {
		match := refTagRE.FindStringSubmatch(tag)
		if len(match) != 2 {
			return ""
		}
		alias := strings.ToLower(match[1])
		r.mu.RLock()
		chunkRef := r.chunkByAlias[alias]
		webRef := r.webByAlias[alias]
		r.mu.RUnlock()
		if chunkRef != nil {
			attrs := fmt.Sprintf(`doc="%s" chunk_id="%s"`, escapeAttr(chunkRef.DocumentTitle), escapeAttr(chunkRef.ChunkID))
			if chunkRef.KnowledgeBaseID != "" {
				attrs += fmt.Sprintf(` kb_id="%s"`, escapeAttr(chunkRef.KnowledgeBaseID))
			}
			return "<kb " + attrs + " />"
		}
		if webRef != nil {
			return fmt.Sprintf(`<web url="%s" title="%s" />`, escapeAttr(webRef.URL), escapeAttr(webRef.Title))
		}
		return ""
	})
}

func escapeAttr(value string) string { return html.EscapeString(value) }

func (r *Registry) ExpandResponse(response *types.ChatResponse) {
	if response == nil {
		return
	}
	response.Content = r.ExpandText(response.Content)
	response.ReasoningContent = r.ExpandText(response.ReasoningContent)
	r.DecodeToolCalls(response.ToolCalls)
}

// StreamExpander prevents partial private <ref/> tags from reaching SSE while
// preserving normal streaming for all other content.
type StreamExpander struct {
	registry *Registry
	pending  string
}

func NewStreamExpander(registry *Registry) *StreamExpander {
	return &StreamExpander{registry: registry}
}

func (d *StreamExpander) Feed(chunk string) string {
	if d == nil || d.registry == nil {
		return chunk
	}
	data := d.pending + chunk
	d.pending = ""
	var out strings.Builder
	for data != "" {
		idx := strings.Index(data, "<")
		if idx < 0 {
			out.WriteString(data)
			break
		}
		out.WriteString(data[:idx])
		data = data[idx:]
		lower := strings.ToLower(data)
		if isSourceTagPending(lower) && !strings.Contains(data, ">") {
			d.pending = data
			break
		}
		if isRefTagStart(lower) {
			end := strings.IndexByte(data, '>')
			if end < 0 {
				d.pending = data
				break
			}
			tag := data[:end+1]
			if refTagRE.MatchString(tag) {
				out.WriteString(d.registry.ExpandText(tag))
			}
			data = data[end+1:]
			continue
		}
		if isNamedTagStart(lower, "kb") || isNamedTagStart(lower, "web") {
			end := strings.IndexByte(data, '>')
			if end < 0 {
				d.pending = data
				break
			}
			data = data[end+1:]
			continue
		}
		out.WriteByte('<')
		data = data[1:]
	}
	return out.String()
}

func isRefTagStart(value string) bool {
	return isNamedTagStart(value, "ref")
}

func isNamedTagStart(value, name string) bool {
	prefix := "<" + name
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	if len(value) == len(prefix) {
		return true
	}
	next := value[len(prefix)]
	return next == ' ' || next == '\t' || next == '\r' || next == '\n' || next == '>'
}

func isSourceTagPending(value string) bool {
	for _, name := range []string{"ref", "kb", "web"} {
		prefix := "<" + name
		if (len(value) <= len(prefix) && strings.HasPrefix(prefix, value)) || isNamedTagStart(value, name) {
			return true
		}
	}
	return false
}

func (d *StreamExpander) Flush() string {
	if d == nil {
		return ""
	}
	pending := d.pending
	d.pending = ""
	lower := strings.ToLower(pending)
	if isSourceTagPending(lower) {
		return ""
	}
	return d.registry.ExpandText(pending)
}
