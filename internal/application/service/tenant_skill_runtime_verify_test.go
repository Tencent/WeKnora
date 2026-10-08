package service

import (
	"context"
	"os"
	"os/exec"
	"path"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/require"
)

// Run only the generated executable probe locally. This exercises shell
// quoting and PATH resolution without executing a supplied install script.
type runtimeProbeManager struct{ *installSandboxManager }

func (m runtimeProbeManager) SessionInstallShellExecutor() sandbox.SessionInstallShellExecutor {
	return m
}

func (m runtimeProbeManager) ExecShellCommandWithOptions(
	_ context.Context,
	_ string,
	command string,
	_ sandbox.ShellExecOptions,
) (*sandbox.ExecuteResult, error) {
	cmd := exec.Command("/bin/bash", "--noprofile", "--norc", "-c", command)
	output, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		code = cmd.ProcessState.ExitCode()
	}
	return &sandbox.ExecuteResult{ExitCode: code, Stderr: string(output)}, nil
}

func TestRuntimePrerequisitesMarkdownOnlySkillCannotSkipReport(t *testing.T) {
	fx := newInstallFixture(t)
	fx.bundle.Files = map[string][]byte{"SKILL.md": []byte("Requires the bsk CLI and browser extension.")}
	_, err := fx.svc.verifySkill(context.Background(), fx.sandboxMgr, "session", installSkillDir, fx.bundle)
	var gate *skillVerificationError
	require.ErrorAs(t, err, &gate)
	require.True(t, gate.Repairable)
	require.Contains(t, err.Error(), "install-report.json")
}

func TestRuntimePrerequisiteReportValidation(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"commands":null,"blockers":[]}`,
		`{"commands":[],"blockers":null}`,
		`{"commands":["bsk; touch bad"],"blockers":[]}`,
		`{"commands":["/root/.local/bin/bsk"],"blockers":[]}`,
		`{"commands":[],"blockers":[""]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			fx := newInstallFixture(t)
			fx.sandboxMgr.files = map[string][]byte{
				path.Join(installSkillDir, ".weknora/install-report.json"): []byte(raw),
			}
			err := fx.svc.verifyRuntimePrerequisites(context.Background(), fx.sandboxMgr, "session", installSkillDir)
			var gate *skillVerificationError
			require.ErrorAs(t, err, &gate)
			require.True(t, gate.Repairable)
		})
	}
}

func TestRuntimePrerequisitesResolveSkillLocalCLI(t *testing.T) {
	fx := newInstallFixture(t)
	dir := path.Join(t.TempDir(), "skill with ' quotes")
	binDir := path.Join(dir, ".weknora/bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	fx.sandboxMgr.files = map[string][]byte{
		path.Join(dir, ".weknora/install-report.json"): []byte(`{"commands":["weknora-test-cli"],"blockers":[]}`),
	}
	mgr := runtimeProbeManager{fx.sandboxMgr}
	err := fx.svc.verifyRuntimePrerequisites(context.Background(), mgr, "session", dir)
	var gate *skillVerificationError
	require.ErrorAs(t, err, &gate)
	require.True(t, gate.Repairable)
	require.Contains(t, err.Error(), "weknora-test-cli")
	require.NoError(t, os.WriteFile(path.Join(binDir, "weknora-test-cli"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	require.NoError(t, fx.svc.verifyRuntimePrerequisites(context.Background(), mgr, "session", dir))
}

func TestRunInstallDoesNotSnapshotUnresolvedExternalPrerequisites(t *testing.T) {
	fx := newInstallFixture(t)
	fx.sandboxMgr.files = map[string][]byte{
		path.Join(installSkillDir, ".weknora/install-report.json"): []byte(
			`{"commands":["bsk"],"blockers":["The browser extension cannot reach the remote sandbox daemon."]}`,
		),
	}
	err := fx.svc.runInstall(context.Background(), 7, "cfg-1", "sk-1", fx.bundle)
	require.ErrorContains(t, err, "browser extension")
	require.NotContains(t, fx.events, "create-snapshot")
	require.Len(t, fx.agentPrompts, 1, "an external setup blocker must not trigger pointless package retries")
}

func TestRuntimeReportPromptsDoNotPrescribeSkillSpecificValues(t *testing.T) {
	fx := newInstallFixture(t)
	fx.bundle.Files = map[string][]byte{
		"SKILL.md": []byte("Summarize supplied text. No external commands are required."),
	}
	prompts := map[string]string{
		"install": buildInstallPrompt(installSkillDir, fx.bundle, nil),
		"repair": buildRepairPrompt(installSkillDir, &skillVerificationError{
			Language: "runtime prerequisites", Problems: []string{"Missing install-report.json"},
		}),
	}
	for name, prompt := range prompts {
		t.Run(name, func(t *testing.T) {
			for _, field := range []string{".weknora/install-report.json", "commands:", "blockers:"} {
				require.Contains(t, prompt, field)
			}
			for _, leakedExample := range []string{
				"bsk", "browser extension", "127.0.0.1", "Describe an unresolved external prerequisite here",
			} {
				require.NotContains(t, strings.ToLower(prompt), strings.ToLower(leakedExample))
			}
		})
	}
}
