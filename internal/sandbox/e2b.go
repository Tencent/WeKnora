package sandbox

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	ci "github.com/Tencent/WeKnora/internal/sandbox/internal/codeinterpreter"
)

// Sentinels used to frame run output so we can parse stdout/stderr/exit code
// from the streaming bash output reliably.
const (
	e2bSentinelStdoutBegin = "__E2B_STDOUT_BEGIN__"
	e2bSentinelStdoutEnd   = "__E2B_STDOUT_END__"
	e2bSentinelStderrBegin = "__E2B_STDERR_BEGIN__"
	e2bSentinelStderrEnd   = "__E2B_STDERR_END__"
	e2bSentinelExitPrefix  = "__E2B_EXITCODE__="

	// defaultE2BRunBase is the base directory inside the E2B sandbox where
	// per-execution workspaces are created. /tmp is writable in the
	// default template.
	defaultE2BRunBase = "/tmp/run"

	// defaultE2BTemplate is the default E2B template alias.
	defaultE2BTemplate = "code-interpreter-v1"

	// defaultE2BSandboxTimeout matches the e2b defaults (5 minutes).
	defaultE2BSandboxTimeout = 5 * time.Minute

	// maxE2BReadBytes is the maximum amount of bytes read back from the
	// sandbox for a single file during artifact collection.
	maxE2BReadBytes = 4 * 1024 * 1024

	// helper bash timeouts
	defaultE2BBashTimeout    = 30 * time.Second
	defaultE2BStageTimeout   = 60 * time.Second
	defaultE2BCollectTimeout = 30 * time.Second
)

// E2BSandbox implements the Sandbox interface using the E2B cloud sandbox
// service.  It spins up a single long-lived sandbox per E2BSandbox instance
// and runs every requested script inside that sandbox.
type E2BSandbox struct {
	config *Config

	mu      sync.Mutex
	sbx     *ci.Sandbox
	initErr error
}

// NewE2BSandbox creates a new E2B-backed sandbox. The sandbox is created lazily
// on the first Execute call so construction itself cannot fail for lack of an
// API key.
func NewE2BSandbox(config *Config) *E2BSandbox {
	if config == nil {
		config = DefaultConfig()
	}
	return &E2BSandbox{config: config}
}

// Type returns the sandbox type.
func (s *E2BSandbox) Type() SandboxType {
	return SandboxTypeE2B
}

// IsAvailable returns true when an E2B API key is configured (either via the
// sandbox config or the E2B_API_KEY environment variable).
func (s *E2BSandbox) IsAvailable(ctx context.Context) bool {
	if s.config.E2BAPIKey != "" {
		return true
	}
	return os.Getenv("E2B_API_KEY") != ""
}

// Cleanup terminates the underlying E2B sandbox if one has been created.
func (s *E2BSandbox) Cleanup(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sbx == nil {
		return nil
	}
	err := s.sbx.Kill(ctx)
	s.sbx = nil
	return err
}

// ensureSandbox lazily creates the underlying *ci.Sandbox.
func (s *E2BSandbox) ensureSandbox(ctx context.Context) (*ci.Sandbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sbx != nil {
		running, err := s.sbx.IsRunning(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to check if e2b sandbox is running: %w", err)
		}
		if running {
			return s.sbx, nil
		}
	}
	if s.initErr != nil {
		return nil, s.initErr
	}

	template := s.config.E2BTemplate
	if template == "" {
		template = defaultE2BTemplate
	}
	lifetime := s.config.E2BSandboxTimeout
	if lifetime <= 0 {
		lifetime = defaultE2BSandboxTimeout
	}

	sbx, err := ci.Create(ctx, &ci.SandboxOpts{
		APIKey:   s.config.E2BAPIKey,
		Domain:   s.config.E2BDomain,
		Template: template,
		Timeout:  lifetime,
	})
	if err != nil {
		s.initErr = fmt.Errorf("failed to create e2b sandbox: %w", err)
		return nil, s.initErr
	}
	s.sbx = sbx
	return sbx, nil
}

