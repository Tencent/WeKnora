package service

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// Read the current caller's stored preference at engine creation, never the
// shared agent owner's preference or a browser/client-supplied user ID.
func (s *agentService) browserSearchInstructions(ctx context.Context) (string, error) {
	id, _ := types.UserIDFromContext(ctx)
	if s.userRepo == nil || id == "" {
		return types.DefaultBrowserSearchInstructions, nil
	}
	user, err := s.userRepo.GetUserByID(ctx, id)
	if err != nil {
		return "", fmt.Errorf("load browser search preferences: %w", err)
	}
	return user.Preferences.EffectiveBrowserSearchInstructions(), nil
}
