package types

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// YAML data structures for config/agent_type_presets.yaml
// ---------------------------------------------------------------------------

// AgentTypePresetI18n holds localised label and description for a single locale.
type AgentTypePresetI18n struct {
	Label       string `yaml:"label"       json:"label"`
	Description string `yaml:"description" json:"description"`
}

// AgentTypeKBFilter describes how the agent editor should filter selectable KBs
// when this preset is active. Predicates are evaluated against the KB's
// capabilities (see KBCapabilities).
//
// If all three slices are empty, the preset imposes no KB restriction.
type AgentTypeKBFilter struct {
	// AnyOf: KB must expose at least ONE of these capability flags.
	AnyOf []string `yaml:"any_of"  json:"any_of,omitempty"`
	// AllOf: KB must expose ALL of these capability flags.
	AllOf []string `yaml:"all_of"  json:"all_of,omitempty"`
	// NoneOf: KB must expose NONE of these capability flags.
	NoneOf []string `yaml:"none_of" json:"none_of,omitempty"`
}

// AgentTypePresetEntry is one entry in the agent_type_presets list in YAML.
//
// Only fields relevant for auto-filling the editor form are included under
// `config`. Everything else (retrieval thresholds, etc.) falls back to the
// user-edited defaults / CustomAgent.EnsureDefaults.
type AgentTypePresetEntry struct {
	ID       string                         `yaml:"id"        json:"id"`
	I18n     map[string]AgentTypePresetI18n `yaml:"i18n"      json:"i18n"`
	Config   *AgentTypePresetConfig         `yaml:"config"    json:"config,omitempty"`
	KBFilter *AgentTypeKBFilter             `yaml:"kb_filter" json:"kb_filter,omitempty"`
}

// AgentTypePresetConfig is a restricted view of CustomAgentConfig used purely
// as the preset payload. Any field left at its zero value is NOT applied by
// the preset so the user's existing value stays untouched.
//
// We mirror json tags (not yaml tags) directly from CustomAgentConfig so the
// frontend can apply them via simple Object.assign.
type AgentTypePresetConfig struct {
	SystemPromptID         string   `yaml:"system_prompt_id"       json:"system_prompt_id,omitempty"`
	Temperature            float64  `yaml:"temperature"            json:"temperature,omitempty"`
	MaxIterations          int      `yaml:"max_iterations"         json:"max_iterations,omitempty"`
	AllowedTools           []string `yaml:"allowed_tools"          json:"allowed_tools,omitempty"`
	RetainRetrievalHistory bool     `yaml:"retain_retrieval_history" json:"retain_retrieval_history,omitempty"`
	FAQPriorityEnabled     bool     `yaml:"faq_priority_enabled"   json:"faq_priority_enabled,omitempty"`
	WebSearchEnabled       bool     `yaml:"web_search_enabled"     json:"web_search_enabled,omitempty"`
	SupportedFileTypes     []string `yaml:"supported_file_types"   json:"supported_file_types,omitempty"`
	// KBSelectionMode presets the KB picker mode: "all" | "selected" | "none".
	// When empty, the user's current value stays untouched.
	KBSelectionMode string `yaml:"kb_selection_mode" json:"kb_selection_mode,omitempty"`
}

// agentTypePresetsFile is the top-level YAML structure.
type agentTypePresetsFile struct {
	Presets []AgentTypePresetEntry `yaml:"agent_type_presets"`
}

// ---------------------------------------------------------------------------
// Global registry (populated from YAML at startup)
// ---------------------------------------------------------------------------

var (
	agentTypePresets     map[string]*AgentTypePresetEntry // keyed by preset ID
	agentTypePresetsMu   sync.RWMutex
	agentTypePresetsOnce sync.Once
	agentTypePresetIDs   []string // insertion order, for stable UI listing
)

