package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/service/credential"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// SetSecretHandles installs the handle store browser_fill_secret reads.
func (s *agentService) SetSecretHandles(h tools.SecretHandles) {
	s.secretHandles = h
}

// secureFormBrowser returns the session's secure browser when this run may
// offer the secure credential form: a signed-in user acting in person, on a
// remote sandbox whose desktop is on, for an agent entitled to the sandbox
// shell. Anything else gets nil.
func secureFormBrowser(
	ctx context.Context, mgr sandbox.Manager, config *types.AgentConfig,
) sandbox.SessionSecureBrowser {
	p, ok := types.PrincipalFromContext(ctx)
	if !ok || p.Type != types.PrincipalWebUser || p.ActingVia != "" {
		return nil
	}
	if mgr == nil || config == nil || !config.SkillsEnabled || mgr.GetType() == sandbox.SandboxTypeHost {
		return nil
	}
	reporter, ok := mgr.(sandbox.SessionDesktopPromptReporter)
	if !ok || !reporter.DesktopEnabledForPrompt() {
		return nil
	}
	provider, ok := mgr.(sandbox.SessionSecureBrowserProvider)
	if !ok {
		return nil
	}
	return provider.SessionSecureBrowser()
}

func (s *agentService) registerSecureFormTools(
	ctx context.Context, registry *tools.ToolRegistry, sessionID string, config *types.AgentConfig,
) {
	if s.secretHandles == nil || !credential.Available() {
		return
	}
	asker, ok := s.toolApprovalGate.(approval.SecureFormAsker)
	if !ok || asker == nil {
		return
	}
	mgr, err := s.resolveWorkspaceSandbox(ctx, sessionID, config)
	if err != nil {
		logger.Warnf(ctx, "Failed to resolve sandbox for the secure credential form: %v", err)
		return
	}
	browser := secureFormBrowser(ctx, mgr, config)
	if browser == nil {
		return
	}
	registry.RegisterTool(tools.NewRequestSecureFormTool(asker, browser))
	registry.RegisterTool(tools.NewBrowserFillSecretTool(s.secretHandles, browser))
	logger.Infof(ctx, "Registered request_secure_form and browser_fill_secret")
}
