package types

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// YAML data structures for config/builtin_agents.yaml
// ---------------------------------------------------------------------------

// BuiltinAgentI18n holds localised name and description for a single locale.
type BuiltinAgentI18n struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// BuiltinAgentEntry is one entry in the builtin_agents list in YAML.
type BuiltinAgentEntry struct {
	ID        string                       `yaml:"id"`
	Avatar    string                       `yaml:"avatar"`
	IsBuiltin bool                         `yaml:"is_builtin"`
	I18n      map[string]BuiltinAgentI18n  `yaml:"i18n"`
	Config    CustomAgentConfig            `yaml:"config"`
}

// builtinAgentsFile is the top-level YAML structure.
type builtinAgentsFile struct {
	BuiltinAgents []BuiltinAgentEntry `yaml:"builtin_agents"`
}

// ---------------------------------------------------------------------------
// Global registry (populated from YAML at startup)
// ---------------------------------------------------------------------------

var (
	builtinAgentEntries     map[string]*BuiltinAgentEntry // keyed by agent ID
	builtinAgentEntriesMu   sync.RWMutex
	builtinAgentEntriesOnce sync.Once
)

// LoadBuiltinAgentsConfig loads built-in agent definitions from the given
// config directory (e.g. "./config"). The file must be named "builtin_agents.yaml".
// This should be called once at startup, after config.LoadConfig determines
// the config directory.
//
// If the file does not exist, the function is a no-op and the hard-coded
// defaults in BuiltinAgentRegistry remain effective.
func LoadBuiltinAgentsConfig(configDir string) error {
	var loadErr error
	builtinAgentEntriesOnce.Do(func() {
		filePath := filepath.Join(configDir, "builtin_agents.yaml")
		data, err := os.ReadFile(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				// File not found – perfectly fine, keep using hard-coded defaults.
				return
			}
			loadErr = fmt.Errorf("read builtin_agents.yaml: %w", err)
			return
		}

		var file builtinAgentsFile
		if err := yaml.Unmarshal(data, &file); err != nil {
			loadErr = fmt.Errorf("parse builtin_agents.yaml: %w", err)
			return
		}

		builtinAgentEntriesMu.Lock()
		defer builtinAgentEntriesMu.Unlock()

		builtinAgentEntries = make(map[string]*BuiltinAgentEntry, len(file.BuiltinAgents))
		for i := range file.BuiltinAgents {
			entry := &file.BuiltinAgents[i]
			builtinAgentEntries[entry.ID] = entry
		}

		// Rebuild the BuiltinAgentRegistry so that IsBuiltinAgentID / GetBuiltinAgent
		// continue to work transparently.
		rebuildRegistryFromConfig()
	})
	return loadErr
}

// rebuildRegistryFromConfig replaces the BuiltinAgentRegistry entries with
// factory functions that read from the YAML-loaded config. Must be called
// while builtinAgentEntriesMu is held.
func rebuildRegistryFromConfig() {
	for id := range builtinAgentEntries {
		agentID := id // capture for closure
		BuiltinAgentRegistry[agentID] = func(tenantID uint64) *CustomAgent {
			return buildAgentFromEntry(agentID, tenantID, "")
		}
	}
}

// ---------------------------------------------------------------------------
// Public API — context-aware, i18n-capable
// ---------------------------------------------------------------------------

