// Package catalog provides the global model-parameter catalog (models.json,
// WeKnora's own schema, design §5.4) used to prefill model configuration
// forms. One copy serves all tenants; it is loaded asynchronously at startup
// and never blocks or fails startup — an unavailable catalog only degrades
// prefill (取值链 skips the catalog tier, ADR 0001/0002).
package catalog

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

//go:embed data/models.json
var embeddedCatalog []byte

// Source selects where the catalog loads from. Values are a fixed enum —
// arbitrary URLs are rejected (安全约束, PRD).
type Source string

const (
	SourceGitHub Source = "GITHUB"
	SourceCNB    Source = "CNB"
	SourceLocal  Source = "LOCAL"
)

// DefaultSource is LOCAL until the CNB/GitHub mirror URL is decided
// (user ruling 2026-09-09: 镜像地址后置，本期离线兜底). Switching the default
// back is a one-line change once mirror URLs exist.
const DefaultSource = SourceLocal

// mirrorURLs holds the fetch URL per remote source. Empty means the mirror
// has not been provisioned yet — the loader falls back to LOCAL.
var mirrorURLs = map[Source]string{
	SourceGitHub: "", // TODO(官方): models.json mirror URL once decided
	SourceCNB:    "", // TODO(官方): models.json mirror URL once decided
}

// Catalog is the parsed models.json. Providers maps WeKnora provider ids to
// their known model entries.
type Catalog struct {
	Version   string                   `json:"version"`
	Providers map[string]CatalogModels `json:"providers"`
}

// CatalogModels groups one provider's known models.
type CatalogModels struct {
	Models map[string]CatalogModel `json:"models"`
}

// CatalogModel is the per-model parameter entry (design §5.4 schema).
type CatalogModel struct {
	ContextWindow   int              `json:"context_window,omitempty"`
	MaxOutputTokens int              `json:"max_output_tokens,omitempty"`
	InputModalities []string         `json:"input_modalities,omitempty"`
	Thinking        *CatalogThinking `json:"thinking,omitempty"`
}

// CatalogThinking mirrors the provider ThinkingCaps shape so a catalog hit
// can prefill the model record's level selection (ADR 0002).
type CatalogThinking struct {
	Supported    bool     `json:"supported"`
	CanDisable   bool     `json:"can_disable"`
	Levels       []string `json:"levels,omitempty"`
	DefaultLevel string   `json:"default_level,omitempty"`
}

// fetchTimeout bounds remote catalog pulls (design §5.10.4).
const fetchTimeout = 10 * time.Second

// maxCatalogBytes bounds catalog completeness checks (design §5.10.4).
const maxCatalogBytes = 20 << 20 // 20MB

var (
	mu           sync.RWMutex
	current      *Catalog
	fallbackOnce sync.Once
)

// Load resolves the source, fetches and parses the catalog, and swaps it into
// the in-memory cache. Failure leaves the previous cache (or none) and returns
// an error for the caller to log — startup proceeds regardless.
func Load(ctx context.Context, source Source) error {
	if source == "" {
		source = DefaultSource
	}
	data, err := fetch(ctx, source)
	if err != nil {
		return err
	}
	cat, err := parse(data)
	if err != nil {
		return fmt.Errorf("parse catalog: %w", err)
	}
	mu.Lock()
	current = cat
	mu.Unlock()
	return nil
}

