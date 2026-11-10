package sandbox

import "testing"

func isolateDockerEnabled(t *testing.T) {
	t.Helper()
	ClearDockerBackendEnabledOverride()
	t.Cleanup(ClearDockerBackendEnabledOverride)
}

func TestDockerBackendEnabledDefaultsOff(t *testing.T) {
	isolateDockerEnabled(t)
	t.Setenv(DockerBackendEnabledEnv, "")
	if DockerBackendEnabled() {
		t.Fatal("empty env must leave the docker backend disabled")
	}
	if err := EnsureDockerBackendAllowed(SandboxTypeDocker); err != ErrDockerBackendDisabled {
		t.Fatalf("EnsureDockerBackendAllowed(docker) = %v, want ErrDockerBackendDisabled", err)
	}
	if err := EnsureDockerBackendAllowed(SandboxTypeCube); err != nil {
		t.Fatalf("EnsureDockerBackendAllowed(cube) = %v, want nil", err)
	}
}

func TestDockerBackendEnabledAcceptsParseBoolTrue(t *testing.T) {
	isolateDockerEnabled(t)
	for _, raw := range []string{"true", "TRUE", "1", "t"} {
		t.Run(raw, func(t *testing.T) {
			isolateDockerEnabled(t)
			t.Setenv(DockerBackendEnabledEnv, raw)
			if !DockerBackendEnabled() {
				t.Fatalf("%q should enable the docker backend", raw)
			}
			if err := EnsureDockerBackendAllowed(SandboxTypeDocker); err != nil {
				t.Fatalf("EnsureDockerBackendAllowed(docker) = %v, want nil", err)
			}
		})
	}
}

func TestDockerBackendEnabledRejectsFalsey(t *testing.T) {
	isolateDockerEnabled(t)
	for _, raw := range []string{"false", "0", "no", "yes", "on"} {
		t.Run(raw, func(t *testing.T) {
			isolateDockerEnabled(t)
			t.Setenv(DockerBackendEnabledEnv, raw)
			if DockerBackendEnabled() {
				t.Fatalf("%q must not enable the docker backend", raw)
			}
		})
	}
}

func TestSetDockerBackendEnabledOverridesEnv(t *testing.T) {
	isolateDockerEnabled(t)
	t.Setenv(DockerBackendEnabledEnv, "false")
	SetDockerBackendEnabled(true)
	if !DockerBackendEnabled() {
		t.Fatal("system-settings push must enable even when env is false")
	}
	SetDockerBackendEnabled(false)
	t.Setenv(DockerBackendEnabledEnv, "true")
	if DockerBackendEnabled() {
		t.Fatal("system-settings false must win over env true")
	}
}
