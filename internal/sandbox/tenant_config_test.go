package sandbox

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func globalTestConfig() *Config {
	return &Config{
		Type:             SandboxTypeE2B,
		DefaultTimeout:   60 * time.Second,
		E2BAPIKey:        "global-key",
		E2BAPIURL:        "https://global.e2b.dev",
		E2BSandboxDomain: "global.domain",
		E2BTemplate:      "global-template",
		E2BSandboxTTL:    10 * time.Minute,
		E2BHTTPTimeout:   30 * time.Second,
	}
}

// completeE2BTenantConfig is the minimum a named E2B config must carry now that
// nothing is inherited.
func completeE2BTenantConfig() *types.TenantSandboxConfig {
	return &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B: &types.E2BSandboxConfig{
			APIKey:     "tenant-key",
			TemplateID: "tenant-template",
		},
	}
}

// A nil tenant config means "the deployment default backend", which is the one
// path where the baseline is used as-is.
func TestResolveEffectiveConfigNilTenantKeepsGlobal(t *testing.T) {
	global := globalTestConfig()

	got, err := ResolveEffectiveConfig(nil, global)

	require.NoError(t, err)
	require.Equal(t, *global, *got)
}

func TestResolveEffectiveConfigDoesNotMutateGlobal(t *testing.T) {
	global := globalTestConfig()

	_, err := ResolveEffectiveConfig(completeE2BTenantConfig(), global)

	require.NoError(t, err)
	require.Equal(t, "global-key", global.E2BAPIKey,
		"resolution must not leak tenant values into the shared global config")
}

// The point of the whole design: a named config never picks up the deployment's
// endpoint, domain or key, so what it does not state it does not get.
func TestResolveEffectiveConfigDoesNotInheritProviderFields(t *testing.T) {
	global := globalTestConfig()

	got, err := ResolveEffectiveConfig(completeE2BTenantConfig(), global)

	require.NoError(t, err)
	require.Equal(t, "tenant-key", got.E2BAPIKey)
	require.Equal(t, "tenant-template", got.E2BTemplate)
	require.Empty(t, got.E2BAPIURL, "go-e2b resolves its own API base when unset")
	require.Empty(t, got.E2BSandboxDomain)
}

// A leftover sub-struct from the deployment's other provider must not survive
// either, or a cube config would silently answer with e2b coordinates.
func TestResolveEffectiveConfigClearsInactiveProviderBaseline(t *testing.T) {
	global := globalTestConfig() // global is e2b, with e2b credentials set

	got, err := ResolveEffectiveConfig(&types.TenantSandboxConfig{
		SandboxType: "cube",
		Cube: &types.CubeSandboxConfig{
			APIKey: "cube-key", APIURL: "https://203.0.113.20",
			ProxyURL: "https://203.0.113.21", SandboxDomain: "cube.example",
			TemplateID: "cube-template",
		},
	}, global)

	require.NoError(t, err)
	require.Equal(t, SandboxTypeCube, got.Type)
	require.Equal(t, "https://203.0.113.20", got.CubeAPIURL)
	require.Empty(t, got.E2BAPIKey, "the baseline's e2b credentials must not ride along")
	require.Empty(t, got.E2BTemplate)
}

func TestResolveEffectiveConfigRejectsIncompleteCube(t *testing.T) {
	_, err := ResolveEffectiveConfig(&types.TenantSandboxConfig{
		SandboxType: "cube",
		Cube:        &types.CubeSandboxConfig{APIURL: "https://203.0.113.20"},
	}, globalTestConfig())

	require.ErrorIs(t, err, ErrSandboxConfigIncomplete)
	require.Contains(t, err.Error(), "proxy_url")
	require.Contains(t, err.Error(), "sandbox_domain")
	require.Contains(t, err.Error(), "template_id")
}