// Get returns the currently loaded catalog, or nil when none loaded yet.
func Get() *Catalog {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// LookupModel finds a model entry by provider + model id: exact id first,
// then the normalized id (lowercase, trailing -latest / -YYYYMMDD stripped —
// vendor ids commonly carry them, design §5.10.3 型号匹配归一化).
func LookupModel(providerName, modelID string) (CatalogModel, bool) {
	cat := Get()
	if cat == nil || providerName == "" || modelID == "" {
		return CatalogModel{}, false
	}
	prov, ok := cat.Providers[strings.ToLower(providerName)]
	if !ok {
		return CatalogModel{}, false
	}
	if m, ok := prov.Models[modelID]; ok {
		return m, true
	}
	if norm := normalizeModelID(modelID); norm != "" && norm != modelID {
		m, ok := prov.Models[norm]
		return m, ok
	}
	return CatalogModel{}, false
}

// normalizeModelID lowercases and strips trailing -latest / -YYYYMMDD
// qualifiers: "qwen3-max-2025-07-20" → "qwen3-max", "gpt-5-mini-latest" →
// "gpt-5-mini".
func normalizeModelID(id string) string {
	s := strings.ToLower(strings.TrimSpace(id))
	if idx := strings.Index(s, "-latest"); idx > 0 {
		s = s[:idx]
	}
	// Trailing date qualifiers: -YYYY-MM-DD (dashed, 11 chars incl. the
	// leading dash) or -YYYYMMDD (compact, 9 chars incl. the dash).
	if n := len(s); n > 11 && s[n-11] == '-' && s[n-6] == '-' && s[n-3] == '-' &&
		allDigits(s[n-10:n-6]) && allDigits(s[n-5:n-3]) && allDigits(s[n-2:]) {
		s = s[:n-11]
	} else if idx := strings.LastIndex(s, "-"); idx > 0 && len(s)-idx-1 == 8 && allDigits(s[idx+1:]) {
		s = s[:idx]
	}
	return s
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func fetch(ctx context.Context, source Source) ([]byte, error) {
	switch source {
	case SourceLocal:
		return embeddedCatalog, nil
	case SourceGitHub, SourceCNB:
		url := mirrorURLs[source]
		if url == "" {
			fallbackOnce.Do(func() {
				log.Printf("[catalog] source %s has no mirror URL configured; falling back to LOCAL", source)
			})
			return embeddedCatalog, nil
		}
		return fetchRemote(ctx, url)
	default:
		return nil, fmt.Errorf("unknown catalog source %q", source)
	}
}

func fetchRemote(ctx context.Context, url string) ([]byte, error) {
	// The URL is a compile-time constant from mirrorURLs (never user input —
	// the enum whitelist is the trust boundary), so a plain client with the
	// fetch timeout suffices; SSRF controls govern user-supplied URLs.
	client := &http.Client{Timeout: fetchTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if len(data) > maxCatalogBytes {
		return nil, fmt.Errorf("catalog from %s exceeds %d bytes", url, maxCatalogBytes)
	}
	return data, nil
}

func parse(data []byte) (*Catalog, error) {
	var cat Catalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return nil, err
	}
	if cat.Version == "" || len(cat.Providers) == 0 {
		return nil, fmt.Errorf("catalog missing version or providers")
	}
	return &cat, nil
}

// envSource reads WEKNORA_MODELS_CATALOG; invalid values warn and fall back
// to the default (design §5.10.4).
func envSource() Source {
	raw := strings.TrimSpace(os.Getenv("WEKNORA_MODELS_CATALOG"))
	if raw == "" {
		return DefaultSource
	}
	switch Source(strings.ToUpper(raw)) {
	case SourceGitHub:
		return SourceGitHub
	case SourceCNB:
		return SourceCNB
	case SourceLocal:
		return SourceLocal
	default:
		log.Printf("[catalog] invalid WEKNORA_MODELS_CATALOG %q; falling back to %s", raw, DefaultSource)
		return DefaultSource
	}
}

// Init loads the catalog from the configured source. Called during startup
// (async — callers wrap in a goroutine); failures are logged, never fatal.
func Init(ctx context.Context) {
	source := envSource()
	if err := Load(ctx, source); err != nil {
		log.Printf("[catalog] load from %s failed: %v (prefill falls back to manual entry)", source, err)
		return
	}
	log.Printf("[catalog] loaded (source=%s, version=%s)", source, Get().Version)
}
