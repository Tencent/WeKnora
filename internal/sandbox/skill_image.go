package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// SkillImageFingerprint identifies the provider account a skill snapshot lives
// in. Snapshots are not visible across accounts, so when credentials change the
// stored snapshot silently stops existing for us - this fingerprint is how we
// notice and fall back instead of booting sessions against a dead image ID.
func SkillImageFingerprint(provider, apiKey, apiURL string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(provider),
		strings.TrimSpace(apiKey),
		strings.TrimSpace(apiURL),
	}, "\n")))
	return hex.EncodeToString(sum[:])
}

// skillImageTemplateOverride returns the snapshot ID that should replace the
// base template, or "" when the base template must be kept.
func skillImageTemplateOverride(
	image *types.SkillImageConfig, provider, apiKey, apiURL string,
) string {
	if image == nil || strings.TrimSpace(image.SnapshotID) == "" {
		return ""
	}
	if image.OwnerFingerprint == "" {
		return ""
	}
	if image.OwnerFingerprint != SkillImageFingerprint(provider, apiKey, apiURL) {
		return ""
	}
	return strings.TrimSpace(image.SnapshotID)
}
