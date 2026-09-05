// Package tools — shell_exec.
//
// General shell command execution primitive for the agent. The LLM can freely
// explore and operate inside its session-scoped Cube MicroVM: inspect files,
// transform data, run programs, install dependencies, and prepare or verify
// skill outputs.
//
// Design notes:
//
//   - Session-sandbox capability: registration is feature-gated on the
//     sandbox backend exposing SandboxCommandExecutor (Cube, E2B, Docker).
//     shell_exec never runs on the WeKnora host.
//   - Session-scoped: the sandbox is resolved from ToolExecContext.SessionID
//     so the LLM cannot execute against a foreign session, and installed
//     dependencies persist across subsequent tool calls in the same session.
//   - Non-zero exit is a normal signal, not a tool failure: pip install
//     failures, missing binaries, etc. are all valid results the LLM must
//     inspect. Wire-level errors (sandbox unreachable) and commands that
//     were killed or timed out surface as ToolResult.Success = false.
//   - Output truncation: shell installers produce thousands of lines that
//     would blow up the LLM context. We keep the head (leading messages)
//     and the tail (final errors) with an ellipsis marker so the tail —
//     usually the most informative segment — is preserved.
//   - Command shape blacklist: the sandbox is throwaway, but we still refuse
//     obviously destructive patterns (rm -rf /, fork bombs, mkfs...) to
//     protect the LLM from its own hallucinations.
//   - No backgrounding: trailing '&' and 'nohup' are rejected up-front to
//     avoid orphaned processes inside the sandbox. Optional stdin is allowed
//     as data; when the command is an interpreter that would run stdin as a
//     program, the same blacklist and command-size cap apply to that payload.
package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/agent/skills"
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
	// Real skill setup one-liners fit comfortably under 8 KiB. Generated
	// scripts belong in write_sandbox_file, not inlined in the command.
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
	description: `Execute a command in the current session's isolated sandbox as its non-root user. Never runs on the host.
- CWD defaults to /workspace on every call; cd does not persist. work_dir selects another directory under /workspace and missing directories are created as the same user.
- Use ls/find to discover files, grep/awk to search, and cat/head/tail/sed to inspect text. Read known paths directly; no mandatory discovery call.
- Use write_sandbox_file for scripts or large text; edit_sandbox_file for precise changes. Commands are limited to 8192 bytes. Execution is synchronous (no nohup or trailing &).
- skill_name selects a listed skill for this call. Installed skills use their Python virtualenv and Node modules; host skill resources are automatically staged in the session with the system runtime. Scoped credentials apply to both. Example: skill_name="pdf", command="python3 report.py". Run bundled scripts via "$WEKNORA_SKILL_DIR/scripts/...". Omit skill_name for system commands.
- /workspace/input contains user attachments: preserve originals. /workspace/output holds downloadable deliverables. Installed skills under /opt/weknora/tenant/skills are read-only. Install Python extras WITHOUT skill_name using python3 -m pip install --target /workspace/.skill-packages/<skill> <package>, then run with skill_name. System package installation requires the skill installer; ordinary sessions cannot apt-get or elevate privileges.
- Non-zero exit_code is a command result: inspect stderr before deciding whether a corrected call is useful. Transport failures/timeouts are tool failures. Changing tools does not change permissions; do not repeat a denied operation through another tool.
- stdout/stderr have independent byte limits, preserving head and tail when truncated. Full output is not automatically saved; redirect verbose commands to a workspace log when it must be retained. Binary bytes are suppressed.
- Reference collected deliverables as ![description](sandbox:<file name>) using the exact filename.`,
	schema: utils.GenerateSchema[ShellExecInput](),
}

