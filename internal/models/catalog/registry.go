package catalog

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// GenericID is the catch-all vendor for any OpenAI-compatible endpoint.
const GenericID = "generic"

var (
	mu      sync.RWMutex
	vendors = map[string]*Vendor{}
)

// Register adds a built-in vendor. Registering the same id twice replaces
// the earlier definition (the deployment overlay relies on this).
func Register(v *Vendor) {
	if v == nil || v.ID == "" {
		panic("catalog: vendor without id")
	}
	if v.API == "" {
		v.API = api.APIOpenAICompletions
	}
	if v.Auth == "" {
		v.Auth = AuthBearer
	}
	for i := range v.Models {
		if v.Models[i].Type == "" {
			v.Models[i].Type = types.ModelTypeKnowledgeQA
		}
		if v.Models[i].API == "" && v.Models[i].Type == types.ModelTypeKnowledgeQA {
			v.Models[i].API = v.API
		}
	}
	mu.Lock()
	defer mu.Unlock()
	vendors[v.ID] = v
}

// Get returns a vendor by id.
func Get(id string) (*Vendor, bool) {
	mu.RLock()
	defer mu.RUnlock()
	v, ok := vendors[strings.ToLower(strings.TrimSpace(id))]
	return v, ok
}

// GetOrGeneric returns the vendor or the generic catch-all. When no vendor
// package is linked into the binary (unit tests of leaf packages), a
// built-in generic definition keeps resolution working.
func GetOrGeneric(id string) *Vendor {
	if v, ok := Get(id); ok {
		return v
	}
	if v, ok := Get(GenericID); ok {
		return v
	}
	return fallbackGeneric
}

// fallbackGeneric mirrors internal/models/vendors/generic so behaviour does
// not depend on whether that package was imported.
var fallbackGeneric = &Vendor{
	ID:              GenericID,
	Name:            "Custom (OpenAI-compatible)",
	API:             api.APIOpenAICompletions,
	DefaultBaseURLs: map[types.ModelType]string{},
	ModelTypes: []types.ModelType{
		types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding, types.ModelTypeRerank,
		types.ModelTypeVLLM, types.ModelTypeASR,
	},
	Auth: AuthBearer,
	Compat: VendorCompat{OpenAICompletions: OpenAICompletionsCompat{
		MaxTokensField: Ptr("max_tokens"),
		ThinkingFormat: Ptr(ThinkingFormatChatTemplateKwargs),
	}},
}

// List returns every vendor ordered by Order then id.
func List() []*Vendor {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]*Vendor, 0, len(vendors))
	for _, v := range vendors {
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ListByType returns vendors that support a model type.
func ListByType(modelType types.ModelType) []*Vendor {
	all := List()
	out := make([]*Vendor, 0, len(all))
	for _, v := range all {
		if v.SupportsType(modelType) {
			out = append(out, v)
		}
	}
	return out
}

// DetectByURL finds the vendor whose URLPatterns match a base URL. Used only
// when a stored model row carries no provider id. Longer patterns win so
// "api.lkeap.cloud.tencent.com" beats "tencent".
func DetectByURL(baseURL string) string {
	if baseURL == "" {
		return GenericID
	}
	lower := strings.ToLower(baseURL)
	bestID, bestLen := GenericID, 0
	for _, v := range List() {
		for _, p := range v.URLPatterns {
			if p != "" && strings.Contains(lower, strings.ToLower(p)) && len(p) > bestLen {
				bestID, bestLen = v.ID, len(p)
			}
		}
	}
	return bestID
}

// ModelsByType returns the vendor's catalog entries for a model type,
// hiding deprecated and pattern-only entries.
func (v *Vendor) ModelsByType(modelType types.ModelType) []ModelSpec {
	out := make([]ModelSpec, 0)
	for _, m := range v.Models {
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
				out = append(out, m)
			}
		default:
			if mt == modelType {
				out = append(out, m)
			}
		}
	}
	return out
}

// FindModel looks a model name up in the vendor catalog: exact id, then
// aliases, then glob patterns (longest literal prefix wins). Matching is
// case-insensitive.
func (v *Vendor) FindModel(name string) (ModelSpec, bool) {
	needle := strings.ToLower(strings.TrimSpace(name))
	if needle == "" {
		return ModelSpec{}, false
	}
	for _, m := range v.Models {
		if strings.ToLower(m.ID) == needle {
			return m, true
		}
	}
	for _, m := range v.Models {
		for _, alias := range m.Aliases {
			if strings.ToLower(alias) == needle {
				return m, true
			}
		}
	}
	var best ModelSpec
	bestScore := -1
	for _, m := range v.Models {
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
		return best, true
	}
	return ModelSpec{}, false
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

func defaultValidate(v *Vendor, cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if v.RequiresAuth && v.Auth != AuthSigned && strings.TrimSpace(cfg.APIKey) == "" {
		return fmt.Errorf("API key is required for %s", v.Name)
	}
	if v.ID == GenericID && strings.TrimSpace(cfg.BaseURL) == "" {
		return fmt.Errorf("base URL is required for generic provider")
	}
	if strings.TrimSpace(cfg.ModelName) == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// reset clears the registry and returns a function that puts the previous
// contents back (tests only). The registry is process-global, so a test that
// merely cleared it would leave every later test in the same binary — the
// external catalog_test package included — resolving against an empty
// catalog, which silently turns assertions about real vendors into
// assertions about the generic fallback.
func reset() func() {
	mu.Lock()
	defer mu.Unlock()
	previous := vendors
	vendors = map[string]*Vendor{}
	return func() {
		mu.Lock()
		defer mu.Unlock()
		vendors = previous
	}
}
