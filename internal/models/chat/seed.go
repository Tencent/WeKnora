package chat

import (
	"errors"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
)

// ErrChatSeedUnsupported reports an explicit seed sent to a provider that has
// no seed parameter. Callers must surface it instead of silently dropping
// the seed.
var ErrChatSeedUnsupported = errors.New("chat provider does not support a random seed")

// Seed support states mirror the experiment snapshot contract.
const (
	ChatSeedSupportApplied      = "applied"
	ChatSeedSupportUnsupported  = "unsupported"
	ChatSeedSupportNotRequested = "not_requested"
)

// SeedSupportState classifies whether a chat backend actually accepts a
// seed in its request: OpenAI-compatible providers and Ollama forward it;
// Anthropic's Messages API has no seed parameter.
func SeedSupportState(providerName string, baseURL string, source types.ModelSource) string {
	if source == types.ModelSourceLocal {
		return ChatSeedSupportApplied
	}
	name := provider.ProviderName(providerName)
	if name == "" {
		name = provider.DetectProvider(baseURL)
	}
	if name == provider.ProviderAnthropic {
		return ChatSeedSupportUnsupported
	}
	return ChatSeedSupportApplied
}

// OptionsSeedProvided reports whether the options carry an explicit seed:
// either the pointer-derived flag (evaluation requests distinguish seed=0)
// or a non-zero configured seed (interactive sessions).
func OptionsSeedProvided(opts *ChatOptions) bool {
	return opts != nil && (opts.SeedProvided || opts.Seed != 0)
}