// ShellExecInput defines the input parameters for shell_exec.
type ShellExecInput struct {
	Stdin string `json:"stdin,omitempty" jsonschema:"Optional text passed to the command's stdin, up to 65536 bytes. Preserves quotes and newlines exactly; for larger input write a workspace file and redirect from it."`
	// Command is the shell command to execute. Runs under Bash.
	Command string `json:"command" jsonschema:"Shell command to execute (single line, supports pipes and && chaining). Runs under Bash."`
	// WorkDir is the working directory for the command; defaults to /workspace.
	WorkDir string `json:"work_dir,omitempty" jsonschema:"Absolute or relative work dir. Commands already start in /workspace; omit unless the command must run elsewhere."` //nolint:lll // one-line struct tag
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
	// SkillName, when set, pulls that skill's scoped environment variables
	// (API keys) into this one command's process only. Resolution
	// uses the caller-scoped SkillEnvResolver, so values
	// are per-caller (taken from ctx) and never persist. Omitting it leaves
	// shell_exec's behaviour unchanged.
	SkillName string `json:"skill_name,omitempty" jsonschema:"Optional available skill name. Selects its installed runtime or stages its host resources, plus package overlay and scoped credentials. CWD remains /workspace. Omit for system commands."`
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
	// defaultWorkDir is where a call that omits work_dir lands. Empty means
	// /workspace, which is right for an ordinary session and wrong for an
	// install: an install works in one skill directory and is told not to
	// touch /workspace at all (it is wiped before the snapshot). Leaving the
	// default there made the model prefix `cd <skill-dir> &&` onto command
	// after command, since that is the spelling guaranteed to work.
	defaultWorkDir string
	// defaultTimeout is applied when the caller omits timeout_sec. Ordinary
	// sessions keep the 120s default; install mode uses the 10-minute cap
	// because dependency installs routinely exceed two minutes.
	defaultTimeout time.Duration
	// envResolver, when non-nil, lets a call carrying SkillName pull that
	// skill's per-caller env into the one command it runs. Nil means no skill
	// env is ever injected — identical to today's behaviour.
	envResolver skills.SkillEnvResolver
	// envCapture, when non-nil, records declared skill credentials a
	// successful ordinary command already used so the next named run can
	// inject them. Install-mode tools never invoke it.
	envCapture       SkillEnvCapture
	skillEnvironment *skills.Manager
}

func (t *ShellExecTool) WithSkillEnvironment(manager *skills.Manager) *ShellExecTool {
	t.skillEnvironment = manager
	return t
}

// SkillEnvCapture records NAME=value pairs a successful shell_exec already
// used for one skill. The tools package does not persist them; the agent
// service supplies the write. Values must not be logged by the caller.
type SkillEnvCapture func(ctx context.Context, skillName string, pairs map[string]string)

// NewShellExecTool constructs the tool. `executor` MUST NOT be nil:
// callers should feature-gate registration when the sandbox backend
// does not support ad-hoc shell execution (i.e. is not Cube).
func NewShellExecTool(executor SandboxCommandExecutor, envResolver skills.SkillEnvResolver) *ShellExecTool {
	return &ShellExecTool{
		BaseTool:     shellExecTool,
		executor:     executor,
		envResolver:  envResolver,
		workDirRoots: []string{defaultShellExecWorkDir},
	}
}

// NewInstallShellExecTool constructs the install-mode variant: commands run as
// root and may work inside the skills image root. It is registered only for
// the built-in skill installer agent (see AgentConfig.SkillInstallMode).
//
// skillDir becomes the default working directory, because every command an
// install runs belongs there. A validated directory is required for that: an
// empty or unrecognised one falls back to /workspace rather than guessing.
func NewInstallShellExecTool(
	executor SandboxInstallCommandExecutor, skillDir string,
) *ShellExecTool {
	base := shellExecTool
	defaultWorkDir, ok := sandbox.ValidatedImageSkillDir(skillDir)
	if !ok {
		defaultWorkDir = defaultShellExecWorkDir
	}
	base.description = installShellExecDescription(defaultWorkDir)
	return &ShellExecTool{
		BaseTool:       base,
		executor:       installShellExecutor{inner: executor},
		workDirRoots:   []string{defaultShellExecWorkDir, sandbox.SkillsImageRoot},
		defaultWorkDir: defaultWorkDir,
		defaultTimeout: shellExecMaxTimeout,
	}
}

