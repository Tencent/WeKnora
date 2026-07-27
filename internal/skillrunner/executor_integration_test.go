package skillrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fixedVersionResolver struct{ version ResolvedVersion }

func (resolver fixedVersionResolver) Resolve(context.Context, ExecuteRequest) (ResolvedVersion, error) {
	return resolver.version, nil
}

func TestDockerExecutionIsolationAndCleanup(t *testing.T) {
	if os.Getenv("SKILLRUNNER_DOCKER_INTEGRATION") != "1" {
		t.Skip("set SKILLRUNNER_DOCKER_INTEGRATION=1")
	}
	sourceVolume := "weknora-skill-test-source-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	runDocker(t, "volume", "create", sourceVolume)
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", sourceVolume).Run() })
	script := `import os, socket
print("uid=" + str(os.getuid()))
try:
 open("/skill/mutated", "w").write("bad")
 print("skill_rw=true")
except Exception:
 print("skill_rw=false")
try:
 socket.create_connection(("1.1.1.1", 53), .2)
 print("network=true")
except Exception:
 print("network=false")
try:
 open("/work/large", "wb").write(b"x" * (80 << 20))
 print("quota=false")
except Exception:
 print("quota=true")
`
	populateSourceVolume(t, sourceVolume, "1/skill/version/scripts/check.py", script)
	manifest := "---\nname: test\ndescription: test\nscripts:\n  - scripts/check.py\n---\n# Test\n"
	populateSourceVolume(t, sourceVolume, "1/skill/version/SKILL.md", manifest)
	populateSourceVolume(t, sourceVolume, "1/skill/version/z-reference.md", "reference\n")
	version := ResolvedVersion{SourcePath: "/data/skills/1/skill/version", ContentHash: fixtureHash(map[string]string{"SKILL.md": manifest, "scripts/check.py": script, "z-reference.md": "reference\n"}), SourceVolume: sourceVolume, AllowedScripts: []string{"scripts/check.py"}}
	executor := NewExecutor(fixedVersionResolver{version: version}, 10*time.Second)
	request := ExecuteRequest{ExecutionID: "same-id", TenantID: "1", SkillID: "skill", VersionID: "version", ContentHash: version.ContentHash, ScriptPath: "scripts/check.py"}
	result, err := executor.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"uid=1000", "skill_rw=false", "network=false", "quota=true"} {
		if !strings.Contains(result.Stdout, expected) {
			t.Fatalf("missing %q in %q stderr=%q", expected, result.Stdout, result.Stderr)
		}
	}
	assertNoRunnerResources(t)

	populateSourceVolume(t, sourceVolume, "1/skill/version/scripts/sleep.py", "import time\ntime.sleep(10)\n")
	version.ContentHash = fixtureHash(map[string]string{"SKILL.md": manifest, "scripts/check.py": script, "scripts/sleep.py": "import time\ntime.sleep(10)\n", "z-reference.md": "reference\n"})
	version.AllowedScripts = []string{"scripts/sleep.py"}
	timeoutRequest := request
	timeoutRequest.ContentHash = version.ContentHash
	timeoutRequest.ScriptPath = "scripts/sleep.py"
	timeoutExecutor := NewExecutor(fixedVersionResolver{version: version}, 250*time.Millisecond)
	timeoutResult, err := timeoutExecutor.Execute(context.Background(), timeoutRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !timeoutResult.Killed {
		t.Fatal("expected timeout execution to be killed")
	}
	assertNoRunnerResources(t)
}

func TestDockerConcurrentSameExternalExecutionIDUsesDistinctResources(t *testing.T) {
	if os.Getenv("SKILLRUNNER_DOCKER_INTEGRATION") != "1" {
		t.Skip("set SKILLRUNNER_DOCKER_INTEGRATION=1")
	}
	sourceVolume := "weknora-skill-test-source-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	runDocker(t, "volume", "create", sourceVolume)
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", sourceVolume).Run() })
	populateSourceVolume(t, sourceVolume, "1/skill/version/scripts/run.py", "print('ok')\n")
	version := ResolvedVersion{SourcePath: "/data/skills/1/skill/version", ContentHash: fixtureHash(map[string]string{"scripts/run.py": "print('ok')\n"}), SourceVolume: sourceVolume, AllowedScripts: []string{"scripts/run.py"}}
	executor := NewExecutor(fixedVersionResolver{version: version}, 10*time.Second)
	request := ExecuteRequest{ExecutionID: "duplicate", TenantID: "1", SkillID: "skill", VersionID: "version", ContentHash: version.ContentHash, ScriptPath: "scripts/run.py"}
	var wait sync.WaitGroup
	errorsFound := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := executor.Execute(context.Background(), request)
			errorsFound <- err
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertNoRunnerResources(t)
}

