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

// SkillImageActive reports whether a config's stored snapshot is the image its
// sessions actually boot.
//
// It exists so the agent side can ask the same question the session side
// already answers, from the same inputs: a skill announced to the model while
// sessions keep booting the base template is a skill that cannot be invoked,
// and one hidden while the image carries it is an install nobody can use. It is
// false for backends that cannot snapshot at all, which is what "the base
// template is kept" means for them.
func SkillImageActive(tenantCfg *types.TenantSandboxConfig) bool {
	if tenantCfg == nil {
		return false
	}
	// The credentials are read from the stored provider block because
	// ResolveEffectiveConfig clears the baseline's before applying it, so
	// these are the ones the override is computed from there too.
	//
	// This switch is on the stored SandboxType alone, while
	// ResolveEffectiveConfig falls back to the GLOBAL type when that field is
	// empty - so in principle a config with no stored type could boot as e2b
	// here and be judged "no image" there. The two agree today only because
	// the install flow's skillOwnerFingerprint also returns "" for an empty
	// SandboxType, and an empty OwnerFingerprint forces the override to "":
	// such a config can never hold a usable image in the first place. That
	// agreement is accidental. Keep the fingerprint guard, or make this switch
	// take the global fallback too.
	switch SandboxType(tenantCfg.SandboxType) {
	case SandboxTypeCube:
		if tenantCfg.Cube == nil {
			return false
		}
		return skillImageTemplateOverride(
			tenantCfg.SkillImage, "cube", tenantCfg.Cube.APIKey, tenantCfg.Cube.APIURL,
		) != ""
	case SandboxTypeE2B:
		if tenantCfg.E2B == nil {
			return false
		}
		return skillImageTemplateOverride(
			tenantCfg.SkillImage, "e2b", tenantCfg.E2B.APIKey, tenantCfg.E2B.APIURL,
		) != ""
	}
	return false
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