// installShellExecDescription replaces the session-agent "use write_sandbox_file"
// guidance. That tool is not registered in install mode, and the files this
// agent must write sit under the skills image root, which write_sandbox_file
// cannot accept.
func installShellExecDescription(defaultWorkDir string) string {
	return `Run a shell command as root inside the skill-install sandbox.

## Working Directory
- Every command already starts in ` + "`" + defaultWorkDir + "`" + `, the skill
  you are installing. Use RELATIVE paths: ` + "`ls -la scripts/`" + `,
  ` + "`cat requirements.txt`" + `, ` + "`uv venv --seed .venv`" + `.
- Do NOT prefix ` + "`cd " + defaultWorkDir + " && `" + ` onto your commands.
  You are already there, and the prefix wastes a line on every call.
- Pass ` + "`work_dir`" + ` only to leave that directory, which an install
  rarely needs.

## Usage
- Run commands with this tool. Create or change files in the skill directory
  with ` + "`write_skill_file`" + ` / ` + "`edit_skill_file`" + ` instead of
  ` + "`cat`" + ` or a heredoc: a shell redirect truncates at the
  command-length cap and mangles quoting.
- Install Python extras into the skill's ` + "`.venv`" + `, Node extras into
  ` + "`node_modules`" + `. Prefer ` + "`uv pip install`" + ` / ` + "`python3 -m venv`" + `.

## Parameters
- ` + "`command`" + ` (required): the shell one-liner under ` + "`/bin/bash -l -c`" + `.
- ` + "`work_dir`" + ` (optional): defaults to ` + "`" + defaultWorkDir + "`" + `.
- ` + "`timeout_sec`" + ` (optional): defaults to 600 seconds.

## Returns
- ` + "`exit_code`" + `, ` + "`stdout`" + `, ` + "`stderr`" + `. Non-zero is not a
  tool error — read stderr and adapt.`
}