// Execute runs a single script inside the E2B sandbox.
func (s *E2BSandbox) Execute(ctx context.Context, config *ExecuteConfig) (*ExecuteResult, error) {
	if config == nil {
		return nil, ErrInvalidScript
	}
	if config.Script == "" {
		return nil, ErrInvalidScript
	}

	timeout := config.Timeout
	if timeout == 0 {
		timeout = s.config.DefaultTimeout
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	sbx, err := s.ensureSandbox(ctx)
	if err != nil {
		return nil, err
	}

	// Read script content locally; E2B runs entirely in the cloud so we
	// need to ship the script bytes (plus any auxiliary files under the
	// same directory) into the sandbox before running.
	scriptPath := config.Script
	scriptBytes, err := os.ReadFile(scriptPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrScriptNotFound
		}
		return nil, fmt.Errorf("failed to read script: %w", err)
	}

	// Create a fresh per-execution workspace inside the sandbox.
	wsPath, err := s.createRunDir(ctx, sbx, "exec")
	if err != nil {
		return nil, err
	}
	defer s.removeRunDir(context.Background(), sbx, wsPath)

	// Upload the script.
	scriptName := filepath.Base(scriptPath)
	if err := s.putFiles(ctx, sbx, wsPath, []putFile{{
		Path:    scriptName,
		Content: scriptBytes,
		Mode:    0o755,
	}}); err != nil {
		return nil, err
	}

	// Prepare optional output directory.
	outDir := ""
	if config.CollectOutputDir {
		outDir = path.Join(wsPath, OutputDirName)
		if _, _, _, err := s.runBash(ctx, sbx,
			"mkdir -p "+shellSingleQuote(outDir), defaultE2BStageTimeout); err != nil {
			return nil, err
		}
	}

	interpreter := getInterpreter(scriptName)

	// Build environment variables.
	env := map[string]string{}
	for k, v := range config.Env {
		env[k] = v
	}
	if outDir != "" {
		env[OutputDirEnvVar] = outDir
	}

	stdout, stderr, exit, dur, timedOut, runErr := s.runProgram(ctx, sbx, runProgramReq{
		Cwd:     wsPath,
		Cmd:     interpreter,
		Args:    append([]string{scriptName}, config.Args...),
		Env:     env,
		Stdin:   config.Stdin,
		Timeout: timeout,
	})

	result := &ExecuteResult{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exit,
		Duration: dur,
	}
	if timedOut {
		result.Killed = true
		result.Error = ErrTimeout.Error()
		if result.ExitCode == 0 {
			result.ExitCode = -1
		}
	} else if runErr != nil {
		result.Error = runErr.Error()
		if result.ExitCode == 0 {
			result.ExitCode = -1
		}
	}

	// Collect output files from the sandbox's out dir.
	if outDir != "" {
		log.Printf("[sandbox][e2b] Execute: start collecting output files from %s (patterns=%v)",
			outDir, config.OutputFiles)
		files, collectErr := s.collectOutputFiles(ctx, sbx, outDir, config.OutputFiles)
		if collectErr != nil {
			log.Printf("[sandbox][e2b] Execute: collectOutputFiles error: %v", collectErr)
			if result.Stderr != "" && !strings.HasSuffix(result.Stderr, "\n") {
				result.Stderr += "\n"
			}
			result.Stderr += fmt.Sprintf("[sandbox][e2b] collectOutputFiles error: %v\n", collectErr)
		}
		if len(files) > 0 {
			result.OutputFiles = files
		}
		log.Printf("[sandbox][e2b] Execute: collected %d output file(s) from %s",
			len(files), outDir)
	}

	return result, nil
}

