package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSandboxConfigValueEncryptsInjectedHeaderSecrets(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")

	cfg := &TenantSandboxConfig{
		SandboxType: "cube",
		Network: &SandboxNetworkPolicy{
			CubeRules: []CubeEgressRule{{
				Name:   "allow-api",
				Host:   "api.example.com",
				Inject: []CubeHeaderInject{{Header: "Authorization", Secret: "cube-secret"}},
			}},
			E2BHostRules: []E2BHostRule{{
				Host:    "api.example.com",
				Headers: map[string]string{"X-Key": "e2b-secret"},
			}},
		},
	}

	raw, err := cfg.Value()
	require.NoError(t, err)
	encoded := string(raw.([]byte))
	require.NotContains(t, encoded, "cube-secret")
	require.NotContains(t, encoded, "e2b-secret")
	// Header names stay readable, same rationale as EnvVars keys.
	require.Contains(t, encoded, "Authorization")
	require.Contains(t, encoded, "X-Key")

	var loaded TenantSandboxConfig
	require.NoError(t, loaded.Scan(raw.([]byte)))
	require.Equal(t, "cube-secret", loaded.Network.CubeRules[0].Inject[0].Secret)
	require.Equal(t, "e2b-secret", loaded.Network.E2BHostRules[0].Headers["X-Key"])
}

func TestSandboxConfigForResponseMasksSecrets(t *testing.T) {
	cfg := &TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &E2BSandboxConfig{APIKey: "e2b-secret", APIURL: "https://api.e2b.dev"},
		Cube:        &CubeSandboxConfig{APIKey: "cube-secret", APIURL: "http://cube"},
		EnvVars:     map[string]string{"HF_TOKEN": "hf-secret"},
	}

	out := SandboxConfigForResponse(cfg, true)

	require.Equal(t, RedactedSecretPlaceholder, out.E2B.APIKey)
	require.Equal(t, RedactedSecretPlaceholder, out.Cube.APIKey)
	require.Equal(t, RedactedSecretPlaceholder, out.EnvVars["HF_TOKEN"])
	// Non-secret fields stay visible.
	require.Equal(t, "https://api.e2b.dev", out.E2B.APIURL)
	require.Equal(t, "e2b", out.SandboxType)
	// Original must be untouched.
	require.Equal(t, "e2b-secret", cfg.E2B.APIKey)
	require.Equal(t, "hf-secret", cfg.EnvVars["HF_TOKEN"])
}

func TestSandboxConfigForResponseMasksInjectedHeaderSecrets(t *testing.T) {
	cfg := &TenantSandboxConfig{
		Network: &SandboxNetworkPolicy{
			CubeRules: []CubeEgressRule{{
				Name:   "allow-api",
				Host:   "api.example.com",
				Inject: []CubeHeaderInject{{Header: "Authorization", Secret: "cube-secret"}},
			}},
			E2BHostRules: []E2BHostRule{{
				Host:    "api.example.com",
				Headers: map[string]string{"X-Key": "e2b-secret", "X-Empty": ""},
			}},
		},
	}

	out := SandboxConfigForResponse(cfg, true)

	require.Equal(t, RedactedSecretPlaceholder, out.Network.CubeRules[0].Inject[0].Secret)
	require.Equal(t, RedactedSecretPlaceholder, out.Network.E2BHostRules[0].Headers["X-Key"])
	require.Empty(t, out.Network.E2BHostRules[0].Headers["X-Empty"],
		"an unset header value must not become a placeholder")
	require.Equal(t, "cube-secret", cfg.Network.CubeRules[0].Inject[0].Secret,
		"masking must not mutate the stored config")
}

func TestSandboxConfigForResponseSkipsMaskingWhenDisabled(t *testing.T) {
	cfg := &TenantSandboxConfig{E2B: &E2BSandboxConfig{APIKey: "e2b-secret"}}

	out := SandboxConfigForResponse(cfg, false)

	require.Equal(t, "e2b-secret", out.E2B.APIKey)
}

func TestSandboxConfigForResponseEmptySecretStaysEmpty(t *testing.T) {
	cfg := &TenantSandboxConfig{E2B: &E2BSandboxConfig{APIKey: ""}}

	out := SandboxConfigForResponse(cfg, true)

	require.Empty(t, out.E2B.APIKey, "an unset secret must not become a placeholder")
}

func TestSandboxConfigForResponseNil(t *testing.T) {
	require.Nil(t, SandboxConfigForResponse(nil, true))
}

