package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
)

// childSource is set only after AgentQA has resolved the actual model. It is
// never accepted from an API or a model-supplied tool argument.
type (
	childSourceKey struct{}
	childSource    struct {
		Agent           *types.CustomAgent
		ModelID         string
		SandboxConfigID string
	}
)

// inheritedAgentConfig supplements AgentConfig's JSON with the authorized
// runtime scope. Do not serialize credentials, skill environment values,
// attachment paths or session-owned sandbox state into a child snapshot.
type inheritedAgentConfig struct {
	ToolNames                     []string            `json:"tool_names"`
	Config                        types.AgentConfig   `json:"config"`
	ModelID                       string              `json:"model_id"`
	SearchTargets                 types.SearchTargets `json:"search_targets"`
	WritableKBIDs                 []string            `json:"writable_kb_ids"`
	SandboxConfigID               string              `json:"sandbox_config_id"`
	SkillNames                    []string            `json:"skill_names"`
	OperationsAgentID             string              `json:"operations_agent_id"`
	AssistantName                 string              `json:"assistant_name"`
	ProductDocs                   bool                `json:"product_docs"`
	LocalBrowserEnabled           bool                `json:"local_browser_enabled"`
	AttachmentParseWaitTimeoutSec int                 `json:"attachment_parse_wait_timeout_sec"`
	PinnedMCPServiceIDs           []string            `json:"pinned_mcp_service_ids"`
	PinnedSkillNames              []string            `json:"pinned_skill_names"`
}

func (c delegatedConfig) modelID() string {
	if c.Inherited != nil {
		return c.Inherited.ModelID
	}
	return ""
}

func inheritedChildSnapshot(ctx context.Context, cfg *types.AgentConfig, toolNames []string) (string, error) {
	source, ok := ctx.Value(childSourceKey{}).(childSource)
	tenant, _ := types.TenantIDFromContext(ctx)
	if !ok || source.Agent == nil || source.ModelID == "" || source.Agent.TenantID != tenant ||
		!source.Agent.IsAgentMode() || cfg.SharedAgentReadOnly || cfg.SkillInstallMode() {
		return "", errors.New("current execution cannot create an inherited worker")
	}
	inherited := &inheritedAgentConfig{
		Config: *cfg, ModelID: source.ModelID, ToolNames: toolNames, SearchTargets: cfg.SearchTargets,
		WritableKBIDs: cfg.WritableKBIDs, SandboxConfigID: cfg.SandboxConfigID,
		OperationsAgentID: cfg.OperationsAgentID, AssistantName: cfg.AssistantName,
		ProductDocs: cfg.ProductDocs, LocalBrowserEnabled: cfg.LocalBrowserEnabled,
		AttachmentParseWaitTimeoutSec: cfg.AttachmentParseWaitTimeoutSec,
		PinnedMCPServiceIDs:           cfg.PinnedMCPServiceIDs, PinnedSkillNames: cfg.PinnedSkillNames,
	}
	if source.SandboxConfigID != "" {
		inherited.SandboxConfigID = source.SandboxConfigID
	}
	for _, skill := range cfg.TenantSkills {
		if skill != nil {
			inherited.SkillNames = append(inherited.SkillNames, skill.Name)
		}
	}
	// Marshal immediately: later changes to parent config cannot change queued work.
	saved := delegatedConfig{
		Agent: *source.Agent, Access: cfg.AssistantAccess, AssistantParent: cfg.OperationsAgentID != "",
		Inherited: inherited,
	}
	saved.captureExecution(ctx)
	data, err := json.Marshal(saved)
	return string(data), err
}

// delegatedConfigFromBinding is the pinned configuration of the delegated
// child running on ctx, or nil outside one.
func delegatedConfigFromBinding(ctx context.Context) (*delegatedConfig, error) {
	b := collaboration.FromContext(ctx)
	if b == nil || b.Child == nil {
		return nil, nil
	}
	var saved delegatedConfig
	if err := json.Unmarshal([]byte(b.Child.Member.ConfigJSON), &saved); err != nil {
		return nil, err
	}
	return &saved, nil
}

func inheritedConfigFromBinding(ctx context.Context) (*delegatedConfig, error) {
	saved, err := delegatedConfigFromBinding(ctx)
	if err != nil || saved == nil || saved.Inherited == nil {
		return nil, err
	}
	b := collaboration.FromContext(ctx)
	if saved.Agent.TenantID != b.Scope.TenantID || saved.Inherited.ModelID == "" {
		return nil, errors.New("invalid inherited execution scope")
	}
	return saved, nil
}

