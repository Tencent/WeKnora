package modelcontext

// source_text.go owns the textual syntax of source handles. Handles are
// scheme-shaped (kb://N, doc://N, chunk://N, url://N) so they cannot collide
// with prose such as "B1" or "W6"; that is what makes it safe to decode them
// in any text the model writes — reasoning, answers and free-text tool
// arguments alike — instead of only in a fixed list of ID fields.
//
// Durable text forms are what a decoded handle becomes outside an ID field:
// a web page becomes its URL, and workspace objects become weknora:// URIs
// that a later request encodes back into its own handles.
import (
	"regexp"
	"strings"
)

const (
	chunkHandlePrefix = "chunk://"
	docHandlePrefix   = "doc://"
	kbHandlePrefix    = "kb://"
	webHandlePrefix   = "url://"

	durableURIScheme = "weknora://"
)

const (
	durableKindKnowledgeBase = "kb"
	durableKindDocument      = "doc"
	durableKindChunk         = "chunk"
)

var sourceHandlePrefixes = []string{chunkHandlePrefix, docHandlePrefix, kbHandlePrefix, webHandlePrefix}

// sourceHandleTokenRE finds scheme-shaped tokens. The token body is matched
// greedily over identifier characters and validated in code, so "url://3a"
// is not mistaken for url://3 followed by text.
var sourceHandleTokenRE = regexp.MustCompile(`(?i)\b(?:chunk|doc|kb|url)://[0-9A-Za-z_\-]*`)

var sourceHandleNumberRE = regexp.MustCompile(`^[1-9][0-9]*$`)

// legacySourceHandleRE is the pre-scheme cN/dN/bN/wN syntax. It is accepted
// only as the whole value of a declared ID field, where it cannot be prose.
var legacySourceHandleRE = regexp.MustCompile(`(?i)^([cdbw])([1-9][0-9]*)$`)

var legacyHandlePrefixes = map[byte]string{
	'c': chunkHandlePrefix, 'd': docHandlePrefix, 'b': kbHandlePrefix, 'w': webHandlePrefix,
}

var durableURIRE = regexp.MustCompile(`(?i)weknora://(kb|doc|chunk)/([0-9A-Za-z][0-9A-Za-z_\-]*)`)

// parseSourceHandleToken returns the canonical handle for one scheme-shaped
// token, or "" when the token body is not a positive integer.
func parseSourceHandleToken(token string) string {
	lower := strings.ToLower(token)
	for _, prefix := range sourceHandlePrefixes {
		if strings.HasPrefix(lower, prefix) && sourceHandleNumberRE.MatchString(lower[len(prefix):]) {
			return lower
		}
	}
	return ""
}

// canonicalSourceHandle returns the canonical handle when value is exactly
// one source handle in the current or the legacy syntax.
func canonicalSourceHandle(value string) string {
	value = strings.TrimSpace(value)
	if handle := parseSourceHandleToken(value); handle != "" {
		return handle
	}
	if match := legacySourceHandleRE.FindStringSubmatch(value); len(match) == 3 {
		return legacyHandlePrefixes[strings.ToLower(match[1])[0]] + match[2]
	}
	return ""
}

func isSourceHandleShaped(value string) bool {
	return canonicalSourceHandle(value) != ""
}

// durableURI renders a workspace object as its durable text form.
func durableURI(kind, id string) string {
	return durableURIScheme + kind + "/" + id
}

// parseDurableURI returns the kind and ID when value is exactly one
// weknora:// URI.
func parseDurableURI(value string) (kind, id string, ok bool) {
	value = strings.TrimSpace(value)
	match := durableURIRE.FindStringSubmatch(value)
	if len(match) != 3 || match[0] != value {
		return "", "", false
	}
	return strings.ToLower(match[1]), match[2], true
}

// resolveSourceHandle returns the durable value behind a handle together with
// the table it lives in.
func (r *sourceRegistry) resolveSourceHandle(handle string) (prefix, durable string, chunk ChunkReference, ok bool) {
	if r == nil {
		return "", "", ChunkReference{}, false
	}
	handle = canonicalSourceHandle(handle)
	switch {
	case strings.HasPrefix(handle, chunkHandlePrefix):
		durable, chunk, ok = r.chunks.resolve(handle)
		return chunkHandlePrefix, durable, chunk, ok
	case strings.HasPrefix(handle, docHandlePrefix):
		durable, _, ok = r.docs.resolve(handle)
		return docHandlePrefix, durable, ChunkReference{}, ok
	case strings.HasPrefix(handle, kbHandlePrefix):
		durable, _, ok = r.kbs.resolve(handle)
		return kbHandlePrefix, durable, ChunkReference{}, ok
	case strings.HasPrefix(handle, webHandlePrefix):
		durable, _, ok = r.webs.resolve(handle)
		return webHandlePrefix, durable, ChunkReference{}, ok
	}
	return "", "", ChunkReference{}, false
}

// durableTextForHandle is the form a handle takes in stored or forwarded
// text: a URL for web pages, a weknora:// URI for workspace objects.
func (r *sourceRegistry) durableTextForHandle(handle string) string {
	prefix, durable, _, ok := r.resolveSourceHandle(handle)
	if !ok {
		return ""
	}
	switch prefix {
	case chunkHandlePrefix:
		return durableURI(durableKindChunk, durable)
	case docHandlePrefix:
		return durableURI(durableKindDocument, durable)
	case kbHandlePrefix:
		return durableURI(durableKindKnowledgeBase, durable)
	default:
		return durable
	}
}

