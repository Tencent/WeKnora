// Package tools — shell_exec.
//
// General shell command execution primitive for the agent. The LLM can freely
// explore and operate inside its session-scoped Cube MicroVM: inspect files,
// transform data, run programs, install dependencies, and prepare or verify
// skill outputs.
//
// Design notes:
//
//   - Cube-only capability: registration is feature-gated on the sandbox
//     backend exposing SandboxCommandExecutor. Docker / Local backends do
//     NOT satisfy the interface, preserving their existing stateless
//     security model. shell_exec never runs on the WeKnora host.
//   - Session-scoped: the sandbox is resolved from ToolExecContext.SessionID
//     so the LLM cannot execute against a foreign session, and installed
//     dependencies persist across subsequent tool calls in the same session.
//   - Non-zero exit is a normal signal, not an error: pip install failures,
//     missing binaries, etc. are all valid results the LLM must inspect.
//     Only wire-level errors (sandbox unreachable, timeout) surface as
//     ToolResult.Success = false.
//   - Output truncation: shell installers produce thousands of lines that
//     would blow up the LLM context. We keep the head (leading messages)
//     and the tail (final errors) with an ellipsis marker so the tail —
//     usually the most informative segment — is preserved.
//   - Command shape blacklist: the sandbox is throwaway, but we still refuse
//     obviously destructive patterns (rm -rf /, fork bombs, mkfs...) to
//     protect the LLM from its own hallucinations.
//   - No backgrounding, no stdin: matches the confirmed product decisions.
//     Trailing '&' and 'nohup' are rejected up-front to avoid orphaned
//     processes inside the sandbox.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// SandboxCommandExecutor is the narrow, tool-facing subset of a session-aware
// sandbox manager that supports executing arbitrary shell commands. In
// production it is satisfied by *sandbox.SessionBoundManager; tests can stub
// it with an in-memory fake. Kept local to the tools package for the same
// reason as SandboxFileSource — no dependency leak into higher layers.
type SandboxCommandExecutor interface {
	ExecShellCommand(
		ctx context.Context,
		sessionID string,
		command string,
		workDir string,
		timeout time.Duration,
		env map[string]string,
	) (*sandbox.ExecuteResult, error)
}

// Limits — kept generous enough for `pip install tensorflow` while still
// bounding the LLM context blast radius.
const (
	// defaultShellExecWorkDir is where commands land when the caller omits
	// work_dir. Matches CubeSandbox.Execute's remote directory convention.
	defaultShellExecWorkDir = "/workspace"
	// defaultShellExecTimeout is applied when the caller omits timeout_sec.
	// 120s is enough for most pip installs; heavier installs can opt in via
	// timeout_sec up to shellExecMaxTimeout.
	defaultShellExecTimeout = 120 * time.Second
	// shellExecMaxTimeout hard-caps timeout_sec. Ten minutes covers even the
	// slow "install libreoffice" case without letting a runaway command
	// pin the session's sandbox for hours.
	shellExecMaxTimeout = 10 * time.Minute
	// shellExecMaxCommandBytes rejects excessively long command strings.
	// Real skill setup scripts fit comfortably under 8 KiB.
	shellExecMaxCommandBytes = 8 * 1024
	// max_output_bytes controls stdout only. Stderr uses a smaller independent
	// budget so verbose failures cannot consume the entire tool result.
	defaultShellExecOutputBytes = 16 * 1024
	maxShellExecOutputBytes     = 64 * 1024
	defaultShellExecStderrBytes = 8 * 1024
	maxShellExecStderrBytes     = 16 * 1024
	maxShellExecErrorBytes      = 4 * 1024
	maxShellExecVisibleBytes    = 64 * 1024
)

