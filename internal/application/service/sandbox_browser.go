package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// BrowserCommand payloads never accept shell fragments or a remote endpoint. The
// browser lives in the authenticated session's pinned sandbox and uses a Unix
// socket, so no provider port, CDP endpoint or traffic token reaches the client.
type BrowserCommand struct {
	Phase    string  `json:"phase,omitempty"`
	Action   string  `json:"action"`
	Token    string  `json:"token,omitempty"`
	URL      string  `json:"url,omitempty"`
	X        float64 `json:"x,omitempty"`
	Y        float64 `json:"y,omitempty"`
	Delta    float64 `json:"delta,omitempty"`
	Text     string  `json:"text,omitempty"`
	Key      string  `json:"key,omitempty"`
	Revision int     `json:"revision"`
}

// Validate checks supported actions and bounds browser command input.
func (cmd BrowserCommand) Validate() error {
	if cmd.Action == "pointer" {
		switch cmd.Phase {
		case "down", "move", "up", "cancel":
		default:
			return fmt.Errorf("invalid pointer phase")
		}
		if math.IsNaN(cmd.X) || math.IsInf(cmd.X, 0) || math.IsNaN(cmd.Y) || math.IsInf(cmd.Y, 0) || cmd.X < 0 ||
			cmd.X > 1280 ||
			cmd.Y < 0 ||
			cmd.Y > 800 {
			return fmt.Errorf("invalid pointer coordinate")
		}
	}
	switch cmd.Action {
	case "frame",
		"start",
		"acquire",
		"release",
		"heartbeat",
		"open",
		"back",
		"click",
		"scroll",
		"type",
		"press",
		"pointer":
	default:
		return fmt.Errorf("unsupported browser action")
	}
	if len(cmd.Token) > 128 || len(cmd.URL) > 8192 || len(cmd.Text) > 40000 || len(cmd.Key) > 32 {
		return fmt.Errorf("browser command exceeds limits")
	}
	return nil
}

// BrowserCommand executes a command in the live session without creating or resuming its sandbox.
func (s *SandboxTerminalService) BrowserCommand(
	ctx context.Context,
	sessionID string,
	command BrowserCommand,
) (json.RawMessage, error) {
	if err := command.Validate(); err != nil {
		return nil, err
	}
	mgr, _, err := s.resolveSessionManager(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	live, ok := mgr.(sandbox.SessionLiveShellExecutor)
	if !ok {
		return nil, fmt.Errorf("sandbox does not support browser preview")
	}
	payload, err := json.Marshal(command)
	if err != nil {
		return nil, err
	}
	root := "/opt/weknora/tenant/skills/browser"
	if reader, ok := mgr.(sandbox.SessionBuiltinSkillsReader); ok {
		manifest, err := reader.BuiltinSkills(ctx, sessionID)
		if err == nil {
			for _, entry := range builtin.CompatibleEntries(manifest) {
				if entry.Name == "browser" {
					root = builtin.ImageRoot + "/browser"
				}
			}
		}
	}
	// A workspace-installed controller keeps precedence over a same-named
	// preloaded one. Both paths are fixed server-side.
	chooseRoot := "root=" + sandbox.ShellQuote(root) + "; "
	if root != "/opt/weknora/tenant/skills/browser" {
		chooseRoot += "if [ -x /opt/weknora/tenant/skills/browser/.venv/bin/python ] && " +
			"[ -f /opt/weknora/tenant/skills/browser/scripts/browser.py ]; " +
			"then root=/opt/weknora/tenant/skills/browser; fi; "
	}
	root = `"$root"`
	cmd := chooseRoot + "if [ -x " + root + "/.venv/bin/python ] && [ -f " + root + "/scripts/browser.py ]; then " +
		root + "/.venv/bin/python " + root + "/scripts/browser.py --ui-request " +
		sandbox.ShellQuote(base64.StdEncoding.EncodeToString(payload)) +
		`; else printf '%s\n' '{"ok":false,"state":"unavailable",` +
		`"error":"This sandbox does not provide the browser skill."}'; fi`
	result, err := live.ExecLiveSessionCommand(ctx, sessionID, cmd, 40*time.Second)
	if err != nil {
		return nil, err
	}
	if result == nil || len(result.Stdout) > 4*1024*1024 || !json.Valid([]byte(result.Stdout)) {
		return nil, fmt.Errorf("browser controller returned an invalid response")
	}
	// Structured controller errors (including expired control leases) remain
	// visible to the UI; arbitrary shell stderr never becomes an HTML response.
	return json.RawMessage(result.Stdout), nil
}

// StartSessionBrowser is the explicit user action. Background frame/heartbeat
// requests stay on BrowserCommand, which never creates or resumes a sandbox.
func (s *SandboxTerminalService) StartSessionBrowser(
	ctx context.Context,
	sessionID, configID string,
	command BrowserCommand,
) (json.RawMessage, error) {
	if command.Action != "start" {
		return nil, fmt.Errorf("expected browser start command")
	}
	result, err := s.BrowserCommand(ctx, sessionID, command)
	if !errors.Is(err, sandbox.ErrNoLiveSessionSandbox) && !errors.Is(err, sandbox.ErrSandboxPaused) {
		return result, err
	}
	mgr, _, resolveErr := s.resolveSessionManager(ctx, sessionID)
	if errors.Is(resolveErr, sandbox.ErrNoLiveSessionSandbox) && configID != "" {
		tenantID, _ := types.TenantIDFromContext(ctx)
		mgr, _, resolveErr = resolveSandboxForExecution(
			ctx,
			s.resolver,
			s.fallback,
			s.pinner,
			tenantID,
			sessionID,
			configID,
			s.policy,
		)
	}
	if resolveErr != nil {
		return nil, resolveErr
	}
	if mgr == nil || mgr.GetType() == sandbox.SandboxTypeDisabled {
		return nil, sandbox.ErrNoLiveSessionSandbox
	}
	if err := s.provisionOnManager(ctx, mgr, sessionID); err != nil {
		return nil, err
	}
	return s.BrowserCommand(ctx, sessionID, command)
}