func (r *sourceRegistry) rewriteHandleTokens(text string, rewrite func(token, handle string) string) string {
	if text == "" || !strings.Contains(text, "://") {
		return text
	}
	return sourceHandleTokenRE.ReplaceAllStringFunc(text, func(token string) string {
		handle := parseSourceHandleToken(token)
		if handle == "" {
			return token
		}
		return rewrite(token, handle)
	})
}

// DecodeDurableText restores every registered handle in free text to its
// durable text form. Unknown handles are left for the caller to reject.
func (r *sourceRegistry) DecodeDurableText(text string) string {
	if r == nil {
		return text
	}
	return r.rewriteHandleTokens(text, func(token, handle string) string {
		if durable := r.durableTextForHandle(handle); durable != "" {
			return durable
		}
		return token
	})
}

// DecodeBareText restores registered handles to their raw durable values.
// Structured expressions such as SQL need the bare ID, not a URI.
func (r *sourceRegistry) DecodeBareText(text string) string {
	if r == nil {
		return text
	}
	return r.rewriteHandleTokens(text, func(token, handle string) string {
		if durable := r.durableForHandle(handle); durable != "" {
			return durable
		}
		return token
	})
}

// UnresolvedTextHandles reports handle-shaped tokens that no table backs.
func (r *sourceRegistry) UnresolvedTextHandles(text string) []string {
	var unresolved []string
	r.rewriteHandleTokens(text, func(token, handle string) string {
		if r == nil || r.durableForHandle(handle) == "" {
			unresolved = append(unresolved, handle)
		}
		return token
	})
	return unresolved
}

// EncodeDurableURIs replaces weknora:// URIs with this request's handles.
// Registration only makes the object addressable; tools still authorize
// every durable ID they receive.
func (r *sourceRegistry) EncodeDurableURIs(text string) string {
	if r == nil || text == "" || !strings.Contains(strings.ToLower(text), durableURIScheme) {
		return text
	}
	return durableURIRE.ReplaceAllStringFunc(text, func(uri string) string {
		match := durableURIRE.FindStringSubmatch(uri)
		if handle := r.registerDurable(strings.ToLower(match[1]), match[2]); handle != "" {
			return handle
		}
		return uri
	})
}

func (r *sourceRegistry) registerDurable(kind, id string) string {
	switch kind {
	case durableKindKnowledgeBase:
		return r.RegisterKnowledgeBase(id)
	case durableKindDocument:
		return r.RegisterDocument(id)
	case durableKindChunk:
		return r.registerChunk(ChunkReference{ChunkID: id}, false)
	}
	return ""
}

// ExpandHandleText renders handles in user-visible text. Workspace objects
// become weknora:// links the client resolves, web pages become their URL,
// and a chunk becomes a citation when it is current evidence, or its
// document otherwise. Unknown handles are dropped, like unknown <ref/> tags.
func (r *sourceRegistry) ExpandHandleText(text string) string {
	if r == nil {
		return text
	}
	return r.rewriteHandleTokens(text, func(_, handle string) string {
		prefix, _, chunk, ok := r.resolveSourceHandle(handle)
		if !ok {
			return ""
		}
		if prefix != chunkHandlePrefix {
			return r.durableTextForHandle(handle)
		}
		if tag := r.citationTag(handle); tag != "" {
			return tag
		}
		if chunk.KnowledgeID != "" {
			return durableURI(durableKindDocument, chunk.KnowledgeID)
		}
		return ""
	})
}

// sourceHandleHoldLen is the trailing byte run a stream must withhold because
// the next provider chunk could still complete it into a handle.
func sourceHandleHoldLen(combined string) int {
	lower := strings.ToLower(combined)
	hold := 0
	for _, prefix := range sourceHandlePrefixes {
		if idx := strings.LastIndex(lower, prefix); idx >= 0 && atTokenBoundary(lower, idx) {
			if rest := lower[idx+len(prefix):]; rest == "" || allDigits(rest) {
				hold = max(hold, len(lower)-idx)
			}
		}
		for n := 1; n < len(prefix); n++ {
			start := len(lower) - n
			if start >= 0 && strings.HasSuffix(lower, prefix[:n]) && atTokenBoundary(lower, start) {
				hold = max(hold, n)
			}
		}
	}
	return hold
}

func atTokenBoundary(text string, idx int) bool {
	if idx == 0 {
		return true
	}
	previous := text[idx-1]
	isIdentifierByte := previous >= 'a' && previous <= 'z' || previous >= 'A' && previous <= 'Z' ||
		previous >= '0' && previous <= '9' || previous == '_'
	return !isIdentifierByte
}

// sourceHandleStreamDecoder expands handles split across provider chunks.
type sourceHandleStreamDecoder struct {
	hold streamHold
}

func newSourceHandleStreamDecoder(registry *sourceRegistry) *sourceHandleStreamDecoder {
	return &sourceHandleStreamDecoder{hold: streamHold{
		holdLen: sourceHandleHoldLen,
		emit:    registry.ExpandHandleText,
		flush:   registry.ExpandHandleText,
	}}
}

func (d *sourceHandleStreamDecoder) Feed(chunk string) string {
	if d == nil {
		return chunk
	}
	return d.hold.Feed(chunk)
}

func (d *sourceHandleStreamDecoder) Flush() string {
	if d == nil {
		return ""
	}
	return d.hold.Flush()
}