func TestResolveEffectiveConfigCopiesCubeDNSServers(t *testing.T) {
	got, err := ResolveEffectiveConfig(&types.TenantSandboxConfig{
		SandboxType:           "cube",
		AllowPrivateEndpoints: true,
		Cube: &types.CubeSandboxConfig{
			APIKey: "cube-key", APIURL: "https://203.0.113.20",
			ProxyURL: "https://203.0.113.21", SandboxDomain: "cube.example",
			TemplateID: "cube-template",
			DNSServers: []string{" 8.8.8.8 ", "8.8.8.8", "1.1.1.1"},
		},
	}, globalTestConfig())

	require.NoError(t, err)
	require.Equal(t, []string{"8.8.8.8", "1.1.1.1"}, got.CubeDNSServers)
}

func TestResolveEffectiveConfigRejectsInvalidCubeDNS(t *testing.T) {
	_, err := ResolveEffectiveConfig(&types.TenantSandboxConfig{
		SandboxType:           "cube",
		AllowPrivateEndpoints: true,
		Cube: &types.CubeSandboxConfig{
			APIKey: "cube-key", APIURL: "https://203.0.113.20",
			ProxyURL: "https://203.0.113.21", SandboxDomain: "cube.example",
			TemplateID: "cube-template",
			DNSServers: []string{"dns.google"},
		},
	}, globalTestConfig())

	require.Error(t, err)
	require.Contains(t, err.Error(), "dns.google")
}

func TestResolveEffectiveConfigRejectsIncompleteE2B(t *testing.T) {
	_, err := ResolveEffectiveConfig(&types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &types.E2BSandboxConfig{TemplateID: "t1"},
	}, globalTestConfig())

	require.ErrorIs(t, err, ErrSandboxConfigIncomplete)
	require.Contains(t, err.Error(), "api_key")
}

func TestResolveEffectiveConfigAppliesTimeoutsAndTTL(t *testing.T) {
	global := globalTestConfig()
	tenantCfg := completeE2BTenantConfig()
	tenantCfg.DefaultTimeoutSec = 90
	tenantCfg.E2B.HTTPTimeoutSec = 15
	tenantCfg.E2B.E2BSandboxTTLSeconds = 600

	got, err := ResolveEffectiveConfig(tenantCfg, global)

	require.NoError(t, err)
	require.Equal(t, 90*time.Second, got.DefaultTimeout)
	require.Equal(t, 15*time.Second, got.E2BHTTPTimeout)
	require.Equal(t, 600*time.Second, got.E2BSandboxTTL)
}

// Tuning fields fall back to the built-in constants, never to the deployment's:
// "inherits nothing" would be a much weaker rule with an exception here.
func TestResolveEffectiveConfigTuningFallsBackToBuiltIns(t *testing.T) {
	global := globalTestConfig()
	global.E2BSandboxTTL = 10 * time.Minute
	global.E2BHTTPTimeout = 90 * time.Second

	got, err := ResolveEffectiveConfig(completeE2BTenantConfig(), global)

	require.NoError(t, err)
	require.Equal(t, DefaultE2BSandboxTTL, got.E2BSandboxTTL)
	require.Equal(t, DefaultE2BHTTPTimeout, got.E2BHTTPTimeout)
	require.Equal(t, global.DefaultTimeout, got.DefaultTimeout,
		"the execution timeout is deployment policy and does carry over")
}

func TestResolveEffectiveConfigDisabled(t *testing.T) {
	tenantCfg := &types.TenantSandboxConfig{SandboxType: "disabled"}

	got, err := ResolveEffectiveConfig(tenantCfg, globalTestConfig())

	require.NoError(t, err)
	require.Equal(t, SandboxTypeDisabled, got.Type)
}

func TestResolveEffectiveConfigRejectsUnknownType(t *testing.T) {
	tenantCfg := &types.TenantSandboxConfig{SandboxType: "quantum"}

	_, err := ResolveEffectiveConfig(tenantCfg, globalTestConfig())

	require.Error(t, err)
}

func TestResolveEffectiveConfigRejectsUnsafeURL(t *testing.T) {
	tenantCfg := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &types.E2BSandboxConfig{APIURL: "http://169.254.169.254"},
	}

	_, err := ResolveEffectiveConfig(tenantCfg, globalTestConfig())

	require.ErrorIs(t, err, ErrUnsafeOutboundURL)
}

