package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DockerSandbox implements the Sandbox interface using Docker containers
type DockerSandbox struct {
	config *Config
}

// NewDockerSandbox creates a new Docker-based sandbox
func NewDockerSandbox(config *Config) *DockerSandbox {
	if config == nil {
		config = DefaultConfig()
	}
	if config.DockerImage == "" {
		config.DockerImage = DefaultDockerImage
	}
	return &DockerSandbox{
		config: config,
	}
}

// Type returns the sandbox type
func (s *DockerSandbox) Type() SandboxType {
	return SandboxTypeDocker
}

// IsAvailable checks if Docker is available
func (s *DockerSandbox) IsAvailable(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "docker", "version")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// Execute runs a script in a Docker container
func (s *DockerSandbox) Execute(ctx context.Context, config *ExecuteConfig) (*ExecuteResult, error) {
	if config == nil {
		return nil, ErrInvalidScript
	}

	// Set default timeout
	timeout := config.Timeout
	if timeout == 0 {
		timeout = s.config.DefaultTimeout
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	// prepare output dir
	var hostOutputDir string
	if config.CollectOutputDir {
		tmpDir, err := os.MkdirTemp("", "weknora-output-*")
		if err != nil {
			return nil, fmt.Errorf("failed to create temp output dir: %w", err)
		}
		hostOutputDir = tmpDir
		if err := os.Chmod(tmpDir, 0o777); err != nil {
			log.Printf("[sandbox] warning: failed to chmod output dir: %v", err)
		}
	}

	// Create context with timeout
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Build docker run command
	args := s.buildDockerArgs(config, hostOutputDir)

	startTime := time.Now()
	cmd := exec.CommandContext(execCtx, "docker", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if config.Stdin != "" {
		cmd.Stdin = strings.NewReader(config.Stdin)
	}

	err := cmd.Run()
	duration := time.Since(startTime)

	result := &ExecuteResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}

	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			result.Killed = true
			result.Error = ErrTimeout.Error()
			result.ExitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.Error = err.Error()
			result.ExitCode = -1
		}
	}

	// collect output files
	if hostOutputDir != "" {
		outputFiles, collectErr := CollectOutputFiles(hostOutputDir, config.OutputFiles)
		if collectErr == nil && len(outputFiles) > 0 {
			result.OutputFiles = outputFiles
		}
		os.RemoveAll(hostOutputDir)
	}

	return result, nil
}

// ExecuteInWorkspace execute in workspace
func (s *DockerSandbox) ExecuteInWorkspace(ctx context.Context, config *WorkspaceExecuteConfig) (*ExecuteResult, error) {
	if config == nil {
		return nil, ErrInvalidScript
	}

	ws, err := CreateWorkspace(config.WorkspaceRoot, config.SkillName)
	if err != nil {
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}
	defer func() {
		if !config.PersistWorkspace {
			CleanupWorkspace(ws)
		}
	}()

	skillRelDir, err := StageSkill(ws, config.SkillSourceDir, config.SkillName)
	if err != nil {
		return nil, fmt.Errorf("failed to stage skill: %w", err)
	}

	outDir := GetWorkspaceOutputDir(ws)
	if err := os.Chmod(outDir, 0o777); err != nil {
		log.Printf("[sandbox] warning: failed to chmod output dir: %v", err)
	}

	timeout := config.Timeout
	if timeout == 0 {
		timeout = s.config.DefaultTimeout
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	containerWorkDir := "/workspace"
	containerSkillDir := containerWorkDir + "/" + skillRelDir

	args := s.buildWorkspaceDockerArgs(config, ws, containerWorkDir, containerSkillDir)

	startTime := time.Now()
	cmd := exec.CommandContext(execCtx, "docker", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if config.Stdin != "" {
		cmd.Stdin = strings.NewReader(config.Stdin)
	}

	runErr := cmd.Run()
	duration := time.Since(startTime)

	result := &ExecuteResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}

	if runErr != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			result.Killed = true
			result.Error = ErrTimeout.Error()
			result.ExitCode = -1
		} else if exitErr, ok := runErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.Error = runErr.Error()
			result.ExitCode = -1
		}
	}

	outputFiles, collectErr := CollectOutputFiles(outDir, config.OutputFiles)
	if collectErr == nil && len(outputFiles) > 0 {
		result.OutputFiles = outputFiles
	}

	return result, nil
}

