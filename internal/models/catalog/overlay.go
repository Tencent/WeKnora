package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// OverlayFile is the schema of config/models.json. It mirrors pi's
// ~/.pi/agent/models.json: a "providers" object keyed by provider id where
// each entry either patches a built-in vendor or declares a new one.
type OverlayFile struct {
	Providers map[string]OverlayProvider `json:"providers"`
}

// OverlayProvider patches or declares one vendor.
type OverlayProvider struct {
	Name         string            `json:"name,omitempty"`
	Names        map[string]string `json:"names,omitempty"`
	Description  string            `json:"description,omitempty"`
	Descriptions map[string]string `json:"descriptions,omitempty"`
	Website      string            `json:"website,omitempty"`
	// API is the default chat protocol for new vendors (and overrides it
	// for built-in ones).
	API api.API `json:"api,omitempty"`
	// BaseURL sets the chat base URL; BaseURLs sets per-type URLs keyed by
	// chat | embedding | rerank | vlm | asr.
	BaseURL  string            `json:"base_url,omitempty"`
	BaseURLs map[string]string `json:"base_urls,omitempty"`
	// APIKey is the deployment-level key used when a model row stores none.
	// Supports ${ENV} / $ENV interpolation.
	APIKey       string            `json:"api_key,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Auth         AuthStyle         `json:"auth,omitempty"`
	RequiresAuth *bool             `json:"requires_auth,omitempty"`
	ModelTypes   []string          `json:"model_types,omitempty"`
	URLPatterns  []string          `json:"url_patterns,omitempty"`
	// Icon is a path to an SVG file (relative to the overlay file) or an
	// inline "<svg ...>" string.
	Icon string `json:"icon,omitempty"`
	// Compat is the flat protocol overlay applied at vendor level for API.
	Compat json.RawMessage `json:"compat,omitempty"`
	// ThinkingLevels patches the vendor level map.
	ThinkingLevels map[string]*string `json:"thinking_levels,omitempty"`
	// Models are upserted by id into the vendor catalog.
	Models []ModelSpec `json:"models,omitempty"`
	// ModelOverrides patch existing entries by id without restating them.
	ModelOverrides map[string]ModelSpecPatch `json:"model_overrides,omitempty"`
}

// ModelSpecPatch is a partial ModelSpec.
type ModelSpecPatch struct {
	Name            string               `json:"name,omitempty"`
	API             api.API              `json:"api,omitempty"`
	Reasoning       *bool                `json:"reasoning,omitempty"`
	Input           []string             `json:"input,omitempty"`
	ContextWindow   int                  `json:"context_window,omitempty"`
	MaxOutputTokens int                  `json:"max_output_tokens,omitempty"`
	Cost            *ModelCost           `json:"cost,omitempty"`
	ThinkingLevels  api.ThinkingLevelMap `json:"thinking_levels,omitempty"`
	Compat          json.RawMessage      `json:"compat,omitempty"`
	Aliases         []string             `json:"aliases,omitempty"`
	Deprecated      *bool                `json:"deprecated,omitempty"`
}

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// interpolateEnv expands ${NAME} and $NAME. Unset variables expand to the
// empty string so a missing key fails loudly upstream rather than sending
// the literal placeholder.
func interpolateEnv(s string) string {
	return envPattern.ReplaceAllStringFunc(s, func(m string) string {
		name := strings.Trim(strings.TrimPrefix(m, "$"), "{}")
		return os.Getenv(name)
	})
}

var frontendTypes = map[string]types.ModelType{
	"chat":      types.ModelTypeKnowledgeQA,
	"embedding": types.ModelTypeEmbedding,
	"rerank":    types.ModelTypeRerank,
	"vlm":       types.ModelTypeVLLM,
	"vllm":      types.ModelTypeVLLM,
	"asr":       types.ModelTypeASR,
}

// ParseModelType accepts both frontend ("chat") and backend ("KnowledgeQA")
// spellings.
func ParseModelType(s string) (types.ModelType, bool) {
	if t, ok := frontendTypes[strings.ToLower(strings.TrimSpace(s))]; ok {
		return t, true
	}
	switch types.ModelType(s) {
	case types.ModelTypeKnowledgeQA, types.ModelTypeEmbedding, types.ModelTypeRerank,
		types.ModelTypeVLLM, types.ModelTypeASR:
		return types.ModelType(s), true
	}
	return "", false
}

// LoadOverlay reads config/models.json (or the path in MODELS_CONFIG) and
// applies it to the registry. A missing file is not an error.
func LoadOverlay(configDir string) error {
	path := os.Getenv("MODELS_CONFIG")
	if path == "" {
		path = filepath.Join(configDir, "models.json")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	return ApplyOverlay(data, filepath.Dir(path))
}

// ApplyOverlay applies overlay JSON to the registry. baseDir resolves
// relative icon paths.
func ApplyOverlay(data []byte, baseDir string) error {
	var file OverlayFile
	dec := json.NewDecoder(bytesReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return fmt.Errorf("parse models overlay: %w", err)
	}
	for id, p := range file.Providers {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			continue
		}
		vendor, exists := Get(id)
		if !exists {
			vendor = &Vendor{
				ID:              id,
				Name:            id,
				API:             api.APIOpenAICompletions,
				DefaultBaseURLs: map[types.ModelType]string{},
				ModelTypes:      []types.ModelType{types.ModelTypeKnowledgeQA},
				RequiresAuth:    true,
				Auth:            AuthBearer,
				Order:           1000,
			}
		} else {
			copied := *vendor
			vendor = &copied
			vendor.Models = append([]ModelSpec(nil), vendor.Models...)
			urls := make(map[types.ModelType]string, len(vendor.DefaultBaseURLs))
			for k, v := range vendor.DefaultBaseURLs {
				urls[k] = v
			}
			vendor.DefaultBaseURLs = urls
		}
		if err := applyOverlayProvider(vendor, p, baseDir); err != nil {
			return fmt.Errorf("provider %s: %w", id, err)
		}
		Register(vendor)
	}
	return nil
}

func applyOverlayProvider(v *Vendor, p OverlayProvider, baseDir string) error {
	if p.Name != "" {
		v.Name = p.Name
	}
	if len(p.Names) > 0 {
		v.Names = p.Names
	}
	if p.Description != "" {
		v.Description = p.Description
	}
	if len(p.Descriptions) > 0 {
		v.Descriptions = p.Descriptions
	}
	if p.Website != "" {
		v.Website = p.Website
	}
	if p.API != "" {
		if !p.API.Known() {
			return fmt.Errorf("unknown api %q", p.API)
		}
		v.API = p.API
	}
	if p.BaseURL != "" {
		v.DefaultBaseURLs[types.ModelTypeKnowledgeQA] = interpolateEnv(p.BaseURL)
	}
	for k, u := range p.BaseURLs {
		t, ok := ParseModelType(k)
		if !ok {
			return fmt.Errorf("unknown model type %q in base_urls", k)
		}
		v.DefaultBaseURLs[t] = interpolateEnv(u)
	}
	if p.APIKey != "" {
		v.DefaultAPIKey = interpolateEnv(p.APIKey)
	}
	if len(p.Headers) > 0 {
		headers := make(map[string]string, len(p.Headers))
		for k, val := range p.Headers {
			headers[k] = interpolateEnv(val)
		}
		v.Headers = headers
	}
	if p.Auth != "" {
		v.Auth = p.Auth
	}
	if p.RequiresAuth != nil {
		v.RequiresAuth = *p.RequiresAuth
	}
	if len(p.ModelTypes) > 0 {
		v.ModelTypes = v.ModelTypes[:0]
		for _, s := range p.ModelTypes {
			t, ok := ParseModelType(s)
			if !ok {
				return fmt.Errorf("unknown model type %q", s)
			}
			v.ModelTypes = append(v.ModelTypes, t)
		}
	}
	if len(p.URLPatterns) > 0 {
		v.URLPatterns = p.URLPatterns
	}
	if p.Icon != "" {
		if strings.HasPrefix(strings.TrimSpace(p.Icon), "<svg") {
			v.Icon = []byte(p.Icon)
		} else {
			iconPath := p.Icon
			if !filepath.IsAbs(iconPath) {
				iconPath = filepath.Join(baseDir, iconPath)
			}
			data, err := os.ReadFile(iconPath)
			if err != nil {
				return fmt.Errorf("read icon: %w", err)
			}
			v.Icon = data
		}
	}
	if len(p.Compat) > 0 {
		var err error
		switch v.API {
		case api.APIOpenAICompletions:
			err = decodeCompat(p.Compat, &v.Compat.OpenAICompletions)
		case api.APIOpenAIResponses:
			err = decodeCompat(p.Compat, &v.Compat.OpenAIResponses)
		case api.APIAnthropicMessages:
			err = decodeCompat(p.Compat, &v.Compat.AnthropicMessages)
		case api.APIGoogleGenerativeAI:
			err = decodeCompat(p.Compat, &v.Compat.GoogleGenerativeAI)
		}
		if err != nil {
			return err
		}
	}
	if len(p.ThinkingLevels) > 0 {
		levels := api.ThinkingLevelMap{}
		for k, val := range v.ThinkingLevels {
			levels[k] = val
		}
		for k, val := range p.ThinkingLevels {
			levels[api.ReasoningEffort(k)] = val
		}
		v.ThinkingLevels = levels
	}
	for _, m := range p.Models {
		if m.ID == "" {
			return fmt.Errorf("model without id")
		}
		replaced := false
		for i := range v.Models {
			if strings.EqualFold(v.Models[i].ID, m.ID) {
				v.Models[i] = m
				replaced = true
				break
			}
		}
		if !replaced {
			v.Models = append(v.Models, m)
		}
	}
	for id, patch := range p.ModelOverrides {
		found := false
		for i := range v.Models {
			if strings.EqualFold(v.Models[i].ID, id) {
				applyPatch(&v.Models[i], patch)
				found = true
				break
			}
		}
		if !found {
			spec := ModelSpec{ID: id}
			applyPatch(&spec, patch)
			v.Models = append(v.Models, spec)
		}
	}
	return nil
}

func applyPatch(m *ModelSpec, p ModelSpecPatch) {
	if p.Name != "" {
		m.Name = p.Name
	}
	if p.API != "" {
		m.API = p.API
	}
	if p.Reasoning != nil {
		m.Reasoning = *p.Reasoning
	}
	if len(p.Input) > 0 {
		m.Input = p.Input
	}
	if p.ContextWindow > 0 {
		m.ContextWindow = p.ContextWindow
	}
	if p.MaxOutputTokens > 0 {
		m.MaxOutputTokens = p.MaxOutputTokens
	}
	if p.Cost != nil {
		m.Cost = p.Cost
	}
	if len(p.ThinkingLevels) > 0 {
		levels := api.ThinkingLevelMap{}
		for k, v := range m.ThinkingLevels {
			levels[k] = v
		}
		for k, v := range p.ThinkingLevels {
			levels[k] = v
		}
		m.ThinkingLevels = levels
	}
	if len(p.Compat) > 0 {
		m.Compat = mergeRawObjects(m.Compat, p.Compat)
	}
	if len(p.Aliases) > 0 {
		m.Aliases = append(m.Aliases, p.Aliases...)
	}
	if p.Deprecated != nil {
		m.Deprecated = *p.Deprecated
	}
}

// mergeRawObjects shallow-merges two JSON objects (b wins).
func mergeRawObjects(a, b json.RawMessage) json.RawMessage {
	merged := map[string]json.RawMessage{}
	if len(a) > 0 {
		_ = json.Unmarshal(a, &merged)
	}
	patch := map[string]json.RawMessage{}
	if len(b) > 0 {
		_ = json.Unmarshal(b, &patch)
	}
	for k, v := range patch {
		merged[k] = v
	}
	out, _ := json.Marshal(merged)
	return out
}

// ModelsFile is the schema of a vendor's embedded models.json.
type ModelsFile struct {
	Vendor string      `json:"vendor"`
	Source string      `json:"source,omitempty"`
	Models []ModelSpec `json:"models"`
}

// MustParseModels decodes an embedded models.json; vendors call it from
// package init, so a malformed file fails the build's tests immediately.
func MustParseModels(data []byte) []ModelSpec {
	var file ModelsFile
	dec := json.NewDecoder(bytesReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		panic(fmt.Sprintf("catalog: invalid models.json: %v", err))
	}
	return file.Models
}
