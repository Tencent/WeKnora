// Package catalog loads and queries model metadata. It has no provider behavior or global registry.
package catalog

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/models"
	"github.com/Tencent/WeKnora/internal/models/internal/configcopy"
	"github.com/Tencent/WeKnora/internal/types"
)

// Catalog is an immutable model index for one provider. Queries return owned copies.
type Catalog struct{ entries []models.ModelSpec }

// New builds an immutable index from an owned copy of the supplied entries.
func New(entries []models.ModelSpec) *Catalog { return &Catalog{entries: configcopy.Clone(entries)} }

// Models returns independent copies of all entries, including hidden rules.
func (c *Catalog) Models() []models.ModelSpec { return configcopy.Clone(c.entries) }

// ModelsByType returns the vendor's catalog entries for a model type,
// hiding deprecated and pattern-only entries.
func (c *Catalog) ModelsByType(modelType types.ModelType) []models.ModelSpec {
	out := make([]models.ModelSpec, 0)
	for _, m := range c.entries {
		if m.Deprecated || m.ID == "" {
			continue
		}
		mt := m.Type
		if mt == "" {
			mt = types.ModelTypeKnowledgeQA
		}
		switch modelType {
		case types.ModelTypeVLLM:
			// VLM pickers list chat models that accept images.
			if mt == types.ModelTypeKnowledgeQA && m.AcceptsImages() {
				out = append(out, configcopy.Clone(m))
			}
		default:
			if mt == modelType {
				out = append(out, configcopy.Clone(m))
			}
		}
	}
	return out
}

// FindModel looks a model name up among the vendor's entries of one model
// type: exact id, then aliases, then glob patterns (longest literal prefix
// wins). Matching is case-insensitive.
//
// The type is part of the key because families share prefixes across types.
// Aliyun's qwen3* chat glob also matches qwen3.8-text-embedding, and gpt-5*
// matches gpt-5-embed; an untyped lookup would hand an embedding row the chat
// family's compat, which the embedding overlay rejects, and the row could not
// be built at all. A VLM row is a chat model that accepts images, so it looks
// among the chat entries.
func (c *Catalog) FindModel(name string, modelType types.ModelType) (models.ModelSpec, bool) {
	needle := strings.ToLower(strings.TrimSpace(name))
	if needle == "" {
		return models.ModelSpec{}, false
	}
	want := EntryType(modelType)
	var candidates []models.ModelSpec
	for _, m := range c.entries {
		if EntryType(m.Type) == want {
			candidates = append(candidates, m)
		}
	}
	for _, m := range candidates {
		if strings.ToLower(m.ID) == needle {
			return configcopy.Clone(m), true
		}
	}
	for _, m := range candidates {
		for _, alias := range m.Aliases {
			if strings.ToLower(alias) == needle {
				return configcopy.Clone(m), true
			}
		}
	}
	var best models.ModelSpec
	bestScore := -1
	for _, m := range candidates {
		if m.Match == "" {
			continue
		}
		if globMatch(strings.ToLower(m.Match), needle) {
			score := len(strings.TrimRight(strings.SplitN(m.Match, "*", 2)[0], "*"))
			if score > bestScore {
				best, bestScore = m, score
			}
		}
	}
	if bestScore >= 0 {
		return configcopy.Clone(best), true
	}
	return models.ModelSpec{}, false
}

// EntryType folds a model type onto the catalog entries that describe it:
// chat entries leave Type empty, and VLM rows use them too.
func EntryType(t types.ModelType) types.ModelType {
	if t == "" || t == types.ModelTypeVLLM {
		return types.ModelTypeKnowledgeQA
	}
	return t
}

// globMatch supports '*' wildcards only.
func globMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(s, parts[i])
		if idx < 0 {
			return false
		}
		s = s[idx+len(parts[i]):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}