// ExecuteInWorkspace runs a script in a per-execution sandbox workspace that
// mirrors the host workspace layout (skills/, work/, runs/, out/).
func (s *E2BSandbox) ExecuteInWorkspace(ctx context.Context, config *WorkspaceExecuteConfig) (*ExecuteResult, error) {
	if config == nil {
		return nil, ErrInvalidScript
	}
	if config.Command == "" && config.Script == "" {
		return nil, errors.New("either command or script must be specified")
	}

	timeout := config.Timeout
	if timeout == 0 {
		timeout = s.config.DefaultTimeout
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	sbx, err := s.ensureSandbox(ctx)
	if err != nil {
		return nil, err
	}

	// Create sandbox-side workspace with the standard layout.
	wsPath, err := s.createRunDir(ctx, sbx, config.SkillName)
	if err != nil {
		return nil, err
	}
	if !config.PersistWorkspace {
		defer s.removeRunDir(context.Background(), sbx, wsPath)
	}

	if err := s.ensureWorkspaceLayout(ctx, sbx, wsPath); err != nil {
		return nil, err
	}

	// Stage skill directory.
	skillRel := path.Join(DirSkills, config.SkillName)
	skillDest := path.Join(wsPath, skillRel)
	if config.SkillSourceDir != "" {
		if err := s.uploadDir(ctx, sbx, config.SkillSourceDir, skillDest); err != nil {
			return nil, fmt.Errorf("failed to stage skill: %w", err)
		}
	}

	// Working directory resolution.
	cwd := skillDest
	if config.Cwd != "" {
		if strings.HasPrefix(config.Cwd, "/") {
			cwd = config.Cwd
		} else {
			cwd = path.Join(skillDest, filepath.ToSlash(config.Cwd))
		}
	}
	if _, _, _, err := s.runBash(ctx, sbx,
		"mkdir -p "+shellSingleQuote(cwd), defaultE2BStageTimeout); err != nil {
		return nil, err
	}

	// Workspace environment.
	env := map[string]string{
		EnvWorkspaceDir: wsPath,
		EnvSkillsDir:    path.Join(wsPath, DirSkills),
		EnvWorkDir:      path.Join(wsPath, DirWork),
		OutputDirEnvVar: path.Join(wsPath, DirOut),
		EnvRunDir: path.Join(wsPath, DirRuns,
			"run_"+time.Now().Format("20060102T150405.000")),
	}
	if config.SkillName != "" {
		env[EnvSkillName] = config.SkillName
	}
	for k, v := range config.Env {
		env[k] = v
	}

	var req runProgramReq
	req.Cwd = cwd
	req.Env = env
	req.Stdin = config.Stdin
	req.Timeout = timeout

	if config.Command != "" {
		req.Cmd = "bash"
		req.Args = []string{"-c", config.Command}
	} else {
		scriptPath := config.Script
		if !strings.HasPrefix(scriptPath, "/") {
			scriptPath = path.Join(skillDest, filepath.ToSlash(scriptPath))
		}
		interpreter := getInterpreter(path.Base(scriptPath))
		req.Cmd = interpreter
		req.Args = append([]string{scriptPath}, config.Args...)
	}

	stdout, stderr, exit, dur, timedOut, runErr := s.runProgram(ctx, sbx, req)

	result := &ExecuteResult{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exit,
		Duration: dur,
	}
	if timedOut {
		result.Killed = true
		result.Error = ErrTimeout.Error()
		if result.ExitCode == 0 {
			result.ExitCode = -1
		}
	} else if runErr != nil {
		result.Error = runErr.Error()
		if result.ExitCode == 0 {
			result.ExitCode = -1
		}
	}

	outDir := path.Join(wsPath, DirOut)
	log.Printf("[sandbox][e2b] ExecuteInWorkspace: start collecting output files from %s (patterns=%v)",
		outDir, config.OutputFiles)
	files, collectErr := s.collectOutputFiles(ctx, sbx, outDir, config.OutputFiles)
	if collectErr != nil {
		log.Printf("[sandbox][e2b] ExecuteInWorkspace: collectOutputFiles error: %v", collectErr)
		// Surface collection error through Stderr so caller can diagnose,
		// but don't fail the whole execution if the program itself succeeded.
		if result.Stderr != "" && !strings.HasSuffix(result.Stderr, "\n") {
			result.Stderr += "\n"
		}
		result.Stderr += fmt.Sprintf("[sandbox][e2b] collectOutputFiles error: %v\n", collectErr)
	}
	if len(files) > 0 {
		result.OutputFiles = files
	}
	log.Printf("[sandbox][e2b] ExecuteInWorkspace: collected %d output file(s) from %s",
		len(files), outDir)

	return result, nil
}

type runProgramReq struct {
	Cwd     string
	Cmd     string
	Args    []string
	Env     map[string]string
	Stdin   string
	Timeout time.Duration
}

// runProgram executes a program with framed stdout/stderr/exit-code so callers
// receive the user's output untouched.
func (s *E2BSandbox) runProgram(
	ctx context.Context, sbx *ci.Sandbox, req runProgramReq,
) (stdout, stderr string, exit int, dur time.Duration, timedOut bool, err error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	var envParts []string
	for k, v := range req.Env {
		envParts = append(envParts, k+"="+shellSingleQuote(v))
	}

	var args strings.Builder
	for _, a := range req.Args {
		args.WriteByte(' ')
		args.WriteString(shellSingleQuote(a))
	}

	var stdinRedir string
	if req.Stdin != "" {
		b64 := base64.StdEncoding.EncodeToString([]byte(req.Stdin))
		stdinRedir = " < <(printf %s " + shellSingleQuote(b64) + " | base64 -d)"
	}

	envAssign := ""
	if len(envParts) > 0 {
		envAssign = "env " + strings.Join(envParts, " ") + " "
	}

	cwdPart := ""
	if req.Cwd != "" {
		cwdPart = "cd " + shellSingleQuote(req.Cwd) + " && "
	}

	inner := fmt.Sprintf("%s%s%s%s%s",
		cwdPart, envAssign,
		shellSingleQuote(req.Cmd), args.String(), stdinRedir,
	)
	script := e2bBuildRunWrapper(inner)

	start := time.Now()
	rawOut, rawErr, _, runErr := s.runBash(ctx, sbx, script, timeout)
	dur = time.Since(start)

	stdout, stderr, exit = e2bParseFramedOutput(rawOut, rawErr)

	if runErr != nil {
		if isE2BTimeoutErr(runErr) {
			timedOut = true
		} else {
			err = runErr
		}
	}
	return
}

// runBash runs a bash snippet inside the sandbox using LanguageBash.
func (s *E2BSandbox) runBash(
	ctx context.Context, sbx *ci.Sandbox, script string, timeout time.Duration,
) (string, string, int, error) {
	if sbx == nil {
		return "", "", 0, errors.New("e2b: sandbox not initialized")
	}
	if timeout <= 0 {
		timeout = defaultE2BBashTimeout
	}
	var stdoutB, stderrB strings.Builder
	exec, err := sbx.RunCode(ctx, script, &ci.RunCodeOpts{
		Language: ci.LanguageBash,
		Timeout:  timeout,
		OnStdout: func(m ci.OutputMessage) { stdoutB.WriteString(m.Line) },
		OnStderr: func(m ci.OutputMessage) { stderrB.WriteString(m.Line) },
	})
	if err != nil {
		return stdoutB.String(), stderrB.String(), -1, err
	}
	if exec.Error != nil {
		return stdoutB.String(), stderrB.String(), -1, fmt.Errorf(
			"bash error: %s: %s", exec.Error.Name, exec.Error.Value,
		)
	}
	return stdoutB.String(), stderrB.String(), 0, nil
}

// createRunDir creates a fresh per-execution directory under the configured
// sandbox run base.
func (s *E2BSandbox) createRunDir(
	ctx context.Context, sbx *ci.Sandbox, tag string,
) (string, error) {
	safe := sanitizeForPath(tag)
	if safe == "" {
		safe = "ws"
	}
	suf := time.Now().UnixNano()
	wsPath := path.Join(defaultE2BRunBase, fmt.Sprintf("ws_%s_%d", safe, suf))
	script := "set -e; mkdir -p " + shellSingleQuote(wsPath)
	if _, _, _, err := s.runBash(ctx, sbx, script, defaultE2BStageTimeout); err != nil {
		return "", fmt.Errorf("failed to create e2b workspace: %w", err)
	}
	return wsPath, nil
}

func (s *E2BSandbox) removeRunDir(ctx context.Context, sbx *ci.Sandbox, wsPath string) {
	if wsPath == "" || sbx == nil {
		return
	}
	script := "rm -rf " + shellSingleQuote(wsPath)
	_, _, _, _ = s.runBash(ctx, sbx, script, defaultE2BStageTimeout)
}

// ensureWorkspaceLayout creates the skills/work/runs/out subdirectories.
func (s *E2BSandbox) ensureWorkspaceLayout(
	ctx context.Context, sbx *ci.Sandbox, wsPath string,
) error {
	var sb strings.Builder
	sb.WriteString("set -e; mkdir -p ")
	for _, d := range []string{
		path.Join(wsPath, DirSkills),
		path.Join(wsPath, DirWork),
		path.Join(wsPath, DirRuns),
		path.Join(wsPath, DirOut),
	} {
		sb.WriteString(shellSingleQuote(d))
		sb.WriteByte(' ')
	}
	_, _, _, err := s.runBash(ctx, sbx, sb.String(), defaultE2BStageTimeout)
	return err
}

// putFile describes a single in-memory file to upload to the sandbox.
type putFile struct {
	Path    string // relative path inside dest
	Content []byte
	Mode    os.FileMode
}

// putFiles uploads small files to the sandbox by packing them into a tar.gz
// and extracting on the remote side.
func (s *E2BSandbox) putFiles(
	ctx context.Context, sbx *ci.Sandbox, dest string, files []putFile,
) error {
	if len(files) == 0 {
		return nil
	}
	data, err := tarGzFromPutFiles(files)
	if err != nil {
		return err
	}
	return s.uploadTarGzAndExtract(ctx, sbx, dest, data)
}

// uploadDir packs a local directory into tar.gz then extracts it into dest
// inside the sandbox.
func (s *E2BSandbox) uploadDir(
	ctx context.Context, sbx *ci.Sandbox, hostPath, dest string,
) error {
	if strings.TrimSpace(hostPath) == "" {
		return errors.New("hostPath is empty")
	}
	abs, err := filepath.Abs(hostPath)
	if err != nil {
		return err
	}
	data, err := tarGzFromDir(abs)
	if err != nil {
		return err
	}
	return s.uploadTarGzAndExtract(ctx, sbx, dest, data)
}

// uploadTarGzAndExtract ships a tar.gz archive into the sandbox via base64
// over stdin and extracts it into dest.
func (s *E2BSandbox) uploadTarGzAndExtract(
	ctx context.Context, sbx *ci.Sandbox, dest string, data []byte,
) error {
	b64 := base64.StdEncoding.EncodeToString(data)
	var script strings.Builder
	script.WriteString("set -e; mkdir -p ")
	script.WriteString(shellSingleQuote(dest))
	script.WriteString("; printf %s ")
	script.WriteString(shellSingleQuote(b64))
	script.WriteString(" | base64 -d | tar -xzf - -C ")
	script.WriteString(shellSingleQuote(dest))
	_, _, _, err := s.runBash(ctx, sbx, script.String(), defaultE2BStageTimeout)
	return err
}

// collectOutputFiles lists files under outDir that match patterns and reads
// their contents back to the host.
func (s *E2BSandbox) collectOutputFiles(
	ctx context.Context, sbx *ci.Sandbox, outDir string, patterns []string,
) ([]OutputFile, error) {
	log.Printf("[sandbox][e2b] collectOutputFiles start: outDir=%s patterns=%v",
		outDir, patterns)

	// First, check if the directory exists. If not, return nil.
	exists, err := s.pathExists(ctx, sbx, outDir)
	if err != nil {
		log.Printf("[sandbox][e2b] collectOutputFiles: pathExists error for %s: %v",
			outDir, err)
		return nil, err
	}
	if !exists {
		log.Printf("[sandbox][e2b] collectOutputFiles: outDir %s does not exist, skip",
			outDir)
		return nil, nil
	}

	paths, err := s.listFiles(ctx, sbx, outDir, patterns)
	if err != nil {
		log.Printf("[sandbox][e2b] collectOutputFiles: listFiles error in %s: %v",
			outDir, err)
		return nil, err
	}
	log.Printf("[sandbox][e2b] collectOutputFiles: listFiles found %d file(s) in %s",
		len(paths), outDir)

	var (
		out       []OutputFile
		totalSize int64
		skipped   int
	)
	for _, full := range paths {
		if len(out) >= MaxOutputFiles {
			log.Printf("[sandbox][e2b] collectOutputFiles: reached MaxOutputFiles=%d, stop collecting",
				MaxOutputFiles)
			break
		}
		data, size, err := s.readFile(ctx, sbx, full, MaxOutputFileSize)
		if err != nil {
			log.Printf("[sandbox][e2b] collectOutputFiles: readFile failed for %s: %v",
				full, err)
			skipped++
			continue
		}
		if size > MaxOutputFileSize {
			log.Printf("[sandbox][e2b] collectOutputFiles: skip %s, size=%d exceeds MaxOutputFileSize=%d",
				full, size, MaxOutputFileSize)
			skipped++
			continue
		}
		if totalSize+size > MaxTotalOutputSize {
			log.Printf("[sandbox][e2b] collectOutputFiles: totalSize=%d + size=%d would exceed MaxTotalOutputSize=%d, stop collecting (collected=%d, remaining=%d)",
				totalSize, size, MaxTotalOutputSize, len(out), len(paths)-len(out)-skipped)
			break
		}
		totalSize += size

		rel := strings.TrimPrefix(full, outDir+"/")
		if rel == full {
			rel = path.Base(full)
		}
		mime := detectMIMEType(rel, data)
		isText := isTextMIME(mime)
		of := OutputFile{
			Name:      rel,
			Data:      data,
			MIMEType:  mime,
			SizeBytes: size,
			IsText:    isText,
		}
		if isText && int64(len(data)) <= int64(MaxInlineTextSize) {
			of.Content = string(data)
		}
		log.Printf("[sandbox][e2b] collectOutputFiles: collected file name=%s size=%d mime=%s isText=%v",
			rel, size, mime, isText)
		out = append(out, of)
	}
	log.Printf("[sandbox][e2b] collectOutputFiles done: outDir=%s collected=%d skipped=%d totalSize=%d",
		outDir, len(out), skipped, totalSize)
	return out, nil
}

// pathExists checks whether the given path exists inside the sandbox.
func (s *E2BSandbox) pathExists(
	ctx context.Context, sbx *ci.Sandbox, p string,
) (bool, error) {
	script := "if [ -e " + shellSingleQuote(p) + " ]; then echo __E2B_YES__; else echo __E2B_NO__; fi"
	stdout, stderr, _, err := s.runBash(ctx, sbx, script, defaultE2BBashTimeout)
	if err != nil {
		log.Printf("[sandbox][e2b] pathExists: bash error for %s: %v stdout=%q stderr=%q",
			p, err, stdout, stderr)
		return false, err
	}
	exists := strings.Contains(stdout, "__E2B_YES__")
	log.Printf("[sandbox][e2b] pathExists: path=%s exists=%v (stdout=%q stderr=%q)",
		p, exists, strings.TrimSpace(stdout), strings.TrimSpace(stderr))
	return exists, nil
}

// listFiles returns absolute paths of files under wsPath matching patterns.
// If patterns is empty, it lists all files recursively.
func (s *E2BSandbox) listFiles(
	ctx context.Context, sbx *ci.Sandbox, wsPath string, patterns []string,
) ([]string, error) {
	var cmd strings.Builder
	// Use `cd ... && shopt ... && ...` so any early failure is reported in
	// stderr and we don't silently scan a wrong directory.
	cmd.WriteString("cd ")
	cmd.WriteString(shellSingleQuote(wsPath))
	cmd.WriteString(" && shopt -s globstar nullglob dotglob && ")
	if len(patterns) == 0 {
		// Use absolute path via `find <abs>` so the printed path is always
		// the absolute path, regardless of cwd resolution quirks.
		cmd.WriteString("find ")
		cmd.WriteString(shellSingleQuote(wsPath))
		cmd.WriteString(" -type f -print")
	} else {
		cmd.WriteString("for p in")
		for _, p := range patterns {
			cmd.WriteByte(' ')
			cmd.WriteString(shellSingleQuote(filepath.ToSlash(p)))
		}
		cmd.WriteString("; do for f in $p; do ")
		cmd.WriteString(`if [ -f "$f" ]; then printf '%s\n' "$(pwd)/$f"; fi; `)
		cmd.WriteString("done; done")
	}

	log.Printf("[sandbox][e2b] listFiles: wsPath=%s patterns=%v script=%s",
		wsPath, patterns, cmd.String())
	stdout, stderr, _, err := s.runBash(ctx, sbx, cmd.String(), defaultE2BCollectTimeout)
	if err != nil {
		log.Printf("[sandbox][e2b] listFiles: bash error in %s: %v stdout=%q stderr=%q",
			wsPath, err, stdout, stderr)
		return nil, err
	}
	log.Printf("[sandbox][e2b] listFiles: raw stdout (%d bytes)=%q stderr=%q",
		len(stdout), stdout, strings.TrimSpace(stderr))

	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		clean := path.Clean(line)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		out = append(out, clean)
	}
	log.Printf("[sandbox][e2b] listFiles: parsed %d unique file path(s) from %s",
		len(out), wsPath)
	return out, nil
}

