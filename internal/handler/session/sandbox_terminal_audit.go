package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	terminalAuditMarkerLead      = "\x1b]6973;"
	terminalAuditMarkerEnd       = byte('\a')
	terminalAuditMaxMarkerBytes  = 64 << 10
	terminalAuditMaxCommandBytes = 4096
	terminalAuditQueueSize       = 128
	terminalAuditWriteTimeout    = 3 * time.Second
	terminalAuditCloseTimeout    = 2 * time.Second
)

// terminalAuditPromptCommand runs after Bash completes a command. It captures
// $? first, suppresses the initial prompt, and emits the last committed Bash
// history entry in a private OSC frame. The bridge authenticates and strips
// that frame before any bytes reach xterm.
const terminalAuditPromptCommand = `__wk_rc=$?; __wk_n=${HISTCMD:-0}; ` +
	`if [ "${__WK_AUDIT_READY:-0}" = 1 ] && [ "$__wk_n" != "${__WK_AUDIT_LAST:-}" ]; then ` +
	`__wk_cmd=$(builtin history 1); ` +
	`__wk_b64=$(printf "%s" "$__wk_cmd" | base64 | tr -d "\n"); ` +
	`printf "\033]6973;%s;%s;%s\007" "$WEKNORA_AUDIT_TOKEN" "$__wk_rc" "$__wk_b64"; ` +
	`fi; __WK_AUDIT_READY=1; __WK_AUDIT_LAST=$__wk_n; ` +
	`unset __wk_cmd __wk_b64 __wk_n __wk_rc`

func terminalAuditEnvironment(token string) map[string]string {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	return map[string]string{
		"WEKNORA_AUDIT_TOKEN": token,
		"HISTCONTROL":         "",
		"HISTIGNORE":          "",
		"PROMPT_COMMAND":      terminalAuditPromptCommand,
	}
}

type terminalAuditMarkerFilter struct {
	prefix    []byte
	pending   []byte
	onCommand func(exitCode int, command string)
}

func newTerminalAuditMarkerFilter(
	token string,
	onCommand func(exitCode int, command string),
) *terminalAuditMarkerFilter {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	return &terminalAuditMarkerFilter{
		prefix:    []byte(terminalAuditMarkerLead + token + ";"),
		onCommand: onCommand,
	}
}

// Consume removes authenticated audit markers while preserving all ordinary
// PTY bytes exactly. Provider stream chunks may split anywhere inside an OSC
// frame, so incomplete prefix/payload bytes remain bounded in pending.
func (f *terminalAuditMarkerFilter) Consume(chunk []byte) []byte {
	if f == nil || len(chunk) == 0 {
		return chunk
	}
	f.pending = append(f.pending, chunk...)
	out := make([]byte, 0, len(f.pending))
	for len(f.pending) > 0 {
		index := bytes.Index(f.pending, f.prefix)
		if index < 0 {
			keep := longestTerminalAuditPrefixSuffix(f.pending, f.prefix)
			out = append(out, f.pending[:len(f.pending)-keep]...)
			f.pending = append(f.pending[:0], f.pending[len(f.pending)-keep:]...)
			break
		}
		out = append(out, f.pending[:index]...)
		f.pending = f.pending[index:]
		payloadStart := len(f.prefix)
		end := bytes.IndexByte(f.pending[payloadStart:], terminalAuditMarkerEnd)
		if end < 0 {
			if len(f.pending) > terminalAuditMaxMarkerBytes {
				// A matching private prefix with no terminator is malformed. Drop
				// it rather than buffering forever or leaking the marker token.
				f.pending = f.pending[:0]
			}
			break
		}
		end += payloadStart
		f.acceptPayload(f.pending[payloadStart:end])
		f.pending = f.pending[end+1:]
	}
	return out
}

// Flush returns any ordinary suffix retained for split-prefix detection. A
// truncated private frame is discarded so its token cannot reach the browser.
func (f *terminalAuditMarkerFilter) Flush() []byte {
	if f == nil || len(f.pending) == 0 {
		return nil
	}
	defer func() { f.pending = nil }()
	if bytes.HasPrefix(f.pending, []byte(terminalAuditMarkerLead)) {
		return nil
	}
	return append([]byte(nil), f.pending...)
}