// restoreInheritedConfig keeps the parent's resolved scope, including document
// and tag restrictions. Rebuilding from the original template would lose turn
// overrides; rebuilding from KB IDs alone would widen document-scoped access.
func (s *sessionService) restoreInheritedConfig(
	ctx context.Context, saved *delegatedConfig, tenant *types.Tenant,
) (*types.AgentConfig, error) {
	snapshot := saved.Inherited
	cfg := snapshot.Config
	cfg.SearchTargets = snapshot.SearchTargets
	cfg.SandboxConfigID = snapshot.SandboxConfigID
	cfg.OperationsAgentID = snapshot.OperationsAgentID
	cfg.AssistantName = snapshot.AssistantName
	cfg.AssistantAccess = saved.level(tenant)
	cfg.ProductDocs = snapshot.ProductDocs
	cfg.LocalBrowserEnabled = snapshot.LocalBrowserEnabled
	cfg.AttachmentParseWaitTimeoutSec = snapshot.AttachmentParseWaitTimeoutSec
	cfg.PinnedMCPServiceIDs = snapshot.PinnedMCPServiceIDs
	cfg.PinnedSkillNames = snapshot.PinnedSkillNames
	// Independent conversations keep their own follow-up history even if the
	// source template disables history for ordinary end-user conversations.
	cfg.MultiTurnEnabled = true
	permissions := kbReadPermissions(ctx, s.kbShareService)
	for _, target := range cfg.SearchTargets {
		if target == nil {
			return nil, errors.New("invalid inherited search target")
		}
		kbs, err := s.knowledgeBaseService.GetKnowledgeBasesByIDsOnly(ctx, []string{target.KnowledgeBaseID})
		if err != nil {
			return nil, err
		}
		if len(kbs) != 1 || kbs[0] == nil || kbs[0].TenantID != target.TenantID {
			return nil, fmt.Errorf("inherited knowledge base %s is no longer available", target.KnowledgeBaseID)
		}
		allowed, err := permissions.Check(target.KnowledgeBaseID, target.TenantID, types.OrgRoleViewer)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, errors.New("inherited knowledge scope is no longer authorized")
		}
	}
	roleEnforced := s.cfg != nil && s.cfg.Tenant.IsRBACEnforced()
	writable := kbWritableIDs(ctx, s.kbShareService, cfg.SearchTargets, roleEnforced)
	for _, id := range snapshot.WritableKBIDs {
		if slices.Contains(writable, id) {
			cfg.WritableKBIDs = append(cfg.WritableKBIDs, id)
		}
	}
	if cfg.AssistantAccess == types.AssistantAccessReadOnly {
		cfg.WritableKBIDs = nil
	}
	// Resolve credentials and enabled skill bodies afresh, then intersect with
	// the pinned names. New installations never expand a running child's scope.
	var skills []*types.TenantSkillEntity
	if cfg.SkillsEnabled && len(snapshot.SkillNames) > 0 {
		if s.hostDesktop {
			_, skills = hostSkillsForRun(ctx, s.tenantSkillRepo, s.hostSkillTree, tenant.ID)
		} else {
			sessionID, _ := types.SessionIDFromContext(ctx)
			_, skills = skillsForRun(ctx, s.sandboxPinner, s.sandboxConfigRepo, s.tenantSkillRepo,
				tenant.ID, sessionID, cfg.SandboxConfigID)
		}
	}
	for _, skill := range skills {
		if skill != nil && slices.Contains(snapshot.SkillNames, skill.Name) {
			cfg.TenantSkills = append(cfg.TenantSkills, skill)
		}
	}
	return &cfg, nil
}

// Reads and tools with their own argument-aware approval path must not acquire
// a second generic approval (which would consume the same resumed decision).
func inheritedToolOwnsPolicy(name string) bool {
	if tools.CanRunConcurrently(name) || strings.HasPrefix(name, "mcp_") {
		return true
	}
	switch name {
	case "send_message", "ask_user", tools.ToolCallMCPTool, tools.ToolDiscoverMCPTools,
		"find_operations", "run_operation", "open_page", "view_page", "read_page", "fill_page", "select_tab",
		tools.ToolSearchWeKnoraDocs, tools.ToolReadWeKnoraDoc:
		return true
	}
	return false
}