// Blacklist patterns. These are cheap sanity checks, not a security
// perimeter — the Cube MicroVM session isolation is the real perimeter.
// The intent is to prevent the LLM from bricking its own session with a
// hallucinated one-liner (e.g. `rm -rf /`).
//
// Each entry is a compiled regexp; we return the first matching entry's
// name in the error so the LLM sees exactly why its command was rejected
// and can adjust.
var shellExecBlacklist = []struct {
	name string
	re   *regexp.Regexp
}{
	// `rm -rf /` and variants (including `rm -Rf --no-preserve-root /`).
	// Reject any rm with a recursive+force flag targeting the filesystem
	// root. We intentionally do NOT block `rm -rf /workspace/foo` — a
	// skill legitimately might clean up its scratch directory.
	{name: "rm_root", re: regexp.MustCompile(`(?i)\brm\s+(?:-[a-z]*[rR][a-z]*[fF][a-z]*|-[a-z]*[fF][a-z]*[rR][a-z]*|--recursive[^;|&]*--force|--force[^;|&]*--recursive)\s+(?:--no-preserve-root\s+)?/(?:\s|$)`)},
	// Classic fork bomb.
	{name: "fork_bomb", re: regexp.MustCompile(`:\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`)},
	// Filesystem-format / raw-device writes.
	{name: "mkfs", re: regexp.MustCompile(`(?i)\bmkfs(\.[a-z0-9]+)?\b`)},
	{name: "dd_to_device", re: regexp.MustCompile(`(?i)\bdd\b[^;|&]*\bof=/dev/`)},
	// Host-level power management. Even inside a MicroVM these serve no
	// legitimate skill purpose and would just tear down the session.
	{name: "shutdown", re: regexp.MustCompile(`(?i)\b(shutdown|reboot|halt|poweroff)\b`)},
	// Explicit backgrounding is a product decision (see file header). Trailing
	// `&` (but not `&&`) or a `nohup` prefix indicates the LLM tried to
	// detach a process.
	{name: "background_amp", re: regexp.MustCompile(`(?:^|[^&])&\s*(?:#.*)?$`)},
	{name: "nohup", re: regexp.MustCompile(`(?i)(^|[;|&\s])nohup\b`)},
}

// Tool schema

var shellExecTool = BaseTool{
	name: ToolShellExec,
	description: `Run a shell command inside the current session's isolated remote sandbox.

## Usage
- Use freely to explore and operate inside the sandbox: inspect files, search
  content, transform data, run programs, manage dependencies, install system packages and verify outputs.
- If a 'command not found' error occurs, attempt to resolve it by running ` + "`apt-get update && apt-get install -y <package>`" + `, or use any other appropriate method to install the missing command.
- User-uploaded files listed in ` + "`<sandbox_attachments>`" + ` are restored under
  ` + "`/workspace/input`" + `. Treat them as read-only inputs; write generated files
  under ` + "`/workspace/output`" + `.
- Prefer standard Unix tools for filesystem work: ` + "`find`" + ` / ` + "`file`" + ` to discover
  files and types; ` + "`cat`" + ` / ` + "`head`" + ` / ` + "`tail`" + ` / ` + "`sed`" + ` to inspect
  text; ` + "`grep`" + ` / ` + "`awk`" + ` to search and process it.
- Install skill dependencies (` + "`pip install ...`" + `, ` + "`apt-get update && apt-get install -y ...`" + `,
  ` + "`npm i ...`" + `) before calling ` + "`execute_skill_script`" + ` when required.

## When to Use
- Whenever executing a command is the most direct way to complete the task.
- To inspect any text file or directory in the sandbox, including system paths.
- To chain shell pipelines, unpack archives, compile or run code, and prepare
  intermediate files for later commands or skills.

## When NOT to Use
- DO NOT use this to run a skill's main script — use ` + "`execute_skill_script`" + `
  which handles skill lookup, artifact collection, and proper interpreter
  selection.
- DO NOT try to background processes (` + "`&`" + ` at the end, ` + "`nohup`" + `). Sandbox
  execution is synchronous.

## Parameters
- ` + "`command`" + ` (required): the shell one-liner to run under ` + "`/bin/bash -l -c`" + `.
  Supports pipes, redirects, ` + "`&&`" + ` / ` + "`||`" + ` chaining.
- ` + "`work_dir`" + ` (optional): working directory, defaults to ` + "`/workspace`" + `.
  Created on demand if it doesn't exist.
- ` + "`timeout_sec`" + ` (optional): per-call timeout in seconds. Defaults to 120,
  capped at 600. Large installs (LibreOffice, TensorFlow) may need the cap.
- ` + "`max_output_bytes`" + ` (optional): maximum bytes returned from stdout.
  Defaults to 16384, capped at 65536. Stderr is independently limited to 8192
  bytes by default; ` + "`max_stderr_bytes`" + ` may raise it to at most 16384.
  The complete visible result is always capped at 65536 bytes.
- ` + "`env`" + ` (optional): extra environment variables merged on top of the
  sandbox's base env, e.g. ` + "`{\"PIP_INDEX_URL\": \"https://mirrors.tencent.com/pypi/simple\"}`" + `.

## Returns
- ` + "`exit_code`" + `: 0 on success, non-zero on failure. Non-zero is NOT a tool
  error — the tool call succeeds; you should read stderr and decide what to
  do next (retry, adjust arguments, tell the user).
- ` + "`stdout`" + ` / ` + "`stderr`" + `: captured output, truncated to preserve context
  budget. The tail is preserved when truncation happens, since the final
  lines usually carry the crucial error message.
- Binary output is never returned to the model. Store binary files under
  ` + "`/workspace/output`" + ` so ArtifactCollector can expose them for download.
- ` + "`duration_ms`" + `: wall-clock execution time.

## Safety
- The command runs inside a session-scoped MicroVM: destructive commands
  only affect this session's sandbox, never the host or other sessions.
- Obviously destructive patterns (` + "`rm -rf /`" + `, fork bombs, ` + "`mkfs`" + `, ` + "`shutdown`" + `)
  are refused up-front. Cleaning up your own scratch dir (e.g.
  ` + "`rm -rf /workspace/tmp`" + `) is fine.
- Only available when the sandbox backend is Remote SandBox. On Docker / Local
  deployments this tool is not registered.`,
	schema: utils.GenerateSchema[ShellExecInput](),
}