// WithEnvCapture attaches an optional capture hook. A nil hook is a no-op so
// callers can pass the wiring result through without a nil check.
func (t *ShellExecTool) WithEnvCapture(capture SkillEnvCapture) *ShellExecTool {
	if t != nil {
		t.envCapture = capture
	}
	return t
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
	if input.SkillName != "" && t.skillEnvironment == nil {
		return &types.ToolResult{Success: false, Error: "no skill environment is available for this call; omit skill_name for system commands"}, nil
	}

	if len(input.Stdin) > 65536 {
		return &types.ToolResult{Success: false, Error: "stdin exceeds 65536 bytes; write the input to a workspace file and redirect from it"}, nil
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
			Error: fmt.Sprintf(
				"command too long (%d bytes; max %d). Put the file in write_sandbox_file, then run it with shell_exec",
				len(command), shellExecMaxCommandBytes,
			),
		}, nil
	}
	if reason := checkShellExecBlacklist(command); reason != "" {
		logger.Warnf(ctx, "[Tool][ShellExec] rejected by blacklist: %s command=%q",
			reason, maskCommandAssignments(command))
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("command rejected by shell_exec safety guard: %s", reason),
		}, nil
	}
	if reason := rejectExecutableStdin(command, input.Stdin); reason != "" {
		logger.Warnf(ctx, "[Tool][ShellExec] rejected executable stdin: %s command=%q",
			reason, maskCommandAssignments(command))
		return &types.ToolResult{Success: false, Error: reason}, nil
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
		workDir = t.effectiveDefaultWorkDir()
	}
	if !path.IsAbs(workDir) {
		workDir = path.Join(t.effectiveDefaultWorkDir(), workDir)
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
		timeout = time.Duration(min(input.TimeoutSec, int(shellExecMaxTimeout/time.Second))) * time.Second
	}
	if timeout > shellExecMaxTimeout {
		timeout = shellExecMaxTimeout
	}

	logger.Infof(ctx, "[Tool][ShellExec] session=%s work_dir=%s timeout=%s command=%q",
		sessionID, workDir, timeout, maskCommandAssignments(command))

	// The caller's config-wide variables apply to every command; a skill's
	// declared credentials are added only when the model names the skill.
	// Values come from ctx (the current principal), live only for this process,
	// and are overlaid without displacing anything the model passed via env.
	env := input.Env
	// supplied carries the values this one call brings with it, from the env
	// parameter and from NAME=value assignments in the command. They satisfy a
	// required variable that is not stored yet, which is what makes "tell me
	// the key in chat" work, and they are the only values capture may persist.
	supplied := collectUsedSkillEnv(input.Command, input.Env)
	if t.envResolver != nil {
		resolved, missing, rerr := t.envResolver.ResolveEnv(ctx, input.SkillName)
		if rerr != nil {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("failed to resolve environment variables: %v", rerr),
			}, nil
		}
		missing = stillMissing(missing, supplied)
		if len(missing) > 0 {
			return &types.ToolResult{
				Success: false,
				Error: fmt.Sprintf(
					"skill %q needs the environment variable(s) %s, which nobody has set yet. "+
						"Ask the user for them and pass them in this call's env, "+
						"or have them set the values under Settings → Sandbox secrets.",
					input.SkillName, strings.Join(missing, ", ")),
			}, nil
		}
		if len(resolved) > 0 && env == nil {
			env = make(map[string]string)
		}
		// Model-supplied env wins over resolved values, matching the
		// skill credential contract.
		skills.ApplyResolvedEnv(env, resolved)
		// A name that already resolved is one the workspace or this caller has
		// filled in. Capture must not touch it: otherwise a hallucinated
		// `export KEY=test` would overwrite a working stored credential.
		supplied = dropResolvedNames(supplied, resolved)
	}
	execCommand := command
	if input.SkillName != "" && t.skillEnvironment != nil {
		var prepErr error
		execCommand, env, prepErr = t.skillEnvironment.PrepareShellEnvironment(ctx, sessionID, input.SkillName, command, env)
		if prepErr != nil {
			return &types.ToolResult{Success: false, Error: prepErr.Error()}, nil
		}
	}
	if input.Stdin != "" {
		// Apply input to the entire command, including compound shell grammar.
		// Encoding keeps data out of shell syntax and preserves trailing newlines.
		execCommand = "printf %s " + sandbox.ShellQuote(base64.StdEncoding.EncodeToString([]byte(input.Stdin))) +
			" | base64 -d | /bin/bash --noprofile --norc -c " + sandbox.ShellQuote(execCommand)
	}
	beforeOutputs, inspectedOutputs := sandboxOutputSnapshot(ctx, t.executor, sessionID)
	res, err := t.executor.ExecShellCommand(ctx, sessionID, execCommand, workDir, timeout, env)
	noteSandboxMutation()
	if err != nil {
		logger.Warnf(ctx, "[Tool][ShellExec] execution error: session=%s err=%v", sessionID, err)
		errorText, _ := truncateShellStream(fmt.Sprintf("shell_exec failed: %v", err), maxShellExecErrorBytes)
		return &types.ToolResult{
			Success: false,
			Error:   errorText,
		}, nil
	}
	if res == nil {
		return &types.ToolResult{Success: false, Error: "shell executor returned no result"}, nil
	}
	if res.Killed && res.Error == "" {
		res.Error = sandbox.ErrTimeout.Error()
	}

	t.maybeCaptureSkillEnv(ctx, input.SkillName, supplied, res)

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
		b.WriteString("**Truncated**: yes (head+tail kept; full output was not saved. For future commands, redirect verbose output to a workspace log and read that file.)\n")
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
	if hint := t.recoveryHint(input.SkillName, res.ExitCode, command, stderr); hint != "" {
		b.WriteString(hint)
		b.WriteString("\n")
	}
	var outputFiles []string
	if inspectedOutputs {
		if afterOutputs, ok := sandboxOutputSnapshot(ctx, t.executor, sessionID); ok {
			outputFiles = changedOutputLinks(beforeOutputs, afterOutputs)
		}
	}
	visibleOutput := b.String()
	visibleOutput, totalTruncated := truncateShellStream(visibleOutput, maxShellExecVisibleBytes)
	truncated = truncated || errorTruncated || totalTruncated

	// The tool call itself succeeds even when the shell command exits non-zero:
	// the LLM needs stderr/exit_code as first-class signals to iterate. We
	// mark Success=false when a wire-level problem prevented the command from
	// running, or when the process was killed / timed out.
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
		Success:     res.Error == "" && !res.Killed,
		Error:       errorText,
		Output:      visibleOutput,
		OutputFiles: outputFiles,
		Data:        resultData,
	}, nil
}

