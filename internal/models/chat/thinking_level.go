package chat

import (
	"github.com/Tencent/WeKnora/internal/models/provider"
)

// ResolveThinkingLevel folds the thinking-level priority chain (design §4.2):
//
//  1. callLevel — already resolved upstream to session-request > agent >
//     global-config defaults (service layer folds those three before the
//     wire layer sees them); empty means no call-level opinion.
//  2. modelLevel — the stored default on the model record (ADR 0001: user
//     value, final once saved); empty means the user never picked one.
//  3. providerDefault — ThinkingCaps.DefaultLevel from the adapter
//     declaration; empty means "emit no level parameter" and let the
//     upstream provider use its own default (ADR 0002).
//
// Validation: the winner must be within the model's SelectedLevels (when the
// user/catalog constrained the set) and within the provider's SupportedLevels
// (platform governance). A level outside the bounds falls back to the next
// tier, ultimately to the provider DefaultLevel — this is the "session
// switched models, stored level not supported" fallback from design §4.2.
// Returns "" when no tier yields a usable level.
func ResolveThinkingLevel(callLevel, modelLevel string, selectedLevels []string, caps provider.ThinkingCaps) string {
	containsLevel := func(levels []string, want string) bool {
		for _, l := range levels {
			if l == want {
				return true
			}
		}
		return false
	}
	containsProviderLevel := func(levels []provider.Level, want string) bool {
		for _, l := range levels {
			if string(l) == want {
				return true
			}
		}
		return false
	}

	// A candidate passes when it respects the model-level selection set (if
	// any) and the provider's supported enumeration (if any).
	usable := func(candidate string) bool {
		if candidate == "" {
			return false
		}
		if !caps.Supported {
			return false
		}
		if len(selectedLevels) > 0 && !containsLevel(selectedLevels, candidate) {
			return false
		}
		if len(caps.SupportedLevels) > 0 && !containsProviderLevel(caps.SupportedLevels, candidate) {
			return false
		}
		return true
	}

	for _, candidate := range []string{callLevel, modelLevel, string(caps.DefaultLevel)} {
		if usable(candidate) {
			return candidate
		}
	}
	return ""
}