func TestResolveEffectiveConfigRejectsUnsafeCubeProxyURL(t *testing.T) {
	tenantCfg := &types.TenantSandboxConfig{
		SandboxType: "cube",
		Cube: &types.CubeSandboxConfig{
			APIURL:   "https://203.0.113.10",
			ProxyURL: "http://127.0.0.1:80",
		},
	}

	_, err := ResolveEffectiveConfig(tenantCfg, globalTestConfig())

	require.ErrorIs(t, err, ErrUnsafeOutboundURL)
}

func TestResolveEffectiveConfigRejectsDockerHostNetwork(t *testing.T) {
	_, err := ResolveEffectiveConfig(&types.TenantSandboxConfig{
		SandboxType: "docker",
		Docker: &types.DockerSandboxConfig{
			Image:       "weknora:test",
			NetworkMode: "host",
		},
	}, DefaultConfig())
	require.Error(t, err)
}

// A blank host must come out of config resolution already pointing at the
// daemon the Docker CLI would use, because that resolved value is what the
// connectivity check dials. Leaving it empty here is what made the check fail
// on a Colima or Docker Desktop host, where /var/run/docker.sock is absent.
func TestResolveEffectiveConfigDetectsLocalDockerHostWhenBlank(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///tmp/from-env.sock")

	effective, err := ResolveEffectiveConfig(&types.TenantSandboxConfig{
		SandboxType: "docker",
		Docker:      &types.DockerSandboxConfig{Image: "weknora:test"},
	}, DefaultConfig())

	require.NoError(t, err)
	require.Equal(t, "unix:///tmp/from-env.sock", effective.DockerHost)
}

func TestResolveEffectiveConfigRejectsPlaintextDockerTCP(t *testing.T) {
	_, err := ResolveEffectiveConfig(&types.TenantSandboxConfig{
		SandboxType:           "docker",
		AllowPrivateEndpoints: true,
		Docker: &types.DockerSandboxConfig{
			Image: "weknora:test",
			Host:  "tcp://10.0.0.5:2376",
		},
	}, DefaultConfig())
	require.Error(t, err)
}

// The same bar has to apply to a host nobody typed. A blank field is filled in
// from DOCKER_HOST, and on a deployment pointed at a plaintext daemon that
// resolved value is what gets dialled — so validating only the stored string
// would accept a config that cannot work and say so only at the first sandbox.
func TestResolveEffectiveConfigRejectsResolvedPlaintextDockerTCP(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://10.0.0.5:2375")

	_, err := ResolveEffectiveConfig(&types.TenantSandboxConfig{
		SandboxType:           "docker",
		AllowPrivateEndpoints: true,
		Docker:                &types.DockerSandboxConfig{Image: "weknora:test"},
	}, DefaultConfig())
	require.Error(t, err)
}

// A resolved host is still only a daemon endpoint, so it answers to the same
// outbound policy an explicitly typed one does.
func TestResolveEffectiveConfigRejectsResolvedPrivateDockerHostWithoutOptIn(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://10.0.0.5:2376")

	_, err := ResolveEffectiveConfig(&types.TenantSandboxConfig{
		SandboxType: "docker",
		Docker: &types.DockerSandboxConfig{
			Image:       "weknora:test",
			TLSCertPath: "/etc/weknora/docker-certs",
		},
	}, DefaultConfig())
	require.ErrorIs(t, err, ErrUnsafeOutboundURL)
}

// A missing image is a field the admin can see and fix in the form, so it must
// still be reported ahead of anything about the daemon endpoint.
func TestResolveEffectiveConfigReportsMissingImageBeforeHostProblems(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://10.0.0.5:2375")

	_, err := ResolveEffectiveConfig(&types.TenantSandboxConfig{
		SandboxType: "docker",
	}, DefaultConfig())
	require.ErrorIs(t, err, ErrSandboxConfigIncomplete)
}

func TestEffectiveTemplateIDPerProvider(t *testing.T) {
	require.Equal(t, "e2b-tpl", EffectiveTemplateID(&Config{
		Type: SandboxTypeE2B, E2BTemplate: "e2b-tpl", CubeTemplate: "cube-tpl",
	}))
	require.Equal(t, "cube-tpl", EffectiveTemplateID(&Config{
		Type: SandboxTypeCube, E2BTemplate: "e2b-tpl", CubeTemplate: "cube-tpl",
	}))
	require.Empty(t, EffectiveTemplateID(&Config{Type: SandboxTypeDisabled}))
	require.Empty(t, EffectiveTemplateID(nil))
}