// ShellExecInput defines the input parameters for shell_exec.
type ShellExecInput struct {
	// Command is the shell command to execute. Runs under `/bin/bash -l -c`.
	Command string `json:"command" jsonschema:"Shell command to execute (single line, supports pipes and && chaining). Runs under /bin/bash -l -c."`
	// WorkDir is the working directory for the command; defaults to /workspace.
	WorkDir string `json:"work_dir,omitempty" jsonschema:"Working directory for the command. Defaults to /workspace. Created on demand if missing."`
	// TimeoutSec caps execution time. Zero uses the default (120s); the
	// value is hard-capped at 600s regardless of what the LLM requests.
	TimeoutSec int `json:"timeout_sec,omitempty" jsonschema:"Per-call timeout in seconds. Defaults to 120, hard-capped at 600."`
	// MaxOutputBytes caps returned stdout. Stderr has an independent smaller
	// fixed budget, and the complete model-visible output is capped at 64 KiB.
	MaxOutputBytes int `json:"max_output_bytes,omitempty" jsonschema:"Maximum bytes returned from stdout. Defaults to 16384, hard-capped at 65536. Stderr defaults to 8192 and is hard-capped at 16384; total visible output is hard-capped at 65536."`
	// MaxStderrBytes caps returned stderr independently from stdout.
	MaxStderrBytes int `json:"max_stderr_bytes,omitempty" jsonschema:"Maximum bytes returned from stderr. Defaults to 8192, hard-capped at 16384."`
	// Env carries extra environment variables merged into the shell's env.
	Env map[string]string `json:"env,omitempty" jsonschema:"Optional extra environment variables, e.g. {\"PIP_INDEX_URL\":\"https://mirrors.example.com/pypi/simple\"}."`
}

// SandboxInstallCommandExecutor is the privileged counterpart of
// SandboxCommandExecutor, satisfied by *sandbox.SessionBoundManager via
// sandbox.SessionInstallShellExecutor. It exists as its own named type so the
// install privilege can only be handed over deliberately: nothing that merely
// implements ExecShellCommand can be mistaken for it.
type SandboxInstallCommandExecutor interface {
	ExecShellCommandWithOptions(
		ctx context.Context,
		sessionID string,
		command string,
		opts sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error)
}

// installShellExecutor adapts the privileged executor to the plain executor
// contract the tool speaks, stamping every call as root with the skills image
// root writable. The install agent's whole job is to install dependencies into
// that image, which the default user cannot write.
type installShellExecutor struct {
	inner SandboxInstallCommandExecutor
}