// readFile reads up to limit bytes from the sandbox file at full.
func (s *E2BSandbox) readFile(
	ctx context.Context, sbx *ci.Sandbox, full string, limit int64,
) ([]byte, int64, error) {
	if limit <= 0 {
		limit = maxE2BReadBytes
	}
	var script strings.Builder
	script.WriteString("set -e; F=")
	script.WriteString(shellSingleQuote(full))
	script.WriteString(`; SZ=$(stat -c%s "$F" 2>/dev/null || wc -c < "$F"); `)
	script.WriteString("echo __E2B_SIZE__=$SZ; ")
	script.WriteString("echo __E2B_B64_BEGIN__; ")
	script.WriteString(fmt.Sprintf(`head -c %d "$F" | base64; `, limit))
	script.WriteString("echo __E2B_B64_END__")
	stdout, stderr, _, err := s.runBash(ctx, sbx, script.String(), defaultE2BCollectTimeout)
	if err != nil {
		log.Printf("[sandbox][e2b] readFile: bash error for %s: %v stderr=%q",
			full, err, strings.TrimSpace(stderr))
		return nil, 0, err
	}
	var size int64
	if idx := strings.Index(stdout, "__E2B_SIZE__="); idx >= 0 {
		rest := stdout[idx+len("__E2B_SIZE__="):]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[:nl]
		}
		rest = strings.TrimSpace(rest)
		if v, err := strconv.ParseInt(rest, 10, 64); err == nil {
			size = v
		}
	}
	b64 := e2bExtractBetween(stdout, "__E2B_B64_BEGIN__", "__E2B_B64_END__")
	b64 = strings.ReplaceAll(b64, "\n", "")
	b64 = strings.ReplaceAll(b64, "\r", "")
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, 0, fmt.Errorf("decode base64: %w", err)
	}
	if size == 0 {
		size = int64(len(data))
	}
	return data, size, nil
}