func TestResolveEffectiveConfigUsesSkillSnapshotAsTemplate(t *testing.T) {
	global := DefaultConfig()

	base := &types.TenantSandboxConfig{
		SandboxType: "cube",
		Cube: &types.CubeSandboxConfig{
			APIURL: "https://203.0.113.10", ProxyURL: "https://203.0.113.11",
			SandboxDomain: "cube.example.com", APIKey: "key-1", TemplateID: "tpl-base",
		},
	}
	fp := SkillImageFingerprint("cube", "key-1", "https://203.0.113.10")

	t.Run("usable snapshot overrides the base template", func(t *testing.T) {
		cfg := *base
		cfg.SkillImage = &types.SkillImageConfig{SnapshotID: "snap-1", OwnerFingerprint: fp}

		eff, err := ResolveEffectiveConfig(&cfg, global)

		require.NoError(t, err)
		require.Equal(t, "snap-1", eff.CubeTemplate)
	})

	t.Run("fingerprint mismatch falls back to the base template", func(t *testing.T) {
		cfg := *base
		cfg.SkillImage = &types.SkillImageConfig{
			SnapshotID: "snap-1", OwnerFingerprint: "fingerprint-of-another-account",
		}

		eff, err := ResolveEffectiveConfig(&cfg, global)

		require.NoError(t, err)
		require.Equal(t, "tpl-base", eff.CubeTemplate,
			"a snapshot from another account is invisible; the session must still boot")
	})

	t.Run("empty snapshot keeps the base template", func(t *testing.T) {
		cfg := *base
		cfg.SkillImage = &types.SkillImageConfig{OwnerFingerprint: fp}

		eff, err := ResolveEffectiveConfig(&cfg, global)

		require.NoError(t, err)
		require.Equal(t, "tpl-base", eff.CubeTemplate)
	})
}

func TestResolveEffectiveConfigUsesSkillSnapshotAsE2BTemplate(t *testing.T) {
	global := DefaultConfig()

	base := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B: &types.E2BSandboxConfig{
			APIURL: "https://203.0.113.20", SandboxDomain: "e2b.example.com",
			APIKey: "key-1", TemplateID: "tpl-base",
		},
	}
	fp := SkillImageFingerprint("e2b", "key-1", "https://203.0.113.20")
	cubeFp := SkillImageFingerprint("cube", "key-1", "https://203.0.113.20")

	t.Run("usable snapshot overrides the base template", func(t *testing.T) {
		cfg := *base
		cfg.SkillImage = &types.SkillImageConfig{SnapshotID: "snap-1", OwnerFingerprint: fp}

		eff, err := ResolveEffectiveConfig(&cfg, global)

		require.NoError(t, err)
		require.Equal(t, "snap-1", eff.E2BTemplate)
	})

	t.Run("fingerprint mismatch falls back to the base template", func(t *testing.T) {
		cfg := *base
		cfg.SkillImage = &types.SkillImageConfig{
			SnapshotID: "snap-1", OwnerFingerprint: cubeFp,
		}

		eff, err := ResolveEffectiveConfig(&cfg, global)

		require.NoError(t, err)
		require.Equal(t, "tpl-base", eff.E2BTemplate,
			"a snapshot whose fingerprint was computed for cube must not override e2b; the session must still boot")
	})

	t.Run("empty snapshot keeps the base template", func(t *testing.T) {
		cfg := *base
		cfg.SkillImage = &types.SkillImageConfig{OwnerFingerprint: fp}

		eff, err := ResolveEffectiveConfig(&cfg, global)

		require.NoError(t, err)
		require.Equal(t, "tpl-base", eff.E2BTemplate)
	})
}

