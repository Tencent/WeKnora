package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

const shellExecAsyncDescription = `Run a shell command that may take longer than a couple of minutes: builds, test
suites, data processing, large downloads, install software. shell_exec is killed at 600s; this has no
time limit. It first waits up to yield_time_ms (default 10000, max 30000): if the
command finishes by then you get its result directly, like shell_exec. Otherwise you
get a work_id plus the output so far, and the command keeps running.
Use this instead of &, nohup, setsid or disown inside shell_exec.
Do not pipe the command through tail or head. The job log records only what the
command itself writes, and tail waits until the command exits, so the log stays
empty until then. Head and tail of that log are kept for you.
Run non-interactively. There is no TTY and stdin stays closed, so a prompt waits
forever. Pass the flags that skip questions (apt-get -y, DEBIAN_FRONTEND=noninteractive,
--yes, --non-interactive). Do not use editors, pagers, or commands that read a reply.
Arguments: same as shell_exec, plus description (short label shown to the user) and
yield_time_ms.`

// shellExecAsyncOutputLimit keeps a finished job's stdout, stderr, original
// command and JSON envelope intact, including six-character control-byte escapes.
// The generic 24000-rune cap would split that envelope.
const shellExecAsyncOutputLimit = ((16+8)*1024+shellExecMaxCommandBytes)*6 + 8*1024

// ShellExecAsyncInput is the model-facing argument object.
type ShellExecAsyncInput struct {
	Command     string            `json:"command" jsonschema:"The shell one-liner to run"`
	WorkDir     string            `json:"work_dir,omitempty" jsonschema:"Working directory; defaults like shell_exec"`
	Stdin       string            `json:"stdin,omitempty" jsonschema:"Text written once at start, max 65536 bytes"`
	Env         map[string]string `json:"env,omitempty" jsonschema:"Extra environment variables"`
	SkillName   string            `json:"skill_name,omitempty" jsonschema:"Pull this skill's env into the command"`
	Description string            `json:"description" jsonschema:"Short label shown to the user"`
	YieldTimeMS int               `json:"yield_time_ms,omitempty" jsonschema:"Wait ms; default 10000, max 30000"`
}

// ShellJobSubmitter starts one shell job. *service.ShellJobService implements it.
// EndShellJobRun is optional and checked separately at Cleanup.
type ShellJobSubmitter interface {
	SubmitShellJob(ctx context.Context, s interfaces.SessionRef, req ShellJobSubmitRequest) (ShellJobSubmission, error)
}

// ShellExecAsyncTool validates a command the same way shell_exec does, then
// hands it to ShellJobSubmitter. It does not run the command itself.
type ShellExecAsyncTool struct {
	BaseTool
	sessionBound
	submitter     ShellJobSubmitter
	executor      SandboxCommandExecutor
	envResolver   skills.SkillEnvResolver
	skillEnv      *skills.Manager
	envCapture    SkillEnvCapture
	backgroundRef interfaces.SessionRef
	backgroundRun string
	marks         interface{ MarkStarted(string) }
}

var (
	_ types.Tool                                  = (*ShellExecAsyncTool)(nil)
	_ interface{ Cleanup(context.Context) error } = (*ShellExecAsyncTool)(nil)
)

func init() {
	backgroundSubmissionTools = append(backgroundSubmissionTools, ToolShellExecAsync)
}

// NewShellExecAsyncTool builds shell_exec_async. submitter starts a background
// job, executor runs a command that finishes inside the yield window, and
// envResolver supplies skill credentials.
func NewShellExecAsyncTool(submitter ShellJobSubmitter, executor SandboxCommandExecutor,
	envResolver skills.SkillEnvResolver,
) *ShellExecAsyncTool {
	return &ShellExecAsyncTool{
		BaseTool: NewBaseTool(
			ToolShellExecAsync, shellExecAsyncDescription, utils.GenerateSchema[ShellExecAsyncInput](),
		),
		submitter:   submitter,
		executor:    executor,
		envResolver: envResolver,
	}
}