func e2bBuildRunWrapper(inner string) string {
	var b strings.Builder
	b.WriteString("__ERR=$(mktemp); ")
	b.WriteString("echo " + e2bSentinelStdoutBegin + "; ")
	b.WriteString("{ ")
	b.WriteString(inner)
	b.WriteString(`; } 2>"$__ERR"; __EC=$?; `)
	b.WriteString("echo " + e2bSentinelStdoutEnd + "; ")
	b.WriteString("echo " + e2bSentinelExitPrefix + "$__EC; ")
	b.WriteString("echo " + e2bSentinelStderrBegin + " >&2; ")
	b.WriteString(`cat "$__ERR" >&2; `)
	b.WriteString("echo " + e2bSentinelStderrEnd + " >&2; ")
	b.WriteString(`rm -f "$__ERR"`)
	return b.String()
}

func e2bParseFramedOutput(rawStdout, rawStderr string) (string, string, int) {
	stdout := e2bExtractBetween(rawStdout, e2bSentinelStdoutBegin, e2bSentinelStdoutEnd)
	stderr := e2bExtractBetween(rawStderr, e2bSentinelStderrBegin, e2bSentinelStderrEnd)
	exit := 0
	if idx := strings.LastIndex(rawStdout, e2bSentinelExitPrefix); idx >= 0 {
		rest := rawStdout[idx+len(e2bSentinelExitPrefix):]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[:nl]
		}
		rest = strings.TrimSpace(rest)
		if v, err := strconv.Atoi(rest); err == nil {
			exit = v
		}
	}
	return stdout, stderr, exit
}

