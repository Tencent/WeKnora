// resources.go is the durable-resource half of the model-context registry:
// it assigns request-local res://NNNN handles to stored resource references
// and restores them before application code consumes model output.
package modelcontext

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// storedRefRE also recognizes legacy physical references. New writes persist
// resource:// handles, but old chunks and message history can still contain a
// provider URL. Giving both forms the same request-local alias makes rollout
// safe without a blocking full-table content rewrite.
//
// The final alternative aliases wiki summary-page slugs (summary/<uuid>). They
// are not storage handles, but they share the exact failure mode this registry
// exists to prevent: a high-entropy identifier the model must reproduce
// verbatim (inside [[slug|display]] links and as wiki-tool slug arguments).
// Models routinely mangle the UUID by inserting or dropping hex digits, which
// yields dead cross-links. Aliasing the slug to a low-entropy res:// token
// removes the opportunity to mangle at the source; the token round-trips back
// to the real slug on stream output and in decoded tool-call arguments before
// anything is persisted. Entity slugs (entity/<readable-title>) are low-entropy
// and semantically meaningful, so they are deliberately left untouched.
var storedRefRE = regexp.MustCompile(
	`resource://[0-9A-Za-z_-]{22}|` +
		`(?:storage://[0-9A-Za-z_-]+/)?` +
		`(?:local|minio|cos|tos|s3|oss|ks3|obs)://[^\s)\]>"']+|` +
		`summary/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`,
)

// aliasShapeRE matches the alias syntax produced by EncodeText. It is used only
// to spot alias-shaped tokens the model emitted that the registry cannot map
// back — either a hallucinated reference or a coincidental collision.
var aliasShapeRE = regexp.MustCompile(`res://\d+`)

// resourceRegistry assigns low-entropy, request-local aliases to stable resource
// handles. It is safe to reuse across all rounds of one Agent execution.
type resourceRegistry struct {
	mu         sync.RWMutex
	refToAlias map[string]string
	aliasToRef map[string]string
}

// newResourceRegistry creates an empty request-local alias registry.
func newResourceRegistry() *resourceRegistry {
	return &resourceRegistry{
		refToAlias: make(map[string]string),
		aliasToRef: make(map[string]string),
	}
}

// EncodeText replaces stored references with compact, stable aliases.
func (r *resourceRegistry) EncodeText(value string) string {
	if r == nil || value == "" {
		return value
	}
	return storedRefRE.ReplaceAllStringFunc(value, func(ref string) string {
		r.mu.Lock()
		defer r.mu.Unlock()
		if alias, ok := r.refToAlias[ref]; ok {
			return alias
		}
		// A URL-shaped alias (scheme://digits) keeps the token low-entropy while
		// looking enough like a link that the model reuses it verbatim inside
		// Markdown image/link syntax instead of reasoning about or rewriting it.
		alias := fmt.Sprintf("res://%04d", len(r.aliasToRef)+1)
		r.refToAlias[ref] = alias
		r.aliasToRef[alias] = ref
		return alias
	})
}

// DecodeText restores every alias currently known to the registry.
func (r *resourceRegistry) DecodeText(value string) string {
	if r == nil || value == "" {
		return value
	}
	r.mu.RLock()
	aliases := make([]string, 0, len(r.aliasToRef))
	for alias := range r.aliasToRef {
		aliases = append(aliases, alias)
	}
	sort.SliceStable(aliases, func(i, j int) bool { return len(aliases[i]) > len(aliases[j]) })
	decoded := value
	for _, alias := range aliases {
		decoded = strings.ReplaceAll(decoded, alias, r.aliasToRef[alias])
	}
	r.mu.RUnlock()
	return decoded
}

// StripOrphanAliases removes alias-shaped tokens after all known aliases have
// been restored. Use this only on model output; tool arguments must retain
// unknown aliases long enough for modelcontext to reject the call.
func (r *resourceRegistry) StripOrphanAliases(value string) string {
	if value == "" {
		return value
	}
	return aliasShapeRE.ReplaceAllString(value, "")
}