func (f *terminalAuditMarkerFilter) acceptPayload(payload []byte) {
	parts := bytes.SplitN(payload, []byte{';'}, 2)
	if len(parts) != 2 {
		return
	}
	exitCode, err := strconv.Atoi(string(parts[0]))
	if err != nil || exitCode < 0 || exitCode > 255 {
		return
	}
	raw, err := base64.StdEncoding.DecodeString(string(parts[1]))
	if err != nil {
		return
	}
	command := sanitizeTerminalAuditCommand(string(raw))
	if command != "" && f.onCommand != nil {
		f.onCommand(exitCode, command)
	}
}

func longestTerminalAuditPrefixSuffix(data, prefix []byte) int {
	limit := len(prefix) - 1
	if len(data) < limit {
		limit = len(data)
	}
	for size := limit; size > 0; size-- {
		if bytes.Equal(data[len(data)-size:], prefix[:size]) {
			return size
		}
	}
	return 0
}

var (
	terminalAuditHistoryPrefix = regexp.MustCompile(`^[\t ]*[0-9]+[\t ]+`)
	// The name-run before and after the keyword is allowed to be empty so a
	// bare `PASSWORD=`, `TOKEN=`, or `SECRET=` is redacted too — not only
	// prefixed forms like `API_TOKEN=`. Over-matching a name that merely
	// contains a keyword is the safe direction for a scrubber.
	terminalAuditEnvSecret = regexp.MustCompile(
		`(?i)(\b(?:export[\t ]+)?[A-Za-z0-9_]*` +
			`(?:password|passwd|passphrase|pwd|secret|token|api[_-]?key|` +
			`access[_-]?key|private[_-]?key|credential)` +
			`[A-Za-z0-9_]*[\t ]*=[\t ]*)("[^"]*"|'[^']*'|[^\t ;|&]+)`,
	)
	// Flag names may carry a prefix (`--http-password`, `--db-token`), so the
	// keyword is matched anywhere in the flag name rather than at its start.
	terminalAuditFlagSecret = regexp.MustCompile(
		`(?i)((?:^|[\t \n])--?[A-Za-z0-9_-]*` +
			`(?:password|passwd|passphrase|pwd|secret|token|api[_-]?key|` +
			`access[_-]?key|private[_-]?key|client[_-]?secret|credential)` +
			`[A-Za-z0-9_-]*(?:[=\t ]+))("[^"]*"|'[^']*'|[^\t ;|&]+)`,
	)
	// Any scheme is redacted, not just bearer/basic: `Authorization: token
	// <pat>` (GitHub) and `Authorization: ApiKey <key>` are equally secret.
	terminalAuditAuthorization = regexp.MustCompile(
		`(?i)(authorization[\t ]*:[\t ]*(?:[A-Za-z][A-Za-z0-9._~+/-]*[\t ]+)?)` +
			`("[^"]*"|'[^']*'|[^"'\t ;|&]+)`,
	)
	terminalAuditSecretHeader = regexp.MustCompile(
		`(?i)((?:x-api-key|api-key|x-auth-token|x-access-token)[\t ]*:[\t ]*)` +
			`("[^"]*"|'[^']*'|[^"'\t ;|&]+)`,
	)
	terminalAuditUserPassword = regexp.MustCompile(
		`(?i)((?:^|[\t \n])(?:-u|--user|-p)(?:[=\t ]+)?)("[^"]*"|'[^']*'|[^\t ;|&]+)`,
	)
	terminalAuditBearer      = regexp.MustCompile(`(?i)(\bbearer[\t ]+)([A-Za-z0-9._~+/=-]+)`)
	terminalAuditURLPassword = regexp.MustCompile(`(?i)(://[^:/@\s]+:)([^@\s"']+)(@)`)
)

func sanitizeTerminalAuditCommand(raw string) string {
	command := terminalAuditHistoryPrefix.ReplaceAllString(raw, "")
	command = strings.ReplaceAll(command, "\r\n", "\n")
	command = strings.ReplaceAll(command, "\r", "\n")
	command = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r >= 0x20 && r != 0x7f {
			return r
		}
		return ' '
	}, command)
	command = strings.TrimSpace(command)
	command = terminalAuditEnvSecret.ReplaceAllString(command, `${1}[REDACTED]`)
	command = terminalAuditFlagSecret.ReplaceAllString(command, `${1}[REDACTED]`)
	command = terminalAuditAuthorization.ReplaceAllString(command, `${1}[REDACTED]`)
	command = terminalAuditSecretHeader.ReplaceAllString(command, `${1}[REDACTED]`)
	command = terminalAuditUserPassword.ReplaceAllString(command, `${1}[REDACTED]`)
	command = terminalAuditBearer.ReplaceAllString(command, `${1}[REDACTED]`)
	command = terminalAuditURLPassword.ReplaceAllString(command, `${1}[REDACTED]${3}`)
	return truncateTerminalAuditCommand(command)
}