// maybeCaptureSkillEnv persists the credentials this call brought with it, so
// the next run of the same skill does not have to ask again.
//
// The skill must be named explicitly: inferring it from a path in the command
// would let any successful command that merely mentions a skill directory write
// into that skill's credentials. pairs has already had every resolved name
// removed, so this only ever fills a blank.
func (t *ShellExecTool) maybeCaptureSkillEnv(
	ctx context.Context, skillName string, pairs map[string]string, res *sandbox.ExecuteResult,
) {
	if t == nil || t.envCapture == nil || t.isInstallMode() {
		return
	}
	if res == nil || res.ExitCode != 0 {
		return
	}
	skillName = strings.TrimSpace(skillName)
	if !sandbox.IsValidSkillName(skillName) || len(pairs) == 0 {
		return
	}
	t.envCapture(ctx, skillName, pairs)
}

// stillMissing removes from missing every name this call supplied itself.
func stillMissing(missing []string, supplied map[string]string) []string {
	if len(missing) == 0 || len(supplied) == 0 {
		return missing
	}
	out := missing[:0:0]
	for _, name := range missing {
		if strings.TrimSpace(supplied[name]) == "" {
			out = append(out, name)
		}
	}
	return out
}

// dropResolvedNames returns the supplied values that nothing has stored yet.
func dropResolvedNames(supplied, resolved map[string]string) map[string]string {
	if len(supplied) == 0 || len(resolved) == 0 {
		return supplied
	}
	out := make(map[string]string, len(supplied))
	for name, value := range supplied {
		if _, stored := resolved[name]; stored {
			continue
		}
		out[name] = value
	}
	return out
}

func (t *ShellExecTool) isInstallMode() bool {
	for _, root := range t.workDirRoots {
		if root == sandbox.SkillsImageRoot {
			return true
		}
	}
	return false
}