// LoadAgentTypePresetsConfig loads agent-type preset definitions from the given
// config directory (e.g. "./config"). The file must be named
// "agent_type_presets.yaml". Called once at startup (after LoadConfig determines
// the config directory).
//
// If the file does not exist, this is a no-op and the registry stays empty —
// in that case the frontend will simply not offer the agent-type dropdown.
func LoadAgentTypePresetsConfig(configDir string) error {
	var loadErr error
	agentTypePresetsOnce.Do(func() {
		filePath := filepath.Join(configDir, "agent_type_presets.yaml")
		data, err := os.ReadFile(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				return
			}
			loadErr = fmt.Errorf("read agent_type_presets.yaml: %w", err)
			return
		}

		var file agentTypePresetsFile
		if err := yaml.Unmarshal(data, &file); err != nil {
			loadErr = fmt.Errorf("parse agent_type_presets.yaml: %w", err)
			return
		}

		agentTypePresetsMu.Lock()
		defer agentTypePresetsMu.Unlock()

		agentTypePresets = make(map[string]*AgentTypePresetEntry, len(file.Presets))
		agentTypePresetIDs = make([]string, 0, len(file.Presets))
		for i := range file.Presets {
			entry := &file.Presets[i]
			if entry.ID == "" {
				continue
			}
			agentTypePresets[entry.ID] = entry
			agentTypePresetIDs = append(agentTypePresetIDs, entry.ID)
		}
	})
	return loadErr
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// ListAgentTypePresetsWithContext returns all registered agent-type presets in
// their YAML-declared order, with i18n resolved against the request locale.
//
// The returned slice is a fresh copy safe to mutate by the caller (e.g. to
// tweak the i18n label before encoding).
func ListAgentTypePresetsWithContext(ctx context.Context) []AgentTypePresetEntry {
	locale := localeFromCtx(ctx)

	agentTypePresetsMu.RLock()
	defer agentTypePresetsMu.RUnlock()

	out := make([]AgentTypePresetEntry, 0, len(agentTypePresetIDs))
	for _, id := range agentTypePresetIDs {
		entry := agentTypePresets[id]
		if entry == nil {
			continue
		}
		// Shallow copy and narrow i18n down to the caller's locale so the
		// API response is compact. We keep both "default" and the resolved
		// locale entry in case the frontend wants to fall back.
		cp := *entry
		if len(entry.I18n) > 0 {
			cp.I18n = resolveAgentTypeI18n(entry.I18n, locale)
		}
		out = append(out, cp)
	}
	return out
}

// GetAgentTypePreset returns the raw entry for the given preset ID, or nil.
// Used by prompt-resolution at startup.
func GetAgentTypePreset(id string) *AgentTypePresetEntry {
	agentTypePresetsMu.RLock()
	defer agentTypePresetsMu.RUnlock()
	return agentTypePresets[id]
}

// ResolveAgentTypePresetPromptRefs iterates over all presets and resolves
// system_prompt_id references into the actual prompt template content via the
// provided resolver. This lets downstream services treat the `system_prompt_id`
// field as a concrete prompt string if they need to.
//
// We keep system_prompt_id as-is in the preset (it's also what the editor
// writes back to CustomAgentConfig), so this hook is only used to validate
// that every referenced template actually exists at startup.
func ResolveAgentTypePresetPromptRefs(resolver func(id string) string) {
	agentTypePresetsMu.Lock()
	defer agentTypePresetsMu.Unlock()
	for _, entry := range agentTypePresets {
		if entry == nil || entry.Config == nil || entry.Config.SystemPromptID == "" {
			continue
		}
		if content := resolver(entry.Config.SystemPromptID); content == "" {
			fmt.Printf(
				"Warning: agent type preset %q references system_prompt_id %q but template not found\n",
				entry.ID, entry.Config.SystemPromptID,
			)
		}
	}
}

// resolveAgentTypeI18n picks a reasonable subset of i18n entries for the given
// locale: the exact-match entry (if any) plus the "default" fallback. Callers
// that want full i18n can pass an empty locale to get everything.
func resolveAgentTypeI18n(m map[string]AgentTypePresetI18n, locale string) map[string]AgentTypePresetI18n {
	out := make(map[string]AgentTypePresetI18n, 2)
	if v, ok := m["default"]; ok {
		out["default"] = v
	}
	if locale != "" {
		if v, ok := m[locale]; ok {
			out[locale] = v
		}
	}
	// If neither matched, surface the whole map so the frontend can decide.
	if len(out) == 0 {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}