func TestDockerRejectsPostCopyHashMismatchAndCleansResources(t *testing.T) {
	if os.Getenv("SKILLRUNNER_DOCKER_INTEGRATION") != "1" {
		t.Skip("set SKILLRUNNER_DOCKER_INTEGRATION=1")
	}
	sourceVolume := "weknora-skill-test-source-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	runDocker(t, "volume", "create", sourceVolume)
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", sourceVolume).Run() })
	populateSourceVolume(t, sourceVolume, "1/skill/version/scripts/run.py", "print('tampered')\n")
	version := ResolvedVersion{SourcePath: "/data/skills/1/skill/version", ContentHash: strings.Repeat("a", 64), SourceVolume: sourceVolume, AllowedScripts: []string{"scripts/run.py"}}
	executor := NewExecutor(fixedVersionResolver{version: version}, 10*time.Second)
	request := ExecuteRequest{ExecutionID: "tamper", TenantID: "1", SkillID: "skill", VersionID: "version", ContentHash: version.ContentHash, ScriptPath: "scripts/run.py"}
	if _, err := executor.Execute(context.Background(), request); err == nil || !strings.Contains(err.Error(), "content hash mismatch") {
		t.Fatalf("expected copied-volume hash mismatch, got %v", err)
	}
	assertNoRunnerResources(t)
}

func TestDockerPreparationCancellationLeavesNoHelperOrVolume(t *testing.T) {
	if os.Getenv("SKILLRUNNER_DOCKER_INTEGRATION") != "1" {
		t.Skip("set SKILLRUNNER_DOCKER_INTEGRATION=1")
	}
	sourceVolume := "weknora-skill-test-source-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	runDocker(t, "volume", "create", sourceVolume)
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", sourceVolume).Run() })
	runDocker(t, "run", "--rm", "--user=0:0", "-v", sourceVolume+":/dest", "wechatopenai/weknora-sandbox:latest", "/bin/sh", "-c", "mkdir -p /dest/1/skill/version/scripts && dd if=/dev/zero of=/dest/1/skill/version/scripts/run.py bs=1M count=128")
	version := ResolvedVersion{SourcePath: "/data/skills/1/skill/version", ContentHash: strings.Repeat("a", 64), SourceVolume: sourceVolume, AllowedScripts: []string{"scripts/run.py"}}
	executor := NewExecutor(fixedVersionResolver{version: version}, 10*time.Second)
	request := ExecuteRequest{ExecutionID: "cancel", TenantID: "1", SkillID: "skill", VersionID: "version", ContentHash: version.ContentHash, ScriptPath: "scripts/run.py"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := executor.Execute(ctx, request); err == nil {
		t.Fatal("expected preparation cancellation")
	}
	assertNoRunnerResources(t)
}

func populateSourceVolume(t *testing.T, volume, filePath, content string) {
	t.Helper()
	script := "mkdir -p /dest/$(dirname \"$1\") && printf %s \"$2\" > /dest/\"$1\""
	runDocker(t, "run", "--rm", "--user=0:0", "-v", volume+":/dest", "wechatopenai/weknora-sandbox:latest", "/bin/sh", "-c", script, "sh", filePath, content)
}

func assertNoRunnerResources(t *testing.T) {
	t.Helper()
	for _, args := range [][]string{{"ps", "-aq", "--filter", "label=" + runnerLabel}, {"volume", "ls", "-q", "--filter", "label=" + runnerLabel}} {
		output := runDocker(t, args...)
		if strings.TrimSpace(output) != "" {
			t.Fatalf("runner resources leaked: %s", output)
		}
	}
}

func runDocker(t *testing.T, args ...string) string {
	t.Helper()
	output, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v: %s", args, err, output)
	}
	return string(output)
}

func fixtureHash(files map[string]string) string {
	hash := sha256.New()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		_, _ = hash.Write([]byte(name))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(files[name]))
	}
	return hex.EncodeToString(hash.Sum(nil))
}