func TestMergeSandboxConfigForUpdatePreservesRedactedSecrets(t *testing.T) {
	existing := &TenantSandboxConfig{
		E2B:     &E2BSandboxConfig{APIKey: "old-e2b"},
		Cube:    &CubeSandboxConfig{APIKey: "old-cube"},
		EnvVars: map[string]string{"HF_TOKEN": "old-hf", "GONE": "old-gone"},
	}
	incoming := &TenantSandboxConfig{
		E2B:     &E2BSandboxConfig{APIKey: RedactedSecretPlaceholder}, // untouched by user
		Cube:    &CubeSandboxConfig{APIKey: "new-cube"},               // user typed a new key
		EnvVars: map[string]string{"HF_TOKEN": RedactedSecretPlaceholder},
	}

	out := MergeSandboxConfigForUpdate(incoming, existing)

	require.Equal(t, "old-e2b", out.E2B.APIKey, "placeholder must resolve to the stored secret")
	require.Equal(t, "new-cube", out.Cube.APIKey, "an explicitly typed secret must win")
	require.Equal(t, "old-hf", out.EnvVars["HF_TOKEN"])
	require.NotContains(t, out.EnvVars, "GONE", "env vars removed by the user must not resurrect")
}

func TestMergeSandboxConfigForUpdatePreservesInjectedHeaderSecrets(t *testing.T) {
	existing := &TenantSandboxConfig{
		Network: &SandboxNetworkPolicy{
			CubeRules: []CubeEgressRule{{
				Name: "allow-api",
				Inject: []CubeHeaderInject{
					{Header: "Authorization", Secret: "old-auth"},
					{Header: "X-Trace", Secret: "old-trace"},
				},
			}},
			E2BHostRules: []E2BHostRule{{
				Host:    "api.example.com",
				Headers: map[string]string{"X-Key": "old-key", "X-Other": "old-other"},
			}},
		},
	}
	incoming := &TenantSandboxConfig{
		Network: &SandboxNetworkPolicy{
			CubeRules: []CubeEgressRule{{
				Name: "allow-api",
				Inject: []CubeHeaderInject{
					{Header: "Authorization", Secret: RedactedSecretPlaceholder}, // untouched
					{Header: "X-Trace", Secret: "new-trace"},                     // retyped
				},
			}},
			E2BHostRules: []E2BHostRule{{
				Host:    "api.example.com",
				Headers: map[string]string{"X-Key": RedactedSecretPlaceholder},
			}},
		},
	}

	out := MergeSandboxConfigForUpdate(incoming, existing)

	require.Equal(t, "old-auth", out.Network.CubeRules[0].Inject[0].Secret)
	require.Equal(t, "new-trace", out.Network.CubeRules[0].Inject[1].Secret)
	require.Equal(t, "old-key", out.Network.E2BHostRules[0].Headers["X-Key"])
	// A header the admin deleted must actually go, not be restored.
	require.NotContains(t, out.Network.E2BHostRules[0].Headers, "X-Other")
}

func TestMergeSandboxConfigForUpdateDropsNetworkWhenIncomingOmitsIt(t *testing.T) {
	existing := &TenantSandboxConfig{
		Network: &SandboxNetworkPolicy{DenyEgressByDefault: true},
	}

	out := MergeSandboxConfigForUpdate(&TenantSandboxConfig{}, existing)

	require.Nil(t, out.Network,
		"network is an editor-owned field: omitting it must clear it, unlike SkillImage")
}

func TestMergeSandboxConfigForUpdateHandlesNilExisting(t *testing.T) {
	incoming := &TenantSandboxConfig{E2B: &E2BSandboxConfig{APIKey: RedactedSecretPlaceholder}}

	out := MergeSandboxConfigForUpdate(incoming, nil)

	require.Empty(t, out.E2B.APIKey, "placeholder with no stored value resolves to empty")
}

func TestMergeSandboxConfigForUpdateNilIncoming(t *testing.T) {
	require.Nil(t, MergeSandboxConfigForUpdate(nil, &TenantSandboxConfig{}))
}

func TestMergeSandboxConfigForUpdatePreservesSkillImage(t *testing.T) {
	existing := &TenantSandboxConfig{
		E2B:        &E2BSandboxConfig{APIKey: "old-e2b"},
		SkillImage: &SkillImageConfig{SnapshotID: "snap-1", OwnerFingerprint: "fp-1", Generation: 3},
	}
	incoming := &TenantSandboxConfig{
		E2B:        &E2BSandboxConfig{APIKey: RedactedSecretPlaceholder},
		SkillImage: &SkillImageConfig{SnapshotID: "forged-snap", OwnerFingerprint: "forged-fp"},
	}

	out := MergeSandboxConfigForUpdate(incoming, existing)

	require.Equal(t, "snap-1", out.SkillImage.SnapshotID,
		"a settings save must not replace the install-owned snapshot pointer")
	require.Equal(t, "fp-1", out.SkillImage.OwnerFingerprint)
	require.Equal(t, 3, out.SkillImage.Generation)
	out.SkillImage.SnapshotID = "mutated"
	require.Equal(t, "snap-1", existing.SkillImage.SnapshotID,
		"merge must copy SkillImage so later mutation cannot touch the stored row")
}

