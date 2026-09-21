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
	// Models are upserted by id into the vendor catalog. Entries stay raw so
	// an upsert against an existing id can patch only the keys the operator
	// actually wrote (see applyOverlayProvider).
	Models []json.RawMessage `json:"models,omitempty"`
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
		icon, err := loadOverlayIcon(baseDir, p.Icon)
		if err != nil {
			return err
		}
		v.Icon = icon
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
	for _, raw := range p.Models {
		if err := upsertOverlayModel(v, raw); err != nil {
			return err
		}
	}
	for id, patch := range p.ModelOverrides {
		found := false
		for i := range v.Models {
			if strings.EqualFold(v.Models[i].ID, id) {
				// Clone first, for the same reason as upsertOverlayModel: the
				// entry's slices are shared with the built-in definition.
				spec := cloneModelSpec(v.Models[i])
				applyPatch(&spec, patch)
				v.Models[i] = spec
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

// maxIconBytes caps a vendor icon. Icons are base64'd into every
// GET /models/providers response, so an oversized file is a response-size
// problem for every viewer, not just a disk read.
const maxIconBytes = 256 * 1024

// loadOverlayIcon resolves providers.*.icon to SVG bytes.
//
// The bytes are handed to every viewer of the model pages as a data: URI, so
// the path is treated as untrusted even though writing the overlay already
// requires filesystem access: an operator (or a config-management template)
// that points `icon` at /etc/shadow or ../../secrets must not turn the
// provider list into a file-disclosure endpoint for read-only tenants.
// Hence: no absolute paths, no escaping the overlay's own directory
// (symlinks included), a size cap, and the content must really be an SVG.
func loadOverlayIcon(baseDir, ref string) ([]byte, error) {
	if looksLikeSVG([]byte(ref)) {
		if len(ref) > maxIconBytes {
			return nil, fmt.Errorf("icon: inline svg is %d bytes, limit is %d", len(ref), maxIconBytes)
		}
		return []byte(ref), nil
	}
	if filepath.IsAbs(ref) || strings.HasPrefix(ref, "/") || filepath.VolumeName(ref) != "" {
		return nil, fmt.Errorf("icon: absolute paths are not allowed: %q", ref)
	}
	clean := filepath.Clean(filepath.FromSlash(ref))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("icon: path escapes the overlay directory: %q", ref)
	}
	if baseDir == "" {
		baseDir = "."
	}
	full, err := resolveInsideDir(baseDir, clean)
	if err != nil {
		return nil, fmt.Errorf("icon: %w", err)
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, fmt.Errorf("read icon: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("icon: %q is not a regular file", ref)
	}
	if info.Size() > maxIconBytes {
		return nil, fmt.Errorf("icon: %q is %d bytes, limit is %d", ref, info.Size(), maxIconBytes)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("read icon: %w", err)
	}
	if len(data) > maxIconBytes {
		return nil, fmt.Errorf("icon: %q is %d bytes, limit is %d", ref, len(data), maxIconBytes)
	}
	if !looksLikeSVG(data) {
		return nil, fmt.Errorf("icon: %q is not an SVG document", ref)
	}
	return data, nil
}

// resolveInsideDir joins rel onto dir and fails unless the result stays
// inside it. Both sides are resolved through EvalSymlinks so a symlink
// planted in the overlay directory cannot point out of it; a path that does
// not exist is compared unresolved and left to the caller's os.Stat, which
// reports it with a clearer message.
func resolveInsideDir(dir, rel string) (string, error) {
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		realDir = filepath.Clean(dir)
	}
	full := filepath.Join(realDir, rel)
	if resolved, err := filepath.EvalSymlinks(full); err == nil {
		full = resolved
	}
	if full != realDir && !strings.HasPrefix(full, realDir+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes the overlay directory: %q", rel)
	}
	return full, nil
}

// looksLikeSVG reports whether data is an SVG document: an optional XML
// prologue (declaration, doctype, comments, whitespace) followed by <svg.
func looksLikeSVG(data []byte) bool {
	s := strings.TrimPrefix(string(data), "\ufeff")
	for {
		s = strings.TrimSpace(s)
		switch {
		case strings.HasPrefix(s, "<?"):
			s = skipPast(s, "?>")
		case strings.HasPrefix(s, "<!--"):
			s = skipPast(s, "-->")
		case strings.HasPrefix(s, "<!"):
			s = skipPast(s, ">")
		default:
			lower := strings.ToLower(s)
			if !strings.HasPrefix(lower, "<svg") || len(lower) == len("<svg") {
				return false
			}
			// Reject "<svgfoo": the tag must end right after the name.
			return strings.ContainsRune(" \t\r\n>/", rune(lower[len("<svg")]))
		}
		if s == "" {
			return false
		}
	}
}

// skipPast returns what follows the first occurrence of end, or "" if the
// token never closes.
func skipPast(s, end string) string {
	i := strings.Index(s, end)
	if i < 0 {
		return ""
	}
	return s[i+len(end):]
}

// upsertOverlayModel applies one providers.*.models entry.
//
// A new id creates a full entry. An id that already exists is *patched*, not
// replaced: both config/models.json.example and the docs present `models` as
// the way to restate a known model with corrected facts, and most fields of
// ModelSpec cannot tell "absent" from "zero" — Reasoning is a plain bool, so
// an entry that only fixes context_window used to silently turn reasoning
// off and drop thinking levels, compat and input along with it. Unmarshaling
// the operator's JSON onto a copy of the stored entry leaves every key they
// did not write exactly as it was.
func upsertOverlayModel(v *Vendor, raw json.RawMessage) error {
	// Decode strictly first: this is what rejects typos and wrong types, the
	// same guarantee the rest of the overlay gives. The value is used only
	// for the id, since the merge below re-reads the raw bytes.
	var spec ModelSpec
	dec := json.NewDecoder(bytesReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return fmt.Errorf("model entry: %w", err)
	}
	if spec.ID == "" {
		return fmt.Errorf("model without id")
	}
	for i := range v.Models {
		if !strings.EqualFold(v.Models[i].ID, spec.ID) {
			continue
		}
		// cloneModelSpec first: the vendor's Models slice was only shallow
		// copied, so unmarshaling into the entry's slices/maps in place would
		// write through to the built-in vendor's package-level definition.
		merged := cloneModelSpec(v.Models[i])
		if err := json.Unmarshal(raw, &merged); err != nil {
			return fmt.Errorf("model %s: %w", spec.ID, err)
		}
		v.Models[i] = merged
		return nil
	}
	v.Models = append(v.Models, spec)
	return nil
}

// cloneModelSpec deep-copies the reference-typed fields of a spec.
func cloneModelSpec(m ModelSpec) ModelSpec {
	out := m
	out.Input = append([]string(nil), m.Input...)
	out.Aliases = append([]string(nil), m.Aliases...)
	out.Compat = append(json.RawMessage(nil), m.Compat...)
	if m.ThinkingLevels != nil {
		levels := make(api.ThinkingLevelMap, len(m.ThinkingLevels))
		for k, v := range m.ThinkingLevels {
			levels[k] = v
		}
		out.ThinkingLevels = levels
	}
	return out
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
	// The file-level source is the vendor's model documentation, and most
	// entries omit their own because that page covers them. Pushing it down
	// means every entry can answer "where is this documented" — the editor
	// links to it from the model picker.
	for i := range file.Models {
		if file.Models[i].Source == "" {
			file.Models[i].Source = file.Source
		}
	}
	return file.Models
}