func truncateTerminalAuditCommand(command string) string {
	if len(command) <= terminalAuditMaxCommandBytes {
		return command
	}
	limit := terminalAuditMaxCommandBytes - len("…")
	for limit > 0 && !utf8.ValidString(command[:limit]) {
		limit--
	}
	return command[:limit] + "…"
}

type terminalAuditRecord struct {
	command  string
	exitCode int
}

// terminalAuditRecorder serializes native AuditLog writes off the PTY output
// path. Its bounded queue protects both the terminal and audit storage during
// a command storm; terminal operation never depends on audit availability.
type terminalAuditRecorder struct {
	ctx    context.Context
	cancel context.CancelFunc
	audit  interfaces.AuditLogService

	tenantID  uint64
	actorID   string
	actorRole string
	sessionID string
	backend   string
	ptyID     uint32

	mu        sync.Mutex
	closed    bool
	queue     chan terminalAuditRecord
	done      chan struct{}
	closeWait time.Duration
	closeOnce sync.Once
}

func newTerminalAuditRecorder(
	parent context.Context,
	audit interfaces.AuditLogService,
	tenantID uint64,
	actorID, actorRole, sessionID, backend string,
	ptyID uint32,
) *terminalAuditRecorder {
	if audit == nil || tenantID == 0 || strings.TrimSpace(actorID) == "" || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	recorder := &terminalAuditRecorder{
		ctx: ctx, cancel: cancel, audit: audit,
		tenantID: tenantID, actorID: actorID, actorRole: actorRole,
		sessionID: sessionID, backend: backend, ptyID: ptyID,
		queue:     make(chan terminalAuditRecord, terminalAuditQueueSize),
		done:      make(chan struct{}),
		closeWait: terminalAuditCloseTimeout,
	}
	go recorder.run()
	return recorder
}

func (r *terminalAuditRecorder) Record(exitCode int, command string) {
	if r == nil || command == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	select {
	case r.queue <- terminalAuditRecord{command: command, exitCode: exitCode}:
	default:
		logger.Warnf(r.ctx, "[sandbox-terminal-audit] queue full session=%s; dropping command", r.sessionID)
	}
}

func (r *terminalAuditRecorder) run() {
	defer close(r.done)
	for {
		select {
		case <-r.ctx.Done():
			return
		case record, ok := <-r.queue:
			if !ok {
				return
			}
			r.write(record)
		}
	}
}

func (r *terminalAuditRecorder) write(record terminalAuditRecord) {
	details, err := json.Marshal(map[string]any{
		"command":   record.command,
		"exit_code": record.exitCode,
		"backend":   r.backend,
		"pty_id":    r.ptyID,
	})
	if err != nil {
		return
	}
	outcome := types.AuditOutcomeSuccess
	if record.exitCode != 0 {
		outcome = types.AuditOutcomeFailed
	}
	ctx, cancel := context.WithTimeout(r.ctx, terminalAuditWriteTimeout)
	defer cancel()
	if err := r.audit.Log(ctx, &types.AuditLog{
		TenantID:      r.tenantID,
		ActorUserID:   r.actorID,
		ActorRole:     r.actorRole,
		Action:        types.AuditActionSandboxTerminalCommand,
		TargetType:    "sandbox_session",
		TargetID:      r.sessionID,
		RequestPath:   "/api/v1/sessions/:session_id/sandbox/terminal",
		RequestMethod: "GET",
		Outcome:       outcome,
		Details:       types.JSON(details),
	}); err != nil {
		logger.Warnf(r.ctx, "[sandbox-terminal-audit] write failed session=%s: %v", r.sessionID, err)
	}
}

func (r *terminalAuditRecorder) Close() {
	if r == nil {
		return
	}
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		close(r.queue)
		r.mu.Unlock()

		wait := r.closeWait
		if wait <= 0 {
			wait = terminalAuditCloseTimeout
		}
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-r.done:
		case <-timer.C:
			r.cancel()
			return
		}
		r.cancel()
	})
}
