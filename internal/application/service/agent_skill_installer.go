package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ConfigureChatSkillInstaller is called during container construction, before
// serving requests. Post-construction wiring breaks the intentional cycle:
// the image installer itself creates a restricted agent engine.
func ConfigureChatSkillInstaller(agents interfaces.AgentService, installer *TenantSkillService) {
	if s, ok := agents.(*agentService); ok {
		s.skillInstaller = installer
	}
}

func (s *agentService) canOfferSkillInstaller(ctx context.Context, config *types.AgentConfig) bool {
	return s.skillInstaller != nil && config != nil && config.SkillsEnabled &&
		!config.SkillInstallMode() && tools.CanInstallSkills(ctx)
}