// GetBuiltinAgentWithContext returns a built-in agent whose Name and
// Description are localised according to the language in ctx.
// Falls back to GetBuiltinAgent (default locale) when no YAML config is loaded.
func GetBuiltinAgentWithContext(ctx context.Context, id string, tenantID uint64) *CustomAgent {
	locale := localeFromCtx(ctx)

	builtinAgentEntriesMu.RLock()
	entry, ok := builtinAgentEntries[id]
	builtinAgentEntriesMu.RUnlock()

	if !ok || entry == nil {
		// No YAML entry — fall back to hard-coded factory.
		if factory, exists := BuiltinAgentRegistry[id]; exists {
			return factory(tenantID)
		}
		return nil
	}

	return buildAgentFromEntry(id, tenantID, locale)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// buildAgentFromEntry constructs a *CustomAgent from a BuiltinAgentEntry.
// locale can be "" to use the "default" locale.
func buildAgentFromEntry(id string, tenantID uint64, locale string) *CustomAgent {
	builtinAgentEntriesMu.RLock()
	entry, ok := builtinAgentEntries[id]
	builtinAgentEntriesMu.RUnlock()

	if !ok || entry == nil {
		return nil
	}

	i18n := resolveI18n(entry.I18n, locale)

	agent := &CustomAgent{
		ID:          entry.ID,
		Name:        i18n.Name,
		Description: i18n.Description,
		Avatar:      entry.Avatar,
		IsBuiltin:   entry.IsBuiltin,
		TenantID:    tenantID,
		Config:      entry.Config, // value copy
	}
	agent.EnsureDefaults()
	return agent
}

// resolveI18n picks the best locale match from the i18n map.
// Priority: exact match → language-only match → "default" → first entry.
func resolveI18n(m map[string]BuiltinAgentI18n, locale string) BuiltinAgentI18n {
	if len(m) == 0 {
		return BuiltinAgentI18n{}
	}

	// 1. Exact match  (e.g. "zh-CN")
	if v, ok := m[locale]; ok {
		return v
	}

	// 2. Language-only match (e.g. "zh-CN" → try "zh")
	if idx := strings.IndexAny(locale, "-_"); idx > 0 {
		lang := locale[:idx]
		if v, ok := m[lang]; ok {
			return v
		}
		// Also try matching entries that start with the same language prefix
		for k, v := range m {
			if strings.HasPrefix(k, lang) {
				return v
			}
		}
	}

	// 3. "default" key
	if v, ok := m["default"]; ok {
		return v
	}

	// 4. First available entry
	for _, v := range m {
		return v
	}
	return BuiltinAgentI18n{}
}

// localeFromCtx extracts the locale string from ctx, falling back to "".
func localeFromCtx(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	lang, _ := LanguageFromContext(ctx)
	return lang
}

// ResolveBuiltinAgentPromptRefs iterates over all builtin agent entries and
// resolves system_prompt_id / context_template_id references by calling the
// provided resolver function.  The resolver takes a template ID and returns
// the template content string (empty string if not found).
//
// This must be called after both LoadBuiltinAgentsConfig and prompt template
// loading have completed.
//
// Resolution is gated by `Config.SystemPrompt == ""` (and likewise for
// ContextTemplate). If you want to re-resolve from a refreshed template
// source, call ResetBuiltinAgentResolvedPrompts first to clear the cached
// fields.
func ResolveBuiltinAgentPromptRefs(resolver func(id string) string) {
	builtinAgentEntriesMu.Lock()
	defer builtinAgentEntriesMu.Unlock()

	for _, entry := range builtinAgentEntries {
		if entry == nil {
			continue
		}
		// Resolve system_prompt_id → SystemPrompt
		if entry.Config.SystemPromptID != "" && entry.Config.SystemPrompt == "" {
			if content := resolver(entry.Config.SystemPromptID); content != "" {
				entry.Config.SystemPrompt = content
			} else {
				fmt.Printf("Warning: builtin agent %q references system_prompt_id %q but template not found\n",
					entry.ID, entry.Config.SystemPromptID)
			}
		}
		// Resolve context_template_id → ContextTemplate
		if entry.Config.ContextTemplateID != "" && entry.Config.ContextTemplate == "" {
			if content := resolver(entry.Config.ContextTemplateID); content != "" {
				entry.Config.ContextTemplate = content
			} else {
				fmt.Printf("Warning: builtin agent %q references context_template_id %q but template not found\n",
					entry.ID, entry.Config.ContextTemplateID)
			}
		}
	}
}

// ResetBuiltinAgentResolvedPrompts clears the cached SystemPrompt /
// ContextTemplate fields on every builtin agent that originally derived them
// from an *_id reference. Pair with ResolveBuiltinAgentPromptRefs after
// refreshing the prompt template source (e.g. after replacing
// cfg.PromptTemplates with a DB-sourced snapshot) so the next resolve picks
// up the new content.
//
// Safety: we only clear a field when its corresponding *_id is non-empty.
// This means agents whose YAML wrote SystemPrompt directly (e.g.
// builtin-smart-reasoning has system_prompt: "" and no system_prompt_id, or
// a future agent with literal text in YAML) are left untouched — they were
// never resolver-driven and shouldn't be wiped.
func ResetBuiltinAgentResolvedPrompts() {
	builtinAgentEntriesMu.Lock()
	defer builtinAgentEntriesMu.Unlock()

	for _, entry := range builtinAgentEntries {
		if entry == nil {
			continue
		}
		if entry.Config.SystemPromptID != "" {
			entry.Config.SystemPrompt = ""
		}
		if entry.Config.ContextTemplateID != "" {
			entry.Config.ContextTemplate = ""
		}
	}
}