func (e installShellExecutor) ExecShellCommand(
	ctx context.Context,
	sessionID string,
	command string,
	workDir string,
	timeout time.Duration,
	env map[string]string,
) (*sandbox.ExecuteResult, error) {
	return e.inner.ExecShellCommandWithOptions(ctx, sessionID, command, sandbox.ShellExecOptions{
		WorkDir:         workDir,
		Timeout:         timeout,
		Env:             env,
		AllowSkillsRoot: true,
		AsRoot:          true,
	})
}

// ShellExecTool executes shell commands inside the session's sandbox.
type ShellExecTool struct {
	BaseTool
	executor SandboxCommandExecutor
	// workDirRoots are the directories work_dir may point inside. Ordinary
	// sessions get /workspace only; install mode adds the skills image root.
	workDirRoots []string
	// defaultTimeout is applied when the caller omits timeout_sec. Ordinary
	// sessions keep the 120s default; install mode uses the 10-minute cap
	// because dependency installs routinely exceed two minutes.
	defaultTimeout time.Duration
}

// NewShellExecTool constructs the tool. `executor` MUST NOT be nil:
// callers should feature-gate registration when the sandbox backend
// does not support ad-hoc shell execution (i.e. is not Cube).
func NewShellExecTool(executor SandboxCommandExecutor) *ShellExecTool {
	return &ShellExecTool{
		BaseTool:     shellExecTool,
		executor:     executor,
		workDirRoots: []string{defaultShellExecWorkDir},
	}
}

// NewInstallShellExecTool constructs the install-mode variant: commands run as
// root and may work inside the skills image root. It is registered only for
// the built-in skill installer agent (see AgentConfig.SkillInstallMode).
func NewInstallShellExecTool(executor SandboxInstallCommandExecutor) *ShellExecTool {
	return &ShellExecTool{
		BaseTool:       shellExecTool,
		executor:       installShellExecutor{inner: executor},
		workDirRoots:   []string{defaultShellExecWorkDir, sandbox.SkillsImageRoot},
		defaultTimeout: shellExecMaxTimeout,
	}
}

// OutputLimitChars lets ToolRegistry preserve shell_exec's explicitly bounded,
// caller-configurable output instead of applying its lower generic limit again.
func (t *ShellExecTool) OutputLimitChars(args json.RawMessage) int {
	return maxShellExecVisibleBytes
}

