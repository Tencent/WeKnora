//go:build docker_integration

package skills

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/require"
)

func TestBuiltinShellEnvironmentIntegration(t *testing.T) {
	image := os.Getenv("DOCKER_INTEGRATION_IMAGE")
	if image == "" {
		t.Skip("DOCKER_INTEGRATION_IMAGE is required")
	}
	profile := os.Getenv("DOCKER_INTEGRATION_PROFILE")
	if profile == "" {
		profile = "office-core"
	}
	manifest, err := builtin.PublishedManifestForProfile(profile)
	require.NoError(t, err)
	manager := NewManager(&ManagerConfig{Enabled: true}, nil).WithTenantSource(NewBuiltinSkillSource(manifest))
	require.NoError(t, manager.Initialize(context.Background()))
	available := map[string]bool{}
	for _, entry := range manifest.Skills {
		available[entry.Name] = true
	}
	// Execute the real wrappers sequentially in one container. Conflicting
	// pinned versions must remain isolated, regardless of the previous call.
	var script strings.Builder
	script.WriteString("set -eu\n")
	for _, item := range []struct{ name, dependency, version string }{
		{"docx", "lxml", "6.0.2"},
		{"powerpoint", "lxml", "6.1.3"},
		{"pdf", "pillow", "12.3.0"},
		{"xlsx", "pillow", "11.3.0"},
		{"docx", "lxml", "6.0.2"},
	} {
		if !available[item.name] {
			continue
		}
		code := fmt.Sprintf("import sys, importlib.metadata as m; assert sys.prefix == %q; assert m.version(%q) == %q",
			builtin.ImageRoot+"/"+item.name+"/.venv", item.dependency, item.version)
		wrapped, _, err := manager.PrepareShellEnvironment(context.Background(), "integration", item.name,
			"python3 -c "+sandbox.ShellQuote(code), nil)
		require.NoError(t, err)
		script.WriteString("/bin/bash -c " + sandbox.ShellQuote(wrapped) + "\n")
	}
	nodeCommand := "node -e " + sandbox.ShellQuote("const p = require('pptxgenjs'); if (typeof p !== 'function') process.exit(1); require(process.env.WEKNORA_SKILL_DIR + '/assets/pptxgenjs_helpers')")
	nodeWrapped, nodeEnv, err := manager.PrepareShellEnvironment(context.Background(), "integration", "powerpoint", nodeCommand, nil)
	require.NoError(t, err)
	for key, value := range nodeEnv {
		script.WriteString("export " + key + "=" + sandbox.ShellQuote(value) + "\n")
	}
	script.WriteString("/bin/bash -c " + sandbox.ShellQuote(nodeWrapped) + "\n")
	dir := builtin.ImageRoot + "/pdf"
	script.WriteString("mv " + dir + "/.venv " + dir + "/.venv-disabled\n")
	wrapped, _, err := manager.PrepareShellEnvironment(context.Background(), "integration", "pdf", "python3 -c 'print(123)'", nil)
	require.NoError(t, err)
	script.WriteString("set +e\n/bin/bash -c " + sandbox.ShellQuote(wrapped) + "\nstatus=$?\nset -e\ntest \"$status\" -eq 126\n")
	command, args := sandbox.SkillInterpreterCommand(dir, dir+"/scripts/weknora_smoke.py")
	script.WriteString("set +e\n" + sandbox.ShellQuote(command))
	for _, arg := range args {
		script.WriteString(" " + sandbox.ShellQuote(arg))
	}
	script.WriteString("\nstatus=$?\nset -e\ntest \"$status\" -eq 126\n")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-i", "--entrypoint", "/bin/bash", image)
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Contains(t, string(out), "builtin skill runtime missing")
}
