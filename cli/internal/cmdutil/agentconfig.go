package cmdutil

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"

	sdk "github.com/Tencent/WeKnora/client"
)

// AgentConfigFlags carries hot-path flag values plus per-flag "was set"
// bits so the merge step can distinguish "user did not pass --foo" from
// "user passed --foo zero-value". Mirrors the canonical CLI pattern of
// keeping presence-tracking out-of-band from the value itself, which
// pflag's Changed() facility supplies at the cobra layer.
type AgentConfigFlags struct {
	AgentMode          string
	AgentModeSet       bool
	SystemPrompt       string
	SystemPromptSet    bool
	ModelID            string
	ModelIDSet         bool
	RerankModelID      string
	RerankModelIDSet   bool
	Temperature        float64
	TemperatureSet     bool
	KBSelectionMode    string
	KBSelectionModeSet bool
	KnowledgeBases     []string
	KnowledgeBasesSet  bool
}

// LoadAgentConfig parses YAML or JSON from r into an AgentConfig. kind is
// "yaml" or "json" (typically inferred from file extension by the caller).
// Returned errors carry no typed code — callers should wrap with
// CodeInputInvalidArgument as the user-facing context.
//
// YAML route round-trips through JSON so the SDK's existing `json:` field
// tags (snake_case) are the single source of truth for key names. yaml.v3
// alone would lowercase field names (KnowledgeBases → knowledgebases),
// which doesn't match the JSON schema users see elsewhere.
func LoadAgentConfig(r io.Reader, kind string) (*sdk.AgentConfig, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg sdk.AgentConfig
	switch strings.ToLower(kind) {
	case "yaml", "yml":
		var raw map[string]any
		if err := yaml.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("parse YAML config: %w", err)
		}
		jsBody, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("re-encode YAML as JSON: %w", err)
		}
		if err := json.Unmarshal(jsBody, &cfg); err != nil {
			return nil, fmt.Errorf("parse YAML config (via JSON): %w", err)
		}
	case "json":
		if err := json.Unmarshal(body, &cfg); err != nil {
			return nil, fmt.Errorf("parse JSON config: %w", err)
		}
	default:
		return nil, fmt.Errorf("unknown config format %q (want yaml or json)", kind)
	}
	return &cfg, nil
}

// MergeAgentConfig returns base with hot-path flag overrides applied. Only
// fields whose corresponding *Set bit is true are overridden; the rest of
// base passes through unchanged. The returned pointer is a fresh copy so
// callers can mutate it without aliasing base.
func MergeAgentConfig(base *sdk.AgentConfig, ov AgentConfigFlags) *sdk.AgentConfig {
	out := *base // shallow copy
	if ov.AgentModeSet {
		out.AgentMode = ov.AgentMode
	}
	if ov.SystemPromptSet {
		out.SystemPrompt = ov.SystemPrompt
	}
	if ov.ModelIDSet {
		out.ModelID = ov.ModelID
	}
	if ov.RerankModelIDSet {
		out.RerankModelID = ov.RerankModelID
	}
	if ov.TemperatureSet {
		out.Temperature = ov.Temperature
	}
	if ov.KBSelectionModeSet {
		out.KBSelectionMode = ov.KBSelectionMode
	}
	if ov.KnowledgeBasesSet {
		out.KnowledgeBases = ov.KnowledgeBases
	}
	return &out
}

// skeletonCommentVersion is embedded in the skeleton header so users
// regenerating an outdated template after a schema bump can spot the
// version mismatch.
const skeletonCommentVersion = "v0.5 / 34 fields"

// GenerateAgentSkeleton writes a commented YAML template with every
// AgentConfig field at its zero value to w. Used by
// `agent create --generate-skeleton` so users get a ready-to-edit
// starting point without authoring the 34-field schema from memory.
func GenerateAgentSkeleton(w io.Writer) error {
	const skeleton = `# WeKnora AgentConfig skeleton (` + skeletonCommentVersion + `)
# Edit this file and pass it to:
#   weknora agent create "My Agent" --model <id> --config-file <this-file>
# Hot-path flags on the create command override values set here.

# Operating mode: "quick-answer" or "smart-reasoning"
agent_mode: ""

# System prompt for the agent (also settable via --system-prompt[-file])
system_prompt: ""

# Optional template applied to retrieved context before model input
context_template: ""

# REQUIRED: LLM model id (server-side managed); also settable via --model
model_id: ""

# Optional rerank model id (server-side managed); also settable via --rerank-model
rerank_model_id: ""

# Generation tuning
temperature: 0.0
max_completion_tokens: 0
max_iterations: 0

# Tools / MCP integration
allowed_tools: []
mcp_selection_mode: ""    # "all" / "selected" / "none"
mcp_services: []

# Knowledge base attachment
kb_selection_mode: ""     # "all" / "selected" / "none"; also settable via --kb-selection-mode
knowledge_bases: []       # KB ids; also settable via repeated --kb
supported_file_types: []

# FAQ
faq_priority_enabled: false
faq_direct_answer_threshold: 0.0
faq_score_boost: 0.0

# Web search
web_search_enabled: false
web_search_max_results: 0

# Multi-turn
multi_turn_enabled: false
history_turns: 0

# Retrieval thresholds
embedding_top_k: 0
keyword_threshold: 0.0
vector_threshold: 0.0
rerank_top_k: 0
rerank_threshold: 0.0

# Query understanding / rewrite
enable_query_expansion: false
enable_rewrite: false
rewrite_prompt_system: ""
rewrite_prompt_user: ""
query_understand_model_id: ""

# Fallback when retrieval / generation fails
fallback_strategy: ""     # "fixed" or "model"
fallback_response: ""
fallback_prompt: ""
`
	_, err := io.WriteString(w, skeleton)
	return err
}