func e2bExtractBetween(s, begin, end string) string {
	b := strings.Index(s, begin)
	if b < 0 {
		return ""
	}
	start := b + len(begin)
	if start < len(s) && s[start] == '\n' {
		start++
	}
	rest := s[start:]
	e := strings.Index(rest, end)
	if e < 0 {
		return rest
	}
	return strings.TrimRight(rest[:e], "\n")
}

func isE2BTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout")
}

func tarGzFromPutFiles(files []putFile) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	now := time.Now()
	seen := map[string]bool{}
	for _, f := range files {
		clean := path.Clean(filepath.ToSlash(f.Path))
		if clean == "." || clean == "/" || clean == "" {
			return nil, fmt.Errorf("invalid file path: %s", f.Path)
		}
		if dir := path.Dir(clean); dir != "." && dir != "/" {
			parts := strings.Split(dir, "/")
			cur := ""
			for _, p := range parts {
				if p == "" {
					continue
				}
				if cur == "" {
					cur = p
				} else {
					cur = cur + "/" + p
				}
				if seen[cur] {
					continue
				}
				seen[cur] = true
				if err := tw.WriteHeader(&tar.Header{
					Name:     cur + "/",
					Mode:     0o755,
					ModTime:  now,
					Typeflag: tar.TypeDir,
				}); err != nil {
					return nil, err
				}
			}
		}
		mode := int64(f.Mode)
		if mode == 0 {
			mode = 0o644
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:    clean,
			Mode:    mode,
			Size:    int64(len(f.Content)),
			ModTime: now,
		}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.Content); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func tarGzFromDir(root string) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return tw.WriteHeader(&tar.Header{
				Name:     rel + "/",
				Mode:     int64(info.Mode().Perm()),
				ModTime:  info.ModTime(),
				Typeflag: tar.TypeDir,
			})
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:    rel,
			Mode:    int64(info.Mode().Perm()),
			Size:    int64(len(data)),
			ModTime: info.ModTime(),
		}); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// shellSingleQuote safely quotes a string for inclusion in a POSIX shell command.
func shellSingleQuote(s string) string {
	if s == "" {
		return "''"
	}
	q := strings.ReplaceAll(s, "'", "'\\''")
	return "'" + q + "'"
}