// Execute runs the requested command inside the current session's sandbox.
func (t *ShellExecTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][ShellExec] Execute started")

	var input ShellExecInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, nil
	}

	if t.executor == nil {
		return &types.ToolResult{
			Success: false,
			Error:   "shell_exec is not available in this deployment (remote sandbox required)",
		}, nil
	}

	command := strings.TrimSpace(input.Command)
	if command == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "command is required",
		}, nil
	}
	if len(command) > shellExecMaxCommandBytes {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("command too long (%d bytes; max %d)", len(command), shellExecMaxCommandBytes),
		}, nil
	}
	if reason := checkShellExecBlacklist(command); reason != "" {
		logger.Warnf(ctx, "[Tool][ShellExec] rejected by blacklist: %s command=%q", reason, command)
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("command rejected by shell_exec safety guard: %s", reason),
		}, nil
	}

	sessionID := resolveSessionID(ctx)
	if sessionID == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "no session ID in context; shell_exec must run inside an agent turn",
		}, nil
	}

	workDir := strings.TrimSpace(input.WorkDir)
	if workDir == "" {
		workDir = defaultShellExecWorkDir
	}
	cleanWorkDir := path.Clean(workDir)
	if !t.workDirAllowed(cleanWorkDir) {
		return &types.ToolResult{
			Success: false,
			Error: fmt.Sprintf(
				"work_dir %q is outside the allowed sandbox roots %s",
				input.WorkDir, strings.Join(t.allowedWorkDirRoots(), ", "),
			),
		}, nil
	}
	workDir = cleanWorkDir

	timeout := t.defaultTimeout
	if timeout <= 0 {
		timeout = defaultShellExecTimeout
	}
	if input.TimeoutSec > 0 {
		timeout = time.Duration(input.TimeoutSec) * time.Second
	}
	if timeout > shellExecMaxTimeout {
		timeout = shellExecMaxTimeout
	}

	logger.Infof(ctx, "[Tool][ShellExec] session=%s work_dir=%s timeout=%s command=%q",
		sessionID, workDir, timeout, command)

	res, err := t.executor.ExecShellCommand(ctx, sessionID, command, workDir, timeout, input.Env)
	if err != nil {
		logger.Warnf(ctx, "[Tool][ShellExec] execution error: session=%s err=%v", sessionID, err)
		errorText, _ := truncateShellStream(fmt.Sprintf("shell_exec failed: %v", err), maxShellExecErrorBytes)
		return &types.ToolResult{
			Success: false,
			Error:   errorText,
		}, nil
	}

	outputLimit := resolveShellOutputLimit(input.MaxOutputBytes)
	stderrLimit := resolveShellStderrLimit(input.MaxStderrBytes)
	stdout, stdoutTruncated, stdoutBinary := prepareShellStream(res.Stdout, outputLimit)
	stderr, stderrTruncated, stderrBinary := prepareShellStream(res.Stderr, stderrLimit)
	errorText, errorTruncated := truncateShellStream(res.Error, maxShellExecErrorBytes)
	truncated := stdoutTruncated || stderrTruncated || errorTruncated

	// Human-readable summary for the LLM.
	var b strings.Builder
	b.WriteString(fmt.Sprintf("=== Shell Exec (session=%s) ===\n\n", sessionID))
	b.WriteString(fmt.Sprintf("**Command**: `%s`\n", command))
	b.WriteString(fmt.Sprintf("**Work Dir**: %s\n", workDir))
	b.WriteString(fmt.Sprintf("**Exit Code**: %d\n", res.ExitCode))
	b.WriteString(fmt.Sprintf("**Duration**: %v\n", res.Duration))
	if res.Killed {
		b.WriteString("**Killed**: yes (timeout or terminated)\n")
	}
	if truncated {
		b.WriteString("**Truncated**: yes (head+tail kept; run `tail -n 200 <logfile>` inside the sandbox for full output)\n")
	}
	if stdoutBinary || stderrBinary {
		b.WriteString("**Binary Output Suppressed**: yes (write binary files to the artifact output directory for download)\n")
	}
	b.WriteString("\n")

	if stdout != "" {
		b.WriteString("## Stdout\n\n```\n")
		b.WriteString(stdout)
		if !strings.HasSuffix(stdout, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")
	}
	if stderr != "" {
		b.WriteString("## Stderr\n\n```\n")
		b.WriteString(stderr)
		if !strings.HasSuffix(stderr, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")
	}
	if errorText != "" {
		b.WriteString("## Error\n\n")
		b.WriteString(errorText)
		b.WriteString("\n")
	}
	visibleOutput := b.String()
	visibleOutput, totalTruncated := truncateShellStream(visibleOutput, maxShellExecVisibleBytes)
	truncated = truncated || errorTruncated || totalTruncated

	// The tool call itself succeeds even when the shell command exits non-zero:
	// the LLM needs stderr/exit_code as first-class signals to iterate. We
	// only mark Success=false when a wire-level problem prevented the command
	// from running at all (already handled above via err != nil).
	resultData := map[string]interface{}{
		"display_type":           "shell_exec",
		"session_id":             sessionID,
		"command":                command,
		"work_dir":               workDir,
		"exit_code":              res.ExitCode,
		"stdout":                 stdout,
		"stderr":                 stderr,
		"duration_ms":            res.Duration.Milliseconds(),
		"killed":                 res.Killed,
		"truncated":              truncated,
		"stdout_truncated":       stdoutTruncated,
		"stderr_truncated":       stderrTruncated,
		"stdout_binary":          stdoutBinary,
		"stderr_binary":          stderrBinary,
		"stdout_bytes":           len(res.Stdout),
		"stderr_bytes":           len(res.Stderr),
		"stdout_original_bytes":  len(res.Stdout),
		"stdout_returned_bytes":  len(stdout),
		"stderr_original_bytes":  len(res.Stderr),
		"stderr_returned_bytes":  len(stderr),
		"error_original_bytes":   len(res.Error),
		"error_returned_bytes":   len(errorText),
		"error_truncated":        errorTruncated,
		"total_truncated":        totalTruncated,
		"visible_original_bytes": b.Len(),
		"visible_returned_bytes": len(visibleOutput),
		"max_output_bytes":       outputLimit,
		"max_stderr_bytes":       stderrLimit,
	}

	logger.Infof(ctx, "[Tool][ShellExec] session=%s exit=%d duration=%v killed=%v truncated=%v",
		sessionID, res.ExitCode, res.Duration, res.Killed, truncated)

	return &types.ToolResult{
		Success: true,
		Output:  visibleOutput,
		Data:    resultData,
	}, nil
}

// allowedWorkDirRoots defaults to /workspace so a zero-value tool (or one
// built before install mode existed) keeps the ordinary contract.
func (t *ShellExecTool) allowedWorkDirRoots() []string {
	if len(t.workDirRoots) == 0 {
		return []string{defaultShellExecWorkDir}
	}
	return t.workDirRoots
}

func (t *ShellExecTool) workDirAllowed(cleanWorkDir string) bool {
	for _, root := range t.allowedWorkDirRoots() {
		if isUnderRoot(cleanWorkDir, root) {
			return true
		}
	}
	return false
}

func resolveShellOutputLimit(requested int) int {
	if requested <= 0 {
		return defaultShellExecOutputBytes
	}
	if requested > maxShellExecOutputBytes {
		return maxShellExecOutputBytes
	}
	return requested
}

func resolveShellStderrLimit(requested int) int {
	if requested <= 0 {
		return defaultShellExecStderrBytes
	}
	if requested > maxShellExecStderrBytes {
		return maxShellExecStderrBytes
	}
	return requested
}

// prepareShellStream suppresses binary data before it can enter ToolResult
// Output or Data. Text streams are bounded using head+tail preservation.
func prepareShellStream(s string, limit int) (output string, truncated, binary bool) {
	if isBinaryShellOutput(s) {
		return "", false, true
	}
	output, truncated = truncateShellStream(s, limit)
	return output, truncated, false
}

func isBinaryShellOutput(s string) bool {
	if s == "" {
		return false
	}
	if !utf8.ValidString(s) || strings.IndexByte(s, 0) >= 0 {
		return true
	}
	// Any non-text control byte is enough to suppress the stream. ANSI terminal
	// escapes remain allowed so ordinary colored command output stays readable.
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' &&
			r != '\b' && r != '\f' && r != 0x1b {
			return true
		}
	}
	return false
}