// buildWorkspaceDockerArgs builds the arguments for the docker run command.
func (s *DockerSandbox) buildWorkspaceDockerArgs(config *WorkspaceExecuteConfig, ws *Workspace, containerWorkDir, containerSkillDir string) []string {
	args := []string{"run", "--rm"}

	// Security settings
	args = append(args, "--user", "1000:1000")
	args = append(args, "--cap-drop", "ALL")
	args = append(args, "--pids-limit", "100")
	args = append(args, "--security-opt", "no-new-privileges")

	memLimit := config.MemoryLimit
	if memLimit == 0 {
		memLimit = s.config.MaxMemory
	}
	if memLimit > 0 {
		args = append(args, "--memory", fmt.Sprintf("%d", memLimit))
		args = append(args, "--memory-swap", fmt.Sprintf("%d", memLimit))
	}

	cpuLimit := config.CPULimit
	if cpuLimit == 0 {
		cpuLimit = s.config.MaxCPU
	}
	if cpuLimit > 0 {
		args = append(args, "--cpus", fmt.Sprintf("%.2f", cpuLimit))
	}

	if !config.AllowNetwork {
		args = append(args, "--network", "none")
	}

	skillsHostDir := filepath.Join(ws.Path, DirSkills)
	outHostDir := filepath.Join(ws.Path, DirOut)
	workHostDir := filepath.Join(ws.Path, DirWork)
	runsHostDir := filepath.Join(ws.Path, DirRuns)

	args = append(args, "-v", fmt.Sprintf("%s:%s/%s:ro", skillsHostDir, containerWorkDir, DirSkills))
	args = append(args, "-v", fmt.Sprintf("%s:%s/%s:rw", outHostDir, containerWorkDir, DirOut))
	args = append(args, "-v", fmt.Sprintf("%s:%s/%s:rw", workHostDir, containerWorkDir, DirWork))
	args = append(args, "-v", fmt.Sprintf("%s:%s/%s:rw", runsHostDir, containerWorkDir, DirRuns))

	args = append(args, "--tmpfs", "/tmp:rw,noexec,nosuid,size=128m")

	wsEnv := BuildDockerWorkspaceEnv(containerWorkDir, config.SkillName)
	for k, v := range wsEnv {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	for k, v := range config.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	args = append(args, "-w", containerSkillDir)

	args = append(args, s.config.DockerImage)

	if config.Command != "" {
		args = append(args, "bash", "-c", config.Command)
	} else if config.Script != "" {
		scriptName := config.Script
		interpreter := getInterpreter(filepath.Base(scriptName))
		args = append(args, interpreter, scriptName)
		args = append(args, config.Args...)
	}

	return args
}

// buildDockerArgs constructs the docker run command arguments
func (s *DockerSandbox) buildDockerArgs(config *ExecuteConfig, hostOutputDir string) []string {
	args := []string{"run", "--rm"}

	// Security: run as non-root user
	args = append(args, "--user", "1000:1000")

	// Security: drop all capabilities
	args = append(args, "--cap-drop", "ALL")

	// Security: read-only root filesystem (optional)
	if config.ReadOnlyRootfs {
		args = append(args, "--read-only")
		// Add writable tmp directory
		args = append(args, "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m")
	}

	// Resource limits
	memLimit := config.MemoryLimit
	if memLimit == 0 {
		memLimit = s.config.MaxMemory
	}
	if memLimit > 0 {
		args = append(args, "--memory", fmt.Sprintf("%d", memLimit))
		args = append(args, "--memory-swap", fmt.Sprintf("%d", memLimit)) // Disable swap
	}

	cpuLimit := config.CPULimit
	if cpuLimit == 0 {
		cpuLimit = s.config.MaxCPU
	}
	if cpuLimit > 0 {
		args = append(args, "--cpus", fmt.Sprintf("%.2f", cpuLimit))
	}

	// Network isolation
	if !config.AllowNetwork {
		args = append(args, "--network", "none")
	}

	// Security: disable privileged mode and limit PIDs
	args = append(args, "--pids-limit", "100")
	args = append(args, "--security-opt", "no-new-privileges")

	// Mount the script and working directory as read-only
	scriptDir := filepath.Dir(config.Script)
	args = append(args, "-v", fmt.Sprintf("%s:/workspace:ro", scriptDir))

	// 挂载可写的 output 目录到容器内的 /workspace/out
	if hostOutputDir != "" {
		args = append(args, "-v", fmt.Sprintf("%s:/workspace/%s:rw", hostOutputDir, OutputDirName))
		// 注入 OUTPUT_DIR 环境变量
		args = append(args, "-e", fmt.Sprintf("%s=/workspace/%s", OutputDirEnvVar, OutputDirName))
	}

	// Working directory
	args = append(args, "-w", "/workspace")

	// Environment variables
	for key, value := range config.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", key, value))
	}

	// Image
	args = append(args, s.config.DockerImage)

	// Script execution command
	scriptName := filepath.Base(config.Script)
	interpreter := getInterpreter(scriptName)

	args = append(args, interpreter, scriptName)
	args = append(args, config.Args...)

	return args
}

// getInterpreter returns the appropriate interpreter for a script
func getInterpreter(scriptName string) string {
	ext := strings.ToLower(filepath.Ext(scriptName))
	switch ext {
	case ".py":
		return "python3"
	case ".sh", ".bash":
		return "bash"
	case ".js":
		return "node"
	case ".rb":
		return "ruby"
	case ".pl":
		return "perl"
	default:
		return "sh"
	}
}

// ImageExists checks if the configured Docker image exists locally
func (s *DockerSandbox) ImageExists(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", s.config.DockerImage)
	return cmd.Run() == nil
}

// EnsureImage pulls the Docker image if it doesn't exist locally.
// This is intended to be called during initialization so the image is
// ready before the first script execution.
func (s *DockerSandbox) EnsureImage(ctx context.Context) error {
	if s.ImageExists(ctx) {
		return nil
	}
	cmd := exec.CommandContext(ctx, "docker", "pull", s.config.DockerImage)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to pull image %s: %w (%s)", s.config.DockerImage, err, stderr.String())
	}
	return nil
}

// Cleanup removes any lingering resources
func (s *DockerSandbox) Cleanup(ctx context.Context) error {
	// Docker --rm flag should handle container cleanup
	// This is here for any additional cleanup if needed
	return nil
}