func TestResolveEffectiveConfigUsesSkillSnapshotAsDockerImage(t *testing.T) {
	global := DefaultConfig()
	base := &types.TenantSandboxConfig{
		SandboxType: "docker",
		Docker: &types.DockerSandboxConfig{
			Image: "weknora/sandbox:base",
			Host:  "unix:///var/run/docker.sock",
		},
	}
	fp := SkillImageFingerprint("docker", "", "unix:///var/run/docker.sock")

	t.Run("usable snapshot overrides the base image", func(t *testing.T) {
		cfg := *base
		cfg.Docker = &types.DockerSandboxConfig{
			Image: "weknora/sandbox:base", Host: "unix:///var/run/docker.sock",
		}
		cfg.SkillImage = &types.SkillImageConfig{
			SnapshotID: "weknora-skill/weknora-sk-cfg1-g1", OwnerFingerprint: fp,
		}

		eff, err := ResolveEffectiveConfig(&cfg, global)

		require.NoError(t, err)
		require.Equal(t, "weknora-skill/weknora-sk-cfg1-g1", eff.DockerImage)
	})

	t.Run("fingerprint mismatch falls back to the base image", func(t *testing.T) {
		cfg := *base
		cfg.Docker = &types.DockerSandboxConfig{
			Image: "weknora/sandbox:base", Host: "unix:///var/run/docker.sock",
		}
		cfg.SkillImage = &types.SkillImageConfig{
			SnapshotID: "weknora-skill/weknora-sk-cfg1-g1", OwnerFingerprint: "another-daemon",
		}

		eff, err := ResolveEffectiveConfig(&cfg, global)

		require.NoError(t, err)
		require.Equal(t, "weknora/sandbox:base", eff.DockerImage,
			"a snapshot from another daemon is invisible; the session must still boot")
	})

	// A blank host follows the environment, so binding the fingerprint to the
	// resolved daemon would make "switch docker context" indistinguishable
	// from "rotate the credentials": sessions would silently lose every
	// skill, installs would be refused, and the snapshot prune would skip the
	// config forever. The images are on the same disk either way.
	t.Run("blank host survives a change of local daemon", func(t *testing.T) {
		newConfig := func() *types.TenantSandboxConfig {
			return &types.TenantSandboxConfig{
				SandboxType: "docker",
				Docker:      &types.DockerSandboxConfig{Image: "weknora/sandbox:base"},
				SkillImage: &types.SkillImageConfig{
					SnapshotID: "weknora-skill/weknora-sk-cfg1-g1",
					OwnerFingerprint: SkillOwnerFingerprint(&types.TenantSandboxConfig{
						SandboxType: "docker",
						Docker:      &types.DockerSandboxConfig{Image: "weknora/sandbox:base"},
					}),
				},
			}
		}

		for _, host := range []string{
			"unix:///tmp/colima.sock",
			"unix:///tmp/orbstack.sock",
		} {
			t.Setenv("DOCKER_HOST", host)
			cfg := newConfig()

			eff, err := ResolveEffectiveConfig(cfg, global)

			require.NoError(t, err)
			require.Equal(t, "weknora-skill/weknora-sk-cfg1-g1", eff.DockerImage,
				"DOCKER_HOST=%s must not retire the config's skill image", host)
			require.True(t, SkillImageActive(cfg))
		}
	})

	// An explicit host is still part of the identity: only an admin editing
	// the config can change it, and it may well be another machine.
	t.Run("an explicit host change retires the image", func(t *testing.T) {
		cfg := *base
		cfg.Docker = &types.DockerSandboxConfig{
			Image: "weknora/sandbox:base", Host: "tcp://198.51.100.10:2376",
			TLSCertPath: "/certs",
		}
		cfg.SkillImage = &types.SkillImageConfig{
			SnapshotID: "weknora-skill/weknora-sk-cfg1-g1", OwnerFingerprint: fp,
		}

		eff, err := ResolveEffectiveConfig(&cfg, global)

		require.NoError(t, err)
		require.Equal(t, "weknora/sandbox:base", eff.DockerImage)
	})
}

func TestSkillOwnerFingerprintForDocker(t *testing.T) {
	require.Empty(t, SkillOwnerFingerprint(&types.TenantSandboxConfig{SandboxType: "docker"}),
		"a docker type with no daemon block cannot own a snapshot")
	require.Empty(t, SkillOwnerFingerprint(&types.TenantSandboxConfig{SandboxType: "disabled"}))

	got := SkillOwnerFingerprint(&types.TenantSandboxConfig{
		SandboxType: "docker",
		Docker: &types.DockerSandboxConfig{
			Image: "img", Host: "unix:///var/run/docker.sock", TLSCertPath: "/certs",
		},
	})
	require.Equal(t, SkillImageFingerprint("docker", "/certs", "unix:///var/run/docker.sock"), got)
}