// WithSkillEnvironment attaches the skill runtime used by shell_exec.
func (t *ShellExecAsyncTool) WithSkillEnvironment(manager *skills.Manager) *ShellExecAsyncTool {
	if t == nil {
		return nil
	}
	t.skillEnv = manager
	return t
}

// WithEnvCapture attaches the same credential write-back shell_exec uses.
// A nil hook does nothing. Values stay in memory until the job succeeds.
func (t *ShellExecAsyncTool) WithEnvCapture(capture SkillEnvCapture) *ShellExecAsyncTool {
	if t != nil {
		t.envCapture = capture
	}
	return t
}

// BindBackgroundRun records the run that owns this tool. A handle that is still
// running is marked started on that run; a direct result is not. Cursor expiry
// uses the same run id at Cleanup.
func (t *ShellExecAsyncTool) BindBackgroundRun(
	s interfaces.SessionRef, runID string, marks interface{ MarkStarted(string) },
) {
	if t == nil {
		return
	}
	t.backgroundRef, t.backgroundRun, t.marks = s, runID, marks
}

// Cleanup tells the submitter the run ended so its output cursors can expire.
func (t *ShellExecAsyncTool) Cleanup(context.Context) error {
	if t == nil || t.backgroundRun == "" || t.submitter == nil {
		return nil
	}
	ender, ok := t.submitter.(interface {
		EndShellJobRun(interfaces.SessionRef, string)
	})
	if ok {
		ender.EndShellJobRun(t.backgroundRef, t.backgroundRun)
	}
	return nil
}

// OutputLimitChars is the rune budget for the model-facing JSON envelope.
func (t *ShellExecAsyncTool) OutputLimitChars(json.RawMessage) int {
	return shellExecAsyncOutputLimit
}

