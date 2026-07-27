package skillrunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const sandboxImage = "wechatopenai/weknora-sandbox:latest"
const runnerLabel = "weknora.skill-runner=true"

type VersionResolver interface {
	Resolve(context.Context, ExecuteRequest) (ResolvedVersion, error)
}

type Executor struct {
	resolver VersionResolver
	timeout  time.Duration
}

type executionResources struct {
	token, container, copyContainer, chmodContainer, verifyContainer, skillVolume string
}

func NewExecutor(resolver VersionResolver, timeout time.Duration) *Executor {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Executor{resolver: resolver, timeout: timeout}
}

func newExecutionResources() executionResources {
	token := strings.ReplaceAll(uuid.NewString(), "-", "")
	base := "weknora-skill-" + token
	return executionResources{
		token: token, container: base, copyContainer: base + "-copy",
		chmodContainer: base + "-chmod", verifyContainer: base + "-verify",
		skillVolume: base + "-skill",
	}
}

func BuildContainerSpec(request ExecuteRequest, version ResolvedVersion) ([]string, error) {
	return buildContainerSpec(request, version, newExecutionResources())
}

func buildContainerSpec(request ExecuteRequest, version ResolvedVersion, resources executionResources) ([]string, error) {
	if err := ValidateRequest(request); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(version.SourcePath) || len(version.ContentHash) != 64 || version.SourceVolume == "" {
		return nil, ErrInvalidRequest
	}
	registered := false
	for _, script := range version.AllowedScripts {
		if script == request.ScriptPath {
			registered = true
			break
		}
	}
	if !registered {
		return nil, fmt.Errorf("%w: script is not registered", ErrInvalidRequest)
	}
	interpreter := map[string]string{".py": "python3", ".sh": "/bin/sh", ".js": "node"}[filepath.Ext(request.ScriptPath)]
	args := []string{"run", "--name", resources.container, "--label", runnerLabel, "--network=none", "--read-only",
		"--user=1000:1000", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--memory=256m", "--cpus=0.5", "--pids-limit=64",
		"--tmpfs=/tmp:rw,noexec,nosuid,nodev,size=64m,uid=1000,gid=1000",
		"--tmpfs=/work:rw,noexec,nosuid,nodev,size=64m,uid=1000,gid=1000",
		"-v", resources.skillVolume + ":/skill:ro", "-w", "/work", sandboxImage, interpreter, "/skill/" + request.ScriptPath}
	return append(args, request.Args...), nil
}

