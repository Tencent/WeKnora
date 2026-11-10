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
// false for backends that cannot snapshot at all (disabled), which is
// what "the base template is kept" means for them.
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
	// SkillOwnerFingerprint also returns "" for an empty SandboxType, and an
	// empty OwnerFingerprint forces the override to "": such a config can
	// never hold a usable image in the first place. That agreement is
	// accidental. Keep the fingerprint guard, or make this switch take the
	// global fallback too.
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
	case SandboxTypeDocker:
		return DockerSkillImageOverride(tenantCfg) != ""
	}
	return false
}

// SkillOwnerFingerprint is the provider-account identity stamped onto a
// skill snapshot. The install path refuses to record a snapshot when this is
// empty: a pointer with no owner is discarded at session start.
func SkillOwnerFingerprint(tenantCfg *types.TenantSandboxConfig) string {
	if tenantCfg == nil {
		return ""
	}
	switch SandboxType(tenantCfg.SandboxType) {
	case SandboxTypeCube:
		if tenantCfg.Cube != nil {
			return SkillImageFingerprint("cube", tenantCfg.Cube.APIKey, tenantCfg.Cube.APIURL)
		}
	case SandboxTypeE2B:
		if tenantCfg.E2B != nil {
			return SkillImageFingerprint("e2b", tenantCfg.E2B.APIKey, tenantCfg.E2B.APIURL)
		}
	case SandboxTypeDocker:
		return dockerSkillOwnerFingerprint(tenantCfg.Docker)
	}
	return ""
}

// dockerLocalDaemonIdentity stands in for a blank host in the fingerprint.
//
// It must not be the host DetectLocalDockerHost resolves to. That value comes
// from DOCKER_HOST or the current docker context, so it changes when an
// operator switches between Colima, Docker Desktop and OrbStack, or when the
// app is redeployed with a different environment. Feeding it into the
// fingerprint made all three of those look like a credential rotation: the
// session layer would drop the snapshot and boot the base template with no
// skills, installs would be refused, and PruneSupersededSnapshots would skip
// the config forever, so nothing ever reclaimed the images either.
//
// The account-rotation reasoning that fingerprint exists for does not carry
// over to a local daemon anyway. A rotated Cube/E2B key addresses a different
// account where our snapshot IDs genuinely do not exist; a re-detected local
// host is almost always the same daemon holding the same images on the same
// disk. An explicitly configured host is still part of the identity, because
// that one only changes when an admin edits the config.
const dockerLocalDaemonIdentity = "local-daemon"

// dockerSkillOwnerIdentity is the daemon identity a skill snapshot is stamped
// with. It reads the STORED config rather than a resolved one so that every
// caller — the install path, SkillImageActive and ResolveEffectiveConfig —
// computes the fingerprint from the same inputs.
func dockerSkillOwnerIdentity(docker *types.DockerSandboxConfig) (host, tls string) {
	if docker == nil {
		return "", ""
	}
	host = strings.TrimSpace(docker.Host)
	if host == "" {
		host = dockerLocalDaemonIdentity
	}
	return host, strings.TrimSpace(docker.TLSCertPath)
}

// DockerSkillImageOverride returns the committed skill image the docker backend
// should boot instead of the config's base image, or "" to keep the base.
func DockerSkillImageOverride(tenantCfg *types.TenantSandboxConfig) string {
	if tenantCfg == nil || tenantCfg.Docker == nil {
		return ""
	}
	host, tls := dockerSkillOwnerIdentity(tenantCfg.Docker)
	return skillImageTemplateOverride(tenantCfg.SkillImage, "docker", tls, host)
}

func dockerSkillOwnerFingerprint(docker *types.DockerSandboxConfig) string {
	if docker == nil {
		return ""
	}
	host, tls := dockerSkillOwnerIdentity(docker)
	return SkillImageFingerprint("docker", tls, host)
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