// effectiveDefaultWorkDir keeps a zero-value tool on the ordinary /workspace
// contract; only the install-mode constructor sets anything else.
func (t *ShellExecTool) effectiveDefaultWorkDir() string {
	if strings.TrimSpace(t.defaultWorkDir) == "" {
		return defaultShellExecWorkDir
	}
	return t.defaultWorkDir
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

func shellExecRecoveryHint(exitCode int, command, stderr string) string {
	var parts []string
	if h := shellCommandNotFoundHint(exitCode, command, stderr); h != "" {
		parts = append(parts, h)
	}
	if isFrozenSkillVenvFailure(stderr) {
		parts = append(parts, "Hint: "+frozenSkillTreeGuidance(skillNameFromShellCommand(command)))
		return strings.Join(parts, "\n")
	}
	if h := shellMissingModuleHint(command, stderr); h != "" {
		parts = append(parts, h)
	} else if h := shellInlineEvalHint(command); h != "" {
		parts = append(parts, h)
	}
	return strings.Join(parts, "\n")
}

func (t *ShellExecTool) recoveryHint(skillName string, exitCode int, command, stderr string) string {
	if exitCode == 0 {
		return ""
	}
	if t.skillEnvironment == nil {
		return shellExecRecoveryHint(exitCode, command, stderr)
	}
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "permission denied") || strings.Contains(lower, "read-only file system") {
		return "Permission denied: commands and file tools share the same user. Use /workspace for scratch files and /workspace/output for deliverables. The installed skill tree is read-only; switching tools, chmod, sudo, or retrying the same write cannot grant access."
	}
	if isMissingInterpreterModule(stderr) {
		if skillName == "" {
			return "If this command needs an installed skill's packages, repeat shell_exec with that skill_name to select its runtime. Otherwise install the missing dependency in the writable workspace."
		}
		if strings.Contains(stderr, "Cannot find module") || strings.Contains(stderr, "MODULE_NOT_FOUND") {
			return "The selected skill runtime could not resolve this Node module. For custom scripts, install dependencies in a writable workspace project. NODE_PATH supports CommonJS; ESM imports resolve relative to the script and need a local dependency tree. Preserve the installed skill directory."
		}
		return fmt.Sprintf("The selected skill runtime lacks this module. Install extras with shell_exec WITHOUT skill_name: python3 -m pip install --target %s <package>; then rerun with skill_name=%q.", sandbox.SessionSkillPackageDir(skillName), skillName)
	}
	return shellCommandNotFoundHint(exitCode, command, stderr)
}

func shellMissingModuleHint(command, stderr string) string {
	if !isMissingInterpreterModule(stderr) {
		return ""
	}
	skill := skillNameFromShellCommand(command)
	skillArg := "skill_name=<the skill that owns those packages>"
	if skill != "" {
		skillArg = fmt.Sprintf("skill_name=%q", skill)
	}
	return "Hint: system python3 / node do not see skill packages (docx, pptx, pandas, …). " +
		"Do not pip install them into this session, and do not paste the same program into " +
		"`.venv/bin/python -c`. Write it with write_sandbox_file, then " +
		"shell_exec(" + skillArg + ", command=python3 /workspace/output/inspect.py)."
}

func isMissingInterpreterModule(stderr string) bool {
	if isFrozenSkillVenvFailure(stderr) {
		return false
	}
	return strings.Contains(stderr, "ModuleNotFoundError") ||
		strings.Contains(stderr, "No module named") ||
		strings.Contains(stderr, "Cannot find module") ||
		strings.Contains(stderr, "MODULE_NOT_FOUND")
}

func shellInlineEvalHint(command string) string {
	if !isInlineInterpreterProgram(command) {
		return ""
	}
	skill := skillNameFromShellCommand(command)
	skillArg := "skill_name=..."
	if skill != "" {
		skillArg = fmt.Sprintf("skill_name=%q", skill)
	}
	return "Hint: do not pass a multi-line program through python -c / node -e " +
		"(including a skill venv). Write it with write_sandbox_file, then " +
		"shell_exec(" + skillArg + ", command=python3 /workspace/output/inspect.py)."
}

func isInlineInterpreterProgram(command string) bool {
	if !hasInlineEvalFlag(command) {
		return false
	}
	return len(command) >= 280 || strings.Count(command, "\n") >= 2
}

func hasInlineEvalFlag(command string) bool {
	lower := strings.ToLower(command)
	pythonEval := strings.Contains(lower, "python") && strings.Contains(lower, " -c")
	nodeEval := strings.Contains(lower, "node") &&
		(strings.Contains(lower, " -e") || strings.Contains(lower, " --eval"))
	return pythonEval || nodeEval
}