// EncodeMessages returns a copied message slice with textual references
// compacted. Binary/image content fields are intentionally left untouched.
func (r *resourceRegistry) EncodeMessages(messages []chat.Message) []chat.Message {
	if r == nil || len(messages) == 0 {
		return messages
	}
	encoded := make([]chat.Message, len(messages))
	copy(encoded, messages)
	for i := range encoded {
		encoded[i].Content = r.EncodeText(encoded[i].Content)
		encoded[i].ReasoningContent = r.EncodeText(encoded[i].ReasoningContent)
		if len(encoded[i].MultiContent) > 0 {
			encoded[i].MultiContent = append([]chat.MessageContentPart(nil), encoded[i].MultiContent...)
			for j := range encoded[i].MultiContent {
				encoded[i].MultiContent[j].Text = r.EncodeText(encoded[i].MultiContent[j].Text)
			}
		}
		if len(encoded[i].ToolCalls) > 0 {
			encoded[i].ToolCalls = append([]chat.ToolCall(nil), encoded[i].ToolCalls...)
			for j := range encoded[i].ToolCalls {
				encoded[i].ToolCalls[j].Function.Arguments = r.EncodeText(encoded[i].ToolCalls[j].Function.Arguments)
			}
		}
	}
	return encoded
}

// DecodeToolCalls restores aliases in tool-call JSON arguments.
func (r *resourceRegistry) DecodeToolCalls(toolCalls []types.LLMToolCall) {
	for i := range toolCalls {
		toolCalls[i].Function.Arguments = r.DecodeText(toolCalls[i].Function.Arguments)
	}
}

// OrphanAliases returns the distinct alias-shaped tokens in an already-decoded
// string that the registry cannot resolve. A non-empty result means the model
// emitted a reference no real resource backs (hallucination) or the user text
// happened to collide with the alias syntax. Callers should log/observe these
// rather than surfacing them to end users as broken links.
func (r *resourceRegistry) OrphanAliases(decoded string) []string {
	if decoded == "" {
		return nil
	}
	var orphans []string
	seen := make(map[string]struct{})
	for _, match := range aliasShapeRE.FindAllString(decoded, -1) {
		if r != nil {
			r.mu.RLock()
			_, known := r.aliasToRef[match]
			r.mu.RUnlock()
			if known {
				continue
			}
		}
		if _, dup := seen[match]; dup {
			continue
		}
		seen[match] = struct{}{}
		orphans = append(orphans, match)
	}
	return orphans
}

func (r *resourceRegistry) aliases() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	aliases := make([]string, 0, len(r.aliasToRef))
	for alias := range r.aliasToRef {
		aliases = append(aliases, alias)
	}
	return aliases
}

// resourceStreamDecoder holds only a suffix that could be the beginning of a known
// alias, allowing aliases split across provider chunks to round-trip exactly.
type resourceStreamDecoder struct {
	registry *resourceRegistry
	pending  string
}

// newResourceStreamDecoder creates an alias decoder for one streaming text channel.
func newResourceStreamDecoder(registry *resourceRegistry) *resourceStreamDecoder {
	return &resourceStreamDecoder{registry: registry}
}

// Feed decodes a chunk while retaining suffixes that may complete an alias in
// the next provider chunk.
func (d *resourceStreamDecoder) Feed(chunk string) string {
	if d == nil || d.registry == nil {
		return chunk
	}
	combined := d.pending + chunk
	d.pending = ""
	hold := 0
	for _, alias := range d.registry.aliases() {
		// A provider may split the token at any byte boundary, including
		// "re" + "s://0001". Holding at most the short matching suffix is the
		// only way to guarantee that a request-local handle never leaks.
		for n := 1; n < len(alias); n++ {
			if n > hold && strings.HasSuffix(combined, alias[:n]) {
				hold = n
			}
		}
	}
	if hold > 0 {
		d.pending = combined[len(combined)-hold:]
		combined = combined[:len(combined)-hold]
	}
	return d.registry.DecodeText(combined)
}

// Flush returns any buffered suffix when the provider stream closes.
func (d *resourceStreamDecoder) Flush() string {
	if d == nil {
		return ""
	}
	pending := d.pending
	d.pending = ""
	return d.registry.DecodeText(pending)
}