// Cleanup releases any resources.
func (t *ShellExecTool) Cleanup(ctx context.Context) error {
	return nil
}

// checkShellExecBlacklist reports a non-empty reason string when command
// matches one of the blacklist patterns. Returns "" for allowed commands.
func checkShellExecBlacklist(command string) string {
	for _, entry := range shellExecBlacklist {
		if entry.re.MatchString(command) {
			return entry.name
		}
	}
	return ""
}

// truncateShellStream reduces s to at most limit bytes by keeping the head
// and tail of the stream. The tail is prioritised because the final lines
// of a shell run almost always carry the actionable diagnostic (success
// marker, traceback, "ERROR: could not find matching distribution").
//
// Returns the (possibly truncated) content and a flag indicating whether
// any trimming happened.
func truncateShellStream(s string, limit int) (string, bool) {
	if limit <= 0 || len(s) <= limit {
		return s, false
	}
	// Include the marker inside the byte budget. Its omitted-byte count depends
	// on the retained size, so compute it once, then recalculate the final split.
	marker := fmt.Sprintf("\n...[truncated %d bytes]...\n", len(s)-limit)
	if len(marker) >= limit {
		return s[len(s)-limit:], true
	}
	kept := limit - len(marker)
	head := kept / 4
	tail := kept - head
	marker = fmt.Sprintf("\n...[truncated %d bytes]...\n", len(s)-head-tail)
	if len(marker) != limit-kept {
		kept = limit - len(marker)
		head = kept / 4
		tail = kept - head
	}
	var b strings.Builder
	b.Grow(limit)
	b.WriteString(s[:head])
	b.WriteString(marker)
	b.WriteString(s[len(s)-tail:])
	return b.String(), true
}