// Execute validates the command, wraps stdin the way shell_exec does, and submits.
func (t *ShellExecAsyncTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	logger.Infof(ctx, "[Tool][ShellExecAsync] Execute started")

	var input ShellExecAsyncInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{Success: false, Error: "failed to parse args: " + err.Error()}, nil
	}
	if t.submitter == nil {
		return &types.ToolResult{Success: false, Error: "shell_exec_async is not available in this deployment"}, nil
	}
	if input.SkillName != "" && t.skillEnv == nil {
		return &types.ToolResult{Success: false, Error: "no skill environment is available for this call; " +
			"omit skill_name for system commands"}, nil
	}
	if len(input.Stdin) > 65536 {
		return &types.ToolResult{Success: false, Error: "stdin exceeds 65536 bytes; " +
			"write the input to a workspace file and redirect from it"}, nil
	}
	command := strings.TrimSpace(input.Command)
	if command == "" {
		return &types.ToolResult{Success: false, Error: "command is required"}, nil
	}
	if len(command) > shellExecMaxCommandBytes {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf(
			"command too long (%d bytes; max %d). Put the file in write_sandbox_file, then run it",
			len(command), shellExecMaxCommandBytes)}, nil
	}
	if reason := checkShellExecBlacklist(command); reason != "" {
		logger.Warnf(ctx, "[Tool][ShellExecAsync] rejected by blacklist: %s command=%q",
			reason, maskCommandAssignments(command))
		return &types.ToolResult{
			Success: false,
			Error:   "command rejected by shell_exec safety guard: " + reason,
		}, nil
	}
	if reason := rejectExecutableStdin(command, input.Stdin); reason != "" {
		logger.Warnf(ctx, "[Tool][ShellExecAsync] rejected executable stdin: %s command=%q",
			reason, maskCommandAssignments(command))
		return &types.ToolResult{Success: false, Error: reason}, nil
	}
	sessionID := resolveSessionID(ctx)
	if sessionID == "" {
		return &types.ToolResult{Success: false, Error: "no session ID in context"}, nil
	}
	ref, refErr := t.submitRef(ctx, sessionID)
	if refErr != nil {
		return refErr, nil
	}
	layoutCtx, layoutSpan := langfuse.GetManager().StartChildSpan(ctx, langfuse.SpanOptions{
		Name:  "shelljob.layout",
		Input: map[string]interface{}{"session_id": sessionID},
	})
	layout, layoutErr := executeWorkspaceLayout(layoutCtx, sessionID, t.executor)
	layoutSpan.Finish(spanToolResult(layoutErr, map[string]interface{}{"root": layout.Root}), nil, nil)
	if layoutErr != nil {
		return layoutErr, nil
	}
	workDir, dirErr := resolveAsyncWorkDir(layout, input.WorkDir)
	if dirErr != nil {
		return dirErr, nil
	}
	baselineCtx, baselineSpan := langfuse.GetManager().StartChildSpan(ctx, langfuse.SpanOptions{
		Name:  "shelljob.output_baseline",
		Input: map[string]interface{}{"session_id": sessionID, "output_dir": layoutOutputDir(layout)},
	})
	outputDir, outputSeen, outputBefore := asyncOutputBaseline(baselineCtx, t.executor, sessionID, layout)
	baselineSpan.Finish(map[string]interface{}{"seen": outputSeen, "files": len(outputBefore)}, nil, nil)

	envCtx, envSpan := langfuse.GetManager().StartChildSpan(ctx, langfuse.SpanOptions{
		Name:  "shelljob.env_resolve",
		Input: map[string]interface{}{"skill_name": input.SkillName},
	})
	env, supplied, envErr := t.resolveAsyncEnv(envCtx, input, command)
	envSpan.Finish(spanToolResult(envErr, nil), nil, nil)
	if envErr != nil {
		return envErr, nil
	}
	execCommand := command
	if input.SkillName != "" && t.skillEnv != nil {
		var prepErr error
		execCommand, env, prepErr = t.skillEnv.PrepareShellEnvironment(
			ctx, sessionID, input.SkillName, command, env)
		if prepErr != nil {
			return &types.ToolResult{Success: false, Error: prepErr.Error()}, nil
		}
	}
	if input.Stdin != "" {
		// Same wrapping as shell_exec: the payload is applied to the whole
		// command, and base64 keeps quotes and trailing newlines out of the shell.
		encoded := base64.StdEncoding.EncodeToString([]byte(input.Stdin))
		execCommand = "printf %s " + sandbox.ShellQuote(encoded) +
			" | base64 -d | /bin/bash --noprofile --norc -c " + sandbox.ShellQuote(execCommand)
	}
	// The watcher does not cd to meta.work_dir before starting: the command
	// itself is wrapped in its own cd, and the exec cwd is the session layout
	// root the manager picks (never "/"; shared machines reject that).
	// A brace group keeps a later ";" (skill PATH setup) from running when cd fails.
	// The seat pin sits inside the group, so a failed cd does not claim a screen.
	execCommand = commandInWorkDir(workDir, sandbox.WithSessionDisplay(execCommand))

	request := ShellJobSubmitRequest{
		Command:        execCommand,
		DisplayCommand: command,
		WorkDir:        workDir,
		Env:            env,
		Description:    shellJobLabel(input.Description, command),
		YieldTime:      time.Duration(clampYieldMS(input.YieldTimeMS)) * time.Millisecond,
		RunID:          t.backgroundRun,
		OutputDir:      outputDir,
		OutputSeen:     outputSeen,
		OutputBefore:   outputBefore,
	}
	if meta, ok := ToolExecFromContext(ctx); ok && meta != nil {
		request.ParentMessageID = meta.AssistantMessageID
		request.ToolCallID = meta.ToolCallID
	}
	if preview, finish := shellCommandOutput(ctx, command); preview != nil {
		request.OnOutput = preview
		defer finish()
	}

	submission, err := t.submitter.SubmitShellJob(ctx, ref, request)
	if err != nil {
		if errors.Is(err, interfaces.ErrActivityLimit) ||
			strings.Contains(err.Error(), interfaces.ErrActivityLimit.Error()) {
			return &types.ToolResult{Success: false, Error: "ERR_WORK_LIMIT: this session already runs " +
				"64 background shell jobs; work_wait for one to finish or work_cancel one first"}, nil
		}
		return &types.ToolResult{Success: false, Error: "shell_exec_async failed: " + err.Error()}, nil
	}
	// A direct result was already shown. Only a still-running handle belongs
	// in this run's "started" set.
	if submission.Background && t.marks != nil {
		t.marks.MarkStarted(submission.Result.ID)
	}
	t.captureSkillEnv(ctx, ref, submission, input.SkillName, supplied)
	body := shellJobResultMap(submission.Result)
	raw, err := json.Marshal(body)
	if err != nil {
		return &types.ToolResult{Success: false, Error: "encode shell job result: " + err.Error()}, nil
	}
	return &types.ToolResult{
		Success:     true,
		Output:      string(raw),
		OutputFiles: submission.Result.OutputFiles,
		Data:        map[string]interface{}{"shell_job": body},
	}, nil
}

