package localsandbox

import (
	"context"
	"fmt"
)

// DefaultMode is the mode a session uses until it switches.
func (s *Service) DefaultMode(ctx context.Context, sessionID string) ApprovalMode {
	if s.modes == nil {
		return ModeAsk
	}
	return s.modes.ModeForSession(ctx, sessionID)
}

// ModeForSession is the session's own choice, or the default. The choice
// lives in memory: a restart, a fork (new session ID) or a delete returns the
// session to the default.
func (s *Service) ModeForSession(ctx context.Context, sessionID string) ApprovalMode {
	if mode, ok := s.sessions.mode(sessionID); ok {
		return mode
	}
	return s.DefaultMode(ctx, sessionID)
}

// SetSessionMode switches one session. Only shipped modes are accepted.
func (s *Service) SetSessionMode(sessionID string, mode ApprovalMode) error {
	if !mode.Shipped() {
		return fmt.Errorf("%w: %q", ErrApprovalModeNotShipped, mode)
	}
	s.sessions.setMode(sessionID, mode)
	return nil
}