func (executor *Executor) Execute(ctx context.Context, request ExecuteRequest) (ExecuteResponse, error) {
	if err := ValidateRequest(request); err != nil {
		return ExecuteResponse{}, err
	}
	version, err := executor.resolver.Resolve(ctx, request)
	if err != nil {
		return ExecuteResponse{}, err
	}
	resources := newExecutionResources()
	if err := prepareExecutionVolume(ctx, request, version, resources); err != nil {
		return ExecuteResponse{}, err
	}
	args, err := buildContainerSpec(request, version, resources)
	if err != nil {
		_ = cleanupResources(resources)
		return ExecuteResponse{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, executor.timeout)
	defer cancel()
	command := exec.Command("docker", args...)
	command.Stdin = strings.NewReader(request.Stdin)
	stdout, stderr := newCappedBuffer(MaxOutputBytes), newCappedBuffer(MaxOutputBytes)
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Start(); err != nil {
		_ = cleanupResources(resources)
		return ExecuteResponse{}, err
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	var runErr error
	killed := false
	select {
	case runErr = <-wait:
	case <-runCtx.Done():
		killed = true
		_ = exec.Command("docker", "kill", resources.container).Run()
		select {
		case runErr = <-wait:
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			runErr = <-wait
		}
	}
	cleanupErr := cleanupResources(resources)
	response := ExecuteResponse{Stdout: stdout.String(), Stderr: stderr.String(), Truncated: stdout.truncated || stderr.truncated, Killed: killed}
	if killed {
		response.ExitCode = -1
		if cleanupErr != nil {
			return response, cleanupErr
		}
		return response, nil
	}
	if runErr == nil {
		if cleanupErr != nil {
			return response, cleanupErr
		}
		return response, nil
	}
	var exitError *exec.ExitError
	if !errors.As(runErr, &exitError) {
		return ExecuteResponse{}, fmt.Errorf("execute docker: %w", runErr)
	}
	response.ExitCode = exitError.ExitCode()
	if cleanupErr != nil {
		return response, cleanupErr
	}
	return response, nil
}

func prepareExecutionVolume(ctx context.Context, request ExecuteRequest, version ResolvedVersion, resources executionResources) error {
	create := exec.CommandContext(ctx, "docker", "volume", "create", "--label", runnerLabel, resources.skillVolume)
	if output, err := create.CombinedOutput(); err != nil {
		return fmt.Errorf("create execution volume: %w: %s", err, output)
	}
	source := "/source/" + request.TenantID + "/" + request.SkillID + "/" + request.VersionID + "/."
	commands := []struct {
		name string
		args []string
	}{
		{resources.copyContainer, []string{"run", "--name", resources.copyContainer, "--label", runnerLabel, "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user=0:0", "-v", version.SourceVolume + ":/source:ro", "-v", resources.skillVolume + ":/dest", sandboxImage, "cp", "-a", source, "/dest/"}},
		{resources.chmodContainer, []string{"run", "--name", resources.chmodContainer, "--label", runnerLabel, "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user=0:0", "-v", resources.skillVolume + ":/dest", sandboxImage, "chmod", "-R", "a=rX", "/dest"}},
	}
	for _, command := range commands {
		if output, err := exec.CommandContext(ctx, "docker", command.args...).CombinedOutput(); err != nil {
			_ = cleanupResources(resources)
			return fmt.Errorf("prepare execution volume: %w: %s", err, output)
		}
		_ = exec.Command("docker", "rm", "-f", command.name).Run()
	}
	if err := verifyExecutionVolume(ctx, version, resources); err != nil {
		_ = cleanupResources(resources)
		return err
	}
	return nil
}

const volumeHashScript = `import hashlib, os, sys
h=hashlib.sha256(); root='/skill'
paths=[]
for base, dirs, files in os.walk(root):
 for name in files:
  p=os.path.join(base,name)
  if os.path.islink(p): sys.exit(3)
  paths.append(os.path.relpath(p,root).replace(os.sep,'/'))
for rel in sorted(paths, key=lambda value:value.encode('utf-8')):
  p=os.path.join(root,*rel.split('/')); h.update(rel.encode()); h.update(b'\0')
  with open(p,'rb') as f:
   for chunk in iter(lambda:f.read(65536),b''): h.update(chunk)
print(h.hexdigest())`

func verifyExecutionVolume(ctx context.Context, version ResolvedVersion, resources executionResources) error {
	args := []string{"run", "--name", resources.verifyContainer, "--label", runnerLabel,
		"--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
		"--user=1000:1000", "-v", resources.skillVolume + ":/skill:ro",
		sandboxImage, "python3", "-c", volumeHashScript}
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	_ = exec.Command("docker", "rm", "-f", resources.verifyContainer).Run()
	if err != nil {
		return fmt.Errorf("verify execution volume: %w: %s", err, output)
	}
	if strings.TrimSpace(string(output)) != version.ContentHash {
		return fmt.Errorf("execution volume content hash mismatch")
	}
	return nil
}

func cleanupResources(resources executionResources) error {
	var failures []string
	for attempt := 0; attempt < 3; attempt++ {
		for _, name := range []string{resources.container, resources.copyContainer, resources.chmodContainer, resources.verifyContainer} {
			_ = exec.Command("docker", "rm", "-f", name).Run()
		}
		if output, err := exec.Command("docker", "volume", "rm", resources.skillVolume).CombinedOutput(); err == nil {
			return nil
		} else {
			failures = append(failures, strings.TrimSpace(string(output)))
			time.Sleep(100 * time.Millisecond)
		}
	}
	return fmt.Errorf("cleanup execution resources: %s", strings.Join(failures, "; "))
}

func CleanupOrphans(ctx context.Context) error {
	for _, command := range [][]string{{"container", "prune", "-f", "--filter", "label=" + runnerLabel}, {"volume", "prune", "-f", "--filter", "label=" + runnerLabel}} {
		if output, err := exec.CommandContext(ctx, "docker", command...).CombinedOutput(); err != nil {
			return fmt.Errorf("cleanup orphans: %w: %s", err, output)
		}
	}
	return nil
}

type cappedBuffer struct {
	buffer    bytes.Buffer
	remaining int
	truncated bool
}

func newCappedBuffer(limit int) *cappedBuffer { return &cappedBuffer{remaining: limit} }
func (writer *cappedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	if len(value) > writer.remaining {
		value = value[:writer.remaining]
		writer.truncated = true
	}
	_, _ = writer.buffer.Write(value)
	writer.remaining -= len(value)
	return original, nil
}
func (writer *cappedBuffer) String() string { return writer.buffer.String() }

var _ io.Writer = (*cappedBuffer)(nil)
