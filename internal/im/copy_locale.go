package im

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// localizeCopy returns the Traditional Chinese variant for zh-TW / zh-Hant
// channels and the upstream Simplified string for everything else.
//
// Command confirmations and system hints are generated inside the shared
// command layer, which has no per-channel template mechanism. The channel's
// reply language is already threaded through the IM identity context (the same
// value that drives the model's answer language), so the copy follows it
// instead of switching language mid-conversation for a zh-TW channel.
func localizeCopy(ctx context.Context, simplified, traditional string) string {
	lang, _ := types.LanguageFromContext(ctx)
	normalized := strings.ToLower(strings.ReplaceAll(lang, "_", "-"))
	if strings.HasPrefix(normalized, "zh-tw") || strings.HasPrefix(normalized, "zh-hant") {
		return traditional
	}
	return simplified
}