// SkillImageActive is what the agent side asks before telling a model about an
// installed skill, so it must agree with the template ResolveEffectiveConfig
// actually boots. Any disagreement means either skills that are announced and
// cannot run, or skills that are in the image and hidden.
func TestSkillImageActiveAgreesWithTheResolvedTemplate(t *testing.T) {
	global := DefaultConfig()
	cube := func() *types.TenantSandboxConfig {
		return &types.TenantSandboxConfig{
			SandboxType: "cube",
			Cube: &types.CubeSandboxConfig{
				APIURL: "https://203.0.113.10", ProxyURL: "https://203.0.113.11",
				SandboxDomain: "cube.example.com", APIKey: "key-1", TemplateID: "tpl-base",
			},
		}
	}
	fp := SkillImageFingerprint("cube", "key-1", "https://203.0.113.10")

	cases := map[string]struct {
		config *types.TenantSandboxConfig
		want   bool
	}{
		"snapshot owned by the live credentials": {
			config: func() *types.TenantSandboxConfig {
				cfg := cube()
				cfg.SkillImage = &types.SkillImageConfig{SnapshotID: "snap-1", OwnerFingerprint: fp}
				return cfg
			}(),
			want: true,
		},
		"snapshot from another account": {
			config: func() *types.TenantSandboxConfig {
				cfg := cube()
				cfg.SkillImage = &types.SkillImageConfig{
					SnapshotID: "snap-1", OwnerFingerprint: "another-account",
				}
				return cfg
			}(),
			want: false,
		},
		"snapshot with no recorded owner": {
			config: func() *types.TenantSandboxConfig {
				cfg := cube()
				cfg.SkillImage = &types.SkillImageConfig{SnapshotID: "snap-1"}
				return cfg
			}(),
			want: false,
		},
		"no snapshot yet": {
			config: cube(),
			want:   false,
		},
		"backend that cannot snapshot": {
			config: &types.TenantSandboxConfig{
				SandboxType: "disabled",
				SkillImage: &types.SkillImageConfig{
					SnapshotID: "snap-1", OwnerFingerprint: fp,
				},
			},
			want: false,
		},
		"docker snapshot owned by the live daemon": {
			config: &types.TenantSandboxConfig{
				SandboxType: "docker",
				Docker: &types.DockerSandboxConfig{
					Image: "weknora/sandbox:base",
					Host:  "unix:///var/run/docker.sock",
				},
				SkillImage: &types.SkillImageConfig{
					SnapshotID: "weknora-skill/weknora-sk-cfg1-g1",
					OwnerFingerprint: SkillImageFingerprint(
						"docker", "", "unix:///var/run/docker.sock",
					),
				},
			},
			want: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			active := SkillImageActive(tc.config)
			require.Equal(t, tc.want, active)

			eff, err := ResolveEffectiveConfig(tc.config, global)
			require.NoError(t, err)
			booted := EffectiveTemplateID(eff)
			if active {
				require.Equal(t, tc.config.SkillImage.SnapshotID, booted)
				return
			}
			if tc.config.SkillImage != nil && tc.config.SkillImage.SnapshotID != "" {
				require.NotEqual(t, tc.config.SkillImage.SnapshotID, booted,
					"a skill declared unusable must not be the image the session boots")
			}
		})
	}

	require.False(t, SkillImageActive(nil))
}

func TestSkillImageFingerprintIsStableAndDiscriminating(t *testing.T) {
	a := SkillImageFingerprint("cube", "key-1", "https://a.example.com")
	require.Equal(t, a, SkillImageFingerprint("cube", "key-1", "https://a.example.com"))
	require.NotEqual(t, a, SkillImageFingerprint("cube", "key-2", "https://a.example.com"))
	require.NotEqual(t, a, SkillImageFingerprint("e2b", "key-1", "https://a.example.com"))
}