func asyncOutputBaseline(
	ctx context.Context, executor SandboxCommandExecutor, sessionID string, layout sandbox.WorkspaceLayout,
) (dir string, seen bool, before []sandbox.OutputFileStamp) {
	dir = layoutOutputDir(layout)
	if dir == "" || executor == nil {
		return dir, false, nil
	}
	listed, ok := sandboxOutputSnapshot(ctx, executor, sessionID, dir)
	if !ok {
		return dir, false, nil
	}
	entries := make([]sandbox.RemoteDirEntry, 0, len(listed))
	for _, entry := range listed {
		entries = append(entries, entry)
	}
	stamps, ok := sandbox.StampsFromEntries(entries)
	if !ok {
		return dir, false, nil
	}
	return dir, true, stamps
}

func (t *ShellExecAsyncTool) submitRef(
	ctx context.Context, sessionID string,
) (interfaces.SessionRef, *types.ToolResult) {
	ref := t.backgroundRef
	if strings.TrimSpace(ref.SessionID) == "" {
		ref.SessionID = sessionID
	}
	if ref.TenantID != 0 {
		return ref, nil
	}
	tenantID, ok := types.SandboxTenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return interfaces.SessionRef{}, &types.ToolResult{Success: false, Error: "no session tenant in context"}
	}
	ref.TenantID = tenantID
	return ref, nil
}

func (t *ShellExecAsyncTool) captureSkillEnv(
	ctx context.Context, ref interfaces.SessionRef, submission ShellJobSubmission,
	skillName string, pairs map[string]string,
) {
	succeeded := submission.Result.State == interfaces.WorkStateSucceeded
	if !submission.Background {
		captureUsedSkillEnv(ctx, t.envCapture, skillName, pairs, succeeded)
		return
	}
	if t.envCapture == nil || len(pairs) == 0 || !sandbox.IsValidSkillName(strings.TrimSpace(skillName)) {
		return
	}
	holder, ok := t.submitter.(interface {
		HoldSkillEnvCapture(context.Context, interfaces.SessionRef, string, func(context.Context))
	})
	if !ok || submission.Result.ID == "" {
		return
	}
	name := strings.TrimSpace(skillName)
	held := copySkillPairs(pairs)
	capture := t.envCapture
	holder.HoldSkillEnvCapture(ctx, ref, submission.Result.ID, func(ctx context.Context) {
		captureUsedSkillEnv(ctx, capture, name, held, true)
	})
}

func copySkillPairs(pairs map[string]string) map[string]string {
	out := make(map[string]string, len(pairs))
	for name, value := range pairs {
		out[name] = value
	}
	return out
}