func TestMergeSandboxConfigForUpdateIgnoresIncomingSkillImageOnCreate(t *testing.T) {
	incoming := &TenantSandboxConfig{
		E2B:        &E2BSandboxConfig{APIKey: "new-e2b"},
		SkillImage: &SkillImageConfig{SnapshotID: "forged-snap"},
	}

	out := MergeSandboxConfigForUpdate(incoming, nil)

	require.Nil(t, out.SkillImage, "create must not accept a client-supplied skill image")
}

// The editor never sends skill_image: that pointer is written only by an
// install or a removal. A merge that copied the incoming payload as-is would
// therefore wipe a live snapshot on every "保存运行配置", leaving the skill
// rows in place while every session fell back to the base template. VolumeMount
// is covered here too because the editor omits it for the same reason.
func TestMergeSandboxConfigForUpdateKeepsSkillImageWhenEditorOmitsIt(t *testing.T) {
	existing := &TenantSandboxConfig{
		SandboxType: "cube",
		SkillImage: &SkillImageConfig{
			SnapshotID:       "snap-2",
			Generation:       2,
			BaseTemplateID:   "base-template",
			OwnerFingerprint: "fp-1",
		},
		VolumeMount: &VolumeMountConfig{Enabled: true, VolumeID: "vol-1"},
	}
	incoming := &TenantSandboxConfig{
		SandboxType: "cube",
		Cube:        &CubeSandboxConfig{APIURL: "https://cube.example.com"},
	}

	out := MergeSandboxConfigForUpdate(incoming, existing)

	require.NotNil(t, out.SkillImage)
	require.Equal(t, "snap-2", out.SkillImage.SnapshotID)
	require.Equal(t, 2, out.SkillImage.Generation)
	require.Equal(t, "base-template", out.SkillImage.BaseTemplateID)
	require.Equal(t, "fp-1", out.SkillImage.OwnerFingerprint)
	require.NotNil(t, out.VolumeMount)
	require.Equal(t, "vol-1", out.VolumeMount.VolumeID)

	out.SkillImage.SnapshotID = "mutated"
	require.Equal(t, "snap-2", existing.SkillImage.SnapshotID,
		"merge must copy the stored image, not share the pointer")
}

func TestMergeSandboxConfigForUpdateKeepsSkillRolloutWhenEditorOmitsIt(t *testing.T) {
	existing := &TenantSandboxConfig{
		SandboxType:  "cube",
		SkillRollout: SkillRolloutNewSession,
	}
	incoming := &TenantSandboxConfig{
		SandboxType: "cube",
		Cube:        &CubeSandboxConfig{APIURL: "https://cube.example.com"},
	}

	out := MergeSandboxConfigForUpdate(incoming, existing)

	require.Equal(t, SkillRolloutNewSession, out.SkillRollout,
		"a runtime save must not reset the skills-panel rollout choice")
}

func TestMergeSandboxConfigForUpdateHonoursExplicitSkillRollout(t *testing.T) {
	existing := &TenantSandboxConfig{SkillRollout: SkillRolloutNewSession}
	incoming := &TenantSandboxConfig{SkillRollout: SkillRolloutNextTurn}

	out := MergeSandboxConfigForUpdate(incoming, existing)

	require.Equal(t, SkillRolloutNextTurn, out.SkillRollout)
}

func TestMergeSandboxConfigForUpdateDoesNotMutateInputs(t *testing.T) {
	existing := &TenantSandboxConfig{E2B: &E2BSandboxConfig{APIKey: "old-e2b"}}
	incoming := &TenantSandboxConfig{
		E2B:     &E2BSandboxConfig{APIKey: RedactedSecretPlaceholder},
		EnvVars: map[string]string{"HF_TOKEN": RedactedSecretPlaceholder},
	}

	_ = MergeSandboxConfigForUpdate(incoming, existing)

	require.Equal(t, RedactedSecretPlaceholder, incoming.E2B.APIKey,
		"merge must not mutate the incoming payload")
	require.Equal(t, "old-e2b", existing.E2B.APIKey,
		"merge must not mutate the stored config")
}
