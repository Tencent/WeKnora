package types

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode"
)

// MemoryExperienceRuneBudget limits the experience index injected into a turn.
const MemoryExperienceRuneBudget = 1800

// MemoryEvidence identifies an observation, not an assistant's claim of success.
type MemoryEvidence struct {
	SessionID  string `json:"session_id"`
	MessageID  string `json:"message_id"`
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name"`
	Success    bool   `json:"success"`
}

// MemoryExperience is the applicability and provenance of an operational lesson.
// Content on MemoryItem contains the short recommended action.
type MemoryExperience struct {
	Trigger       string           `json:"trigger"`
	Applicability string           `json:"applicability"`
	Avoid         string           `json:"avoid,omitempty"`
	Outcome       string           `json:"outcome"`
	AgentID       string           `json:"agent_id"`
	AgentTenantID uint64           `json:"agent_tenant_id"`
	TaskSummary   string           `json:"task_summary,omitempty"`
	Evidence      []MemoryEvidence `json:"evidence"`
}

// MemoryAgentScopeContextKey carries the server-resolved agent scope.
const MemoryAgentScopeContextKey ContextKey = "MemoryAgentScope"

type memoryAgentScope struct {
	ID       string
	TenantID uint64
}

// WithMemoryAgentScope binds recall to the server-resolved agent. The model
// cannot change this scope through file-tool arguments.
func WithMemoryAgentScope(ctx context.Context, id string, tenantID uint64) context.Context {
	return context.WithValue(ctx, MemoryAgentScopeContextKey, memoryAgentScope{id, tenantID})
}

// MemoryExperienceAllowed checks whether an experience belongs to the active agent.
func MemoryExperienceAllowed(ctx context.Context, item *MemoryItem) bool {
	if item == nil {
		return false
	}
	if item.Kind != MemoryKindExperience {
		return true
	}
	if item.Experience == nil {
		return false
	}
	scope, ok := ctx.Value(MemoryAgentScopeContextKey).(memoryAgentScope)
	return ok && scope.ID != "" && scope.ID == item.Experience.AgentID &&
		scope.TenantID == item.Experience.AgentTenantID
}

// MemoryStorageKey keeps a tool lesson from replacing a personal fact with the
// same topic, and keeps different agents' operational assumptions separate.
func MemoryStorageKey(item MemoryItem) string {
	key := MemoryItemKey(item.Topic, item.Content)
	if item.Kind != MemoryKindExperience {
		return key
	}
	if item.Experience == nil {
		return "experience:" + key
	}
	sum := sha256.Sum256(
		[]byte(
			fmt.Sprintf(
				"%s/%d/%s/%s",
				item.Experience.AgentID,
				item.Experience.AgentTenantID,
				key,
				NormalizeTopicKey(item.Experience.Applicability),
			),
		),
	)
	return fmt.Sprintf("experience:%x", sum[:])
}

// MemoryItemText is shared by prompt rendering, ranking and tool output, so
// applicability is neither lost nor omitted from the recall budget.
func MemoryItemText(item *MemoryItem) string {
	if item == nil {
		return ""
	}
	content := SanitizeMemoryItemContent(item.Kind, item.Content)
	if item.Kind != MemoryKindExperience || item.Experience == nil {
		return content
	}
	e := item.Experience
	parts := []string{
		"When: " + SanitizeMemoryContent(e.Trigger),
		"Scope: " + SanitizeMemoryContent(e.Applicability),
		"Observed outcome: " + e.Outcome,
		"Lesson: " + content,
	}
	if e.Avoid != "" {
		parts = append(parts, "Avoid: "+SanitizeMemoryContent(e.Avoid))
	}
	tools := make([]string, 0, len(e.Evidence))
	for _, evidence := range e.Evidence {
		if !containsMemoryTool(tools, evidence.ToolName) {
			tools = append(tools, evidence.ToolName)
		}
	}
	if len(tools) > 0 {
		parts = append(parts, "Tools: "+strings.Join(tools, ", "))
	}
	return strings.Join(parts, "; ")
}

func containsMemoryTool(tools []string, name string) bool {
	for _, tool := range tools {
		if tool == name {
			return true
		}
	}
	return false
}

// RenderMemoryExperiences renders experience index entries within the recall budget.
func RenderMemoryExperiences(items []*MemoryItem) string {
	return renderMemoryLines(items, MemoryExperienceRuneBudget)
}

// SanitizeMemoryItemContent preserves ordered experience steps and verification
// while ordinary profile notes keep their existing single-line budget.
func SanitizeMemoryItemContent(kind, content string) string {
	if kind != MemoryKindExperience {
		return SanitizeMemoryContent(content)
	}
	content = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, content)
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if len(runes) > 8000 {
		content = string(runes[:8000])
	}
	return content
}

// MemoryExperienceIndexText renders a compact pointer to a full procedure.
func MemoryExperienceIndexText(item *MemoryItem) string {
	if item == nil || item.Experience == nil {
		return ""
	}
	return fmt.Sprintf("%s — when %s; %s; outcome=%s; read(%q)",
		SanitizeMemoryTopic(item.Topic), SanitizeMemoryTopic(item.Experience.Trigger),
		SanitizeMemoryTopic(item.Experience.Applicability), item.Experience.Outcome,
		"memory://items/"+item.ID+".md")
}

// MemoryItemFingerprint hashes normalized content using the budget for its kind.
func MemoryItemFingerprint(kind, content string) string {
	if kind != MemoryKindExperience {
		return MemoryFingerprint(content)
	}
	var b strings.Builder
	for _, r := range strings.ToLower(SanitizeMemoryItemContent(kind, content)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return fmt.Sprintf("%x", sum[:])
}

// MemoryAgentScopeFromContext returns the server-resolved agent identity and tenant.
func MemoryAgentScopeFromContext(ctx context.Context) (string, uint64) {
	scope, _ := ctx.Value(MemoryAgentScopeContextKey).(memoryAgentScope)
	return scope.ID, scope.TenantID
}