func (t *ShellExecAsyncTool) resolveAsyncEnv(
	ctx context.Context, input ShellExecAsyncInput, command string,
) (map[string]string, map[string]string, *types.ToolResult) {
	supplied := collectUsedSkillEnv(command, input.Env)
	env := input.Env
	if t.envResolver == nil {
		return env, supplied, nil
	}
	// Empty skill name still asks for the caller's config-wide variables,
	// matching shell_exec. Model-supplied env wins over resolved values.
	resolved, missing, err := t.envResolver.ResolveEnv(ctx, input.SkillName)
	if err != nil {
		return nil, nil, &types.ToolResult{Success: false, Error: fmt.Sprintf(
			"failed to resolve environment variables: %v", err)}
	}
	missing = stillMissing(missing, supplied)
	if len(missing) > 0 {
		return nil, nil, &types.ToolResult{Success: false, Error: fmt.Sprintf(
			"skill %q needs the environment variable(s) %s, which nobody has set yet. "+
				"Ask the user for them and pass them in this call's env, "+
				"or have them set the values under Settings → Sandbox secrets.",
			input.SkillName, strings.Join(missing, ", "))}
	}
	if len(resolved) > 0 && env == nil {
		env = make(map[string]string)
	}
	skills.ApplyResolvedEnv(env, resolved)
	// Stored names must not be written again, matching shell_exec.
	return env, dropResolvedNames(supplied, resolved), nil
}

func resolveAsyncWorkDir(layout sandbox.WorkspaceLayout, requested string) (string, *types.ToolResult) {
	root := strings.TrimSpace(layout.Root)
	if root == "" {
		root = sandbox.RemoteWorkspaceLayout().Root
	}
	workDir := strings.TrimSpace(requested)
	if workDir == "" {
		workDir = root
	}
	if !path.IsAbs(workDir) {
		workDir = path.Join(root, workDir)
	}
	clean := path.Clean(workDir)
	roots := asyncWorkDirRoots(layout)
	for _, allowed := range roots {
		if isUnderRoot(clean, allowed) {
			return clean, nil
		}
	}
	return "", &types.ToolResult{Success: false, Error: fmt.Sprintf(
		"work_dir %q is outside the allowed sandbox roots %s",
		requested, strings.Join(roots, ", "))}
}

func asyncWorkDirRoots(layout sandbox.WorkspaceLayout) []string {
	if !layout.IsHost() {
		return []string{"/"}
	}
	if len(layout.WriteRoots) > 0 {
		return layout.WriteRoots
	}
	if strings.TrimSpace(layout.Root) != "" {
		return []string{layout.Root}
	}
	return []string{"/"}
}

func shellJobResultMap(result interfaces.WorkResult) map[string]any {
	body := map[string]any{}
	switch payload := result.Payload.(type) {
	case map[string]any:
		for key, value := range payload {
			body[key] = value
		}
	case nil:
	default:
		raw, err := json.Marshal(payload)
		if err == nil {
			_ = json.Unmarshal(raw, &body)
		}
	}
	if body == nil {
		body = map[string]any{}
	}
	if id, _ := body["work_id"].(string); id == "" {
		body["work_id"] = result.ID
	}
	if state, _ := body["state"].(string); state == "" {
		body["state"] = result.State
	}
	return body
}

func clampYieldMS(ms int) int {
	if ms <= 0 {
		return 10000
	}
	return min(max(ms, 250), 30000)
}

func commandInWorkDir(workDir, command string) string {
	if workDir == "" {
		return command
	}
	// A newline terminates comments and heredoc delimiters without changing
	// the command's shell grammar (an appended semicolon does not).
	return "cd " + sandbox.ShellQuote(workDir) + " && {\n" + command + "\n}"
}

func shellJobLabel(description, command string) string {
	if label := strings.TrimSpace(description); label != "" {
		return label
	}
	return command
}

// spanToolResult reports a *types.ToolResult failure on a span without the
// error channel: ToolResult is a tool-domain outcome, not a Go error, and
// keeping it in output keeps the span status clean.
func spanToolResult(result *types.ToolResult, ok map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for k, v := range ok {
		out[k] = v
	}
	if result != nil && !result.Success {
		out["error"] = result.Error
	}
	return out
}