func skillNameFromShellCommand(command string) string {
	idx := strings.Index(command, sandbox.SkillsImageRoot+"/")
	if idx < 0 {
		return ""
	}
	rest := command[idx:]
	if end := strings.IndexAny(rest, " \t\"'"); end > 0 {
		rest = rest[:end]
	}
	name, inImage := sandbox.SkillNameFromImagePath(rest)
	if !inImage {
		return ""
	}
	return name
}

// shellCommandNotFoundHint steers the model off apt-get install tree/editors
// after a 127. Those packages are not in the slim image (`file` is), and a
// session install is thrown away.
func shellCommandNotFoundHint(exitCode int, command, stderr string) string {
	if exitCode != 127 && !strings.Contains(strings.ToLower(stderr), "command not found") {
		return ""
	}
	missing := inferredMissingCommand(command, stderr)
	switch missing {
	case "tree", "less", "more", "nano", "vim", "vi":
		return "Hint: `" + missing + "` is not in the default sandbox image. Use find/ls, head, sed, and `file`. Skill scripts: `read_file` for skill instructions, then the execution tool named there. Do not apt-get install inspection tools — session packages are discarded."
	default:
		return "Hint: that command is not installed. Prefer find, ls, head, tail, cat, sed, grep, awk, file. apt-get install only for a package this task actually needs — session installs are discarded."
	}
}

func inferredMissingCommand(command, stderr string) string {
	lower := strings.ToLower(stderr)
	const marker = ": command not found"
	if i := strings.Index(lower, marker); i > 0 {
		head := strings.TrimSpace(stderr[:i])
		if j := strings.LastIndexAny(head, ": \t"); j >= 0 {
			head = strings.TrimSpace(head[j+1:])
		}
		if head != "" {
			return path.Base(head)
		}
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return path.Base(fields[0])
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

func rejectExecutableStdin(command, stdin string) string {
	if stdin == "" || !shellStdinIsProgram(command) {
		return ""
	}
	if len(stdin) > shellExecMaxCommandBytes {
		return fmt.Sprintf(
			"stdin program too long (%d bytes; max %d). Put the program in write_sandbox_file, then run it with shell_exec",
			len(stdin), shellExecMaxCommandBytes,
		)
	}
	if reason := checkShellExecBlacklist(stdin); reason != "" {
		return fmt.Sprintf("command rejected by shell_exec safety guard: %s", reason)
	}
	return ""
}

func shellStdinIsProgram(command string) bool {
	fields := strings.Fields(command)
	i := 0
	for i < len(fields) && strings.Contains(fields[i], "=") && !strings.HasPrefix(fields[i], "-") {
		i++
	}
	if i >= len(fields) {
		return false
	}
	bin := path.Base(fields[i])
	args := fields[i+1:]
	switch {
	case bin == "bash" || bin == "sh" || bin == "dash" || bin == "zsh" || bin == "ksh" || bin == "ash":
		return interpreterReadsStdin(args, map[string]bool{"-c": true})
	case bin == "python" || bin == "python2" || bin == "python3" || bin == "pypy" || bin == "pypy3" ||
		strings.HasPrefix(bin, "python3.") || strings.HasPrefix(bin, "python2."):
		return interpreterReadsStdin(args, map[string]bool{"-c": true, "-m": true})
	case bin == "node" || bin == "nodejs":
		return interpreterReadsStdin(args, map[string]bool{"-e": true, "--eval": true, "-p": true, "--print": true})
	case bin == "perl" || bin == "ruby":
		return interpreterReadsStdin(args, map[string]bool{"-e": true, "-c": true})
	default:
		return false
	}
}

func interpreterReadsStdin(args []string, programFlags map[string]bool) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return i+1 >= len(args)
		}
		if programFlags[arg] {
			return false
		}
		if strings.HasPrefix(arg, "-") && len(arg) > 2 && programFlags[arg[:2]] {
			return false
		}
		if arg == "-" || arg == "-s" {
			return true
		}
		if !strings.HasPrefix(arg, "-") {
			return false
		}
	}
	return true
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
