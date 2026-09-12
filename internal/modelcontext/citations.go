// citations.go owns the public-citation surface of the model context: the
// system protocol prompt, expansion of private <ref/> handles into canonical
// <kb/> / <web/> tags, re-compaction of canonical tags replayed from history,
// and the stream expander that keeps partial tags off the wire.
package modelcontext

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

const sourceHandleProtocolPrompt = `

## Source handling protocol (system-owned)
Retrieved content uses request-local source handles: cN identifies a knowledge chunk, wN a web page, dN a document, and bN a knowledge base.
- Use dN and bN only as tool arguments when a tool requests a document or knowledge base.
- Never reveal raw chunk IDs, knowledge IDs, knowledge-base IDs, or private source handles in user-visible output. This does not change separate instructions to preserve retrieved Markdown image URLs.`

const citationEnabledProtocolPrompt = `
- Source citations are enabled for this answer. Cite a knowledge chunk with exactly <ref id="cN"/> and a web page with exactly <ref id="wN"/>.
- Cite only cN/wN handles backed by tool results for the current task, and only when that source supports the adjacent
  claim. Never cite dN/bN.
- Handles in historical answers, tool arguments, or the bound knowledge-base directory are for navigation, not current
  evidence. Retrieve the relevant source before citing it.
- MCP results are external sources. Use the wN handle for the matching URL in the system-provided
  external_source_candidates list; never substitute a knowledge-base cN handle for MCP content. Candidate URLs are links
  observed in the result, not proof that every linked page was read.
- If a source has no citation handle, use its exact supplied HTTP(S) URL as a Markdown link when available.
  If neither is available, omit the citation; never invent or borrow a source.
- Never output <kb> or <web> tags yourself; the system expands valid <ref/> tags after generation.
- Keep each <ref/> inline on the same line as the claim it supports. Do not group citations ` +
	`at the end. For a requested exact output format, use citations only where the format ` +
	`permits them; do not break a required schema to add citations.
- These rules supersede earlier, saved, or custom prompt instructions about citation syntax.`

const citationDisabledProtocolPrompt = `
- Source citations are disabled for this answer. Do not add <ref>, <kb>, <web>, or source ` +
	`attribution links to the answer. This does not prohibit a URL explicitly requested by the ` +
	`user, Wiki navigation links, downloadable deliverables, or relevant image URLs.
- These rules supersede earlier, saved, or custom prompt instructions that require source citations.`

// ProtocolPrompt returns the internal, non-user-editable source protocol for a
// model call. Citation formatting stays out of custom and template prompts.
func sourceProtocolPrompt(citationsEnabled bool) string {
	if citationsEnabled {
		return sourceHandleProtocolPrompt + citationEnabledProtocolPrompt
	}
	return sourceHandleProtocolPrompt + citationDisabledProtocolPrompt
}

// ProtocolPrompt returns the source protocol configured for this registry.
// Request lifecycle code should normally call this through Registry.
func (r *sourceRegistry) ProtocolPrompt() string {
	if r == nil {
		return ""
	}
	return sourceProtocolPrompt(r.citationsEnabled)
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

func (r *sourceRegistry) registerLegacyToolReferences(text string, evidence bool) {
	if r == nil || text == "" {
		return
	}
	r.registerLabeledReferences(text)
	for _, tag := range legacyChunkRE.FindAllString(text, -1) {
		chunkID := firstNonEmpty(publicAttr(chunkAttrRE, tag), publicAttr(faqAttrRE, tag))
		if chunkID == "" {
			continue
		}
		r.registerChunk(ChunkReference{
			ChunkID:         chunkID,
			KnowledgeID:     publicAttr(documentAttrRE, tag),
			KnowledgeBaseID: firstNonEmpty(publicAttr(kbAttrRE, tag), publicAttr(publicKBAttrRE, tag)),
			DocumentTitle:   firstNonEmpty(publicAttr(knowledgeTitleAttrRE, tag), publicAttr(docAttrRE, tag)),
		}, evidence)
	}
}

// CompactPublicCitations folds canonical citations into the private protocol.
// Historical citations register navigation handles only; citations returned by
// a successful current source tool can also authorize evidence.
func (r *sourceRegistry) CompactPublicCitations(text string, evidence bool) string {
	if r == nil || text == "" {
		return text
	}
	text = publicKBTagRE.ReplaceAllStringFunc(text, func(tag string) string {
		chunkID := publicAttr(chunkAttrRE, tag)
		if chunkID == "" {
			return tag
		}
		handle := r.registerChunk(ChunkReference{
			ChunkID:         chunkID,
			KnowledgeBaseID: publicAttr(publicKBAttrRE, tag),
			DocumentTitle:   publicAttr(docAttrRE, tag),
		}, evidence)
		return `<ref id="` + handle + `"/>`
	})
	return publicWebTagRE.ReplaceAllStringFunc(text, func(tag string) string {
		rawURL := publicAttr(urlAttrRE, tag)
		if rawURL == "" {
			return tag
		}
		handle := r.registerWeb(rawURL, publicAttr(titleAttrRE, tag), evidence)
		return `<ref id="` + handle + `"/>`
	})
}

func publicAttr(expression *regexp.Regexp, tag string) string {
	match := expression.FindStringSubmatch(tag)
	if len(match) != 2 {
		return ""
	}
	return html.UnescapeString(match[1])
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
func (r *sourceRegistry) registerLabeledReferences(text string) {
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
// <kb/> / <web/> contract. Unknown handles fail closed and disappear.
func (r *sourceRegistry) ExpandText(text string) string {
	if r == nil || text == "" {
		return text
	}
	// Public citation tags are output-only. Drop any instance written directly
	// by the model, then create canonical tags solely from registered handles.
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
		handle := strings.ToLower(match[1])
		if _, citable := r.citable.Load(handle); !citable {
			return ""
		}
		if chunkID, chunkRef, ok := r.chunks.resolve(handle); ok {
			attrs := fmt.Sprintf(`doc="%s" chunk_id="%s"`, escapeAttr(chunkRef.DocumentTitle), escapeAttr(chunkID))
			if chunkRef.KnowledgeBaseID != "" {
				attrs += fmt.Sprintf(` kb_id="%s"`, escapeAttr(chunkRef.KnowledgeBaseID))
			}
			return "<kb " + attrs + " />"
		}
		if rawURL, web, ok := r.webs.resolve(handle); ok {
			return fmt.Sprintf(`<web url="%s" title="%s" />`, escapeAttr(rawURL), escapeAttr(web.title))
		}
		return ""
	})
}

func escapeAttr(value string) string { return html.EscapeString(value) }

// citationStreamExpander prevents partial private <ref/> tags from reaching SSE while
// preserving normal streaming for all other content.
type citationStreamExpander struct {
	registry *sourceRegistry
	pending  string
}

func newCitationStreamExpander(registry *sourceRegistry) *citationStreamExpander {
	return &citationStreamExpander{registry: registry}
}

func (d *citationStreamExpander) Feed(chunk string) string {
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

func (d *citationStreamExpander) Flush() string {
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
