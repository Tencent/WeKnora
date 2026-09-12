package logger

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/sirupsen/logrus"
)

func newEntry(level logrus.Level, msg string, data logrus.Fields) *logrus.Entry {
	e := logrus.NewEntry(logrus.New())
	e.Time = time.Date(2026, 5, 21, 10, 20, 30, 123_000_000, time.UTC)
	e.Level = level
	e.Message = msg
	e.Data = data
	return e
}

func TestAnsiStripWriter(t *testing.T) {
	var buf strings.Builder
	w := &ansiStripWriter{w: &buf}
	in := []byte("\x1b[32mINFO\x1b[0m hello \x1b[31mERROR\x1b[0m")
	n, err := w.Write(in)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != len(in) {
		t.Fatalf("Write n = %d, want %d", n, len(in))
	}
	if got := buf.String(); got != "INFO hello ERROR" {
		t.Fatalf("stripped output = %q, want %q", got, "INFO hello ERROR")
	}
}

func TestFormat_DefaultModeUnchanged(t *testing.T) {
	f := &CustomFormatter{} // no template, no color
	entry := newEntry(logrus.InfoLevel, "hello", logrus.Fields{
		"request_id": "req-1",
		"caller":     "logger_test.go:1[Test]",
		"k1":         "v1",
	})

	out, err := f.Format(entry)
	if err != nil {
		t.Fatalf("Format returned error: %v", err)
	}
	got := string(out)

	if !strings.HasPrefix(got, "INFO ") {
		t.Errorf("expected INFO-prefixed default output, got %q", got)
	}
	for _, want := range []string{"2026-05-21 10:20:30.123", "req-1", "k1=v1", "logger_test.go:1[Test]", "hello"} {
		if !strings.Contains(got, want) {
			t.Errorf("default output missing %q: %s", want, got)
		}
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("default output should end with newline, got %q", got)
	}
}

func TestFormat_TemplateReplacesAllPlaceholders(t *testing.T) {
	f := &CustomFormatter{
		Template:     "[%d] %level %thread %logger %traceId | %msg",
		threadNeeded: true,
	}
	entry := newEntry(logrus.WarnLevel, "boom", logrus.Fields{
		"request_id": "req-42",
		"caller":     "x.go:9[Fn]",
		"extra":      "ok",
	})

	out, err := f.Format(entry)
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	got := string(out)

	for _, want := range []string{
		"[2026-05-21 10:20:30.123]",
		"WARNING",
		"x.go:9[Fn]",
		"req-42",
		"boom",
		"extra=ok",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("template output missing %q: %s", want, got)
		}
	}
	for _, placeholder := range []string{"%d", "%level", "%thread", "%logger", "%traceId", "%msg"} {
		if strings.Contains(got, placeholder) {
			t.Errorf("placeholder %q not substituted: %s", placeholder, got)
		}
	}
}

func TestFormat_TemplateGoroutineIDSkippedWhenNotReferenced(t *testing.T) {
	// 模板未引用 %thread 时，threadNeeded 应为 false，运行时不应取 goroutine ID。
	// 这里通过观察输出中不含数字-only goroutine ID 段来间接验证；更重要的是确保不 panic。
	f := &CustomFormatter{
		Template:     "[%d] %level | %msg",
		threadNeeded: false,
	}
	entry := newEntry(logrus.InfoLevel, "no-thread", nil)
	if _, err := f.Format(entry); err != nil {
		t.Fatalf("Format error: %v", err)
	}
}

// TestFormat_ColorDoesNotPolluteMessage 是修复 colorize 误染 bug 的回归测试。
// 旧实现对整行做 ReplaceAll(line, "INFO", colored)，会把消息正文里的 "INFO"
// 一并染色；新实现只在 %level 替换位置注入颜色。
func TestFormat_ColorDoesNotPolluteMessage(t *testing.T) {
	f := &CustomFormatter{
		ForceColor:   true,
		Template:     "%level | %msg",
		threadNeeded: false,
	}
	entry := newEntry(logrus.InfoLevel, "user INFO loaded", nil)

	out, err := f.Format(entry)
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	got := string(out)

	// 输出中 ANSI 序列总数应恰好为 2（开头一对 color + reset），
	// 而非旧实现下消息里的 "INFO" 也被替换导致出现 4 段。
	const ansiOpen = "\033[32m" // green for INFO
	const ansiReset = "\033[0m"
	if strings.Count(got, ansiOpen) != 1 {
		t.Errorf("expected exactly 1 green-open sequence, got %d in %q", strings.Count(got, ansiOpen), got)
	}
	if strings.Count(got, ansiReset) != 1 {
		t.Errorf("expected exactly 1 reset sequence, got %d in %q", strings.Count(got, ansiReset), got)
	}
	// 消息正文中的 "INFO" 字面串应保持未染色（其前驱字符不是 ANSI 开头）。
	idx := strings.Index(got, "user INFO loaded")
	if idx < 0 {
		t.Fatalf("message body not found verbatim in output: %q", got)
	}
}

// TestFormat_TemplateNoCascadingReplace 验证使用 NewReplacer 单趟替换，
// 字段值里含有占位符字面串（例如 traceId 值为 "%msg"）时不会被二次替换。
func TestFormat_TemplateNoCascadingReplace(t *testing.T) {
	f := &CustomFormatter{
		Template:     "%traceId>%msg",
		threadNeeded: false,
	}
	entry := newEntry(logrus.InfoLevel, "actual-msg", logrus.Fields{
		"request_id": "%msg", // 恶意/巧合的字段值
	})

	out, err := f.Format(entry)
	if err != nil {
		t.Fatalf("Format error: %v", err)
	}
	got := strings.TrimRight(string(out), "\n")
	want := "%msg>actual-msg"
	if got != want {
		t.Errorf("cascading-replace regression: got %q, want %q", got, want)
	}
}

func TestLevelColorFor(t *testing.T) {
	cases := map[logrus.Level]string{
		logrus.DebugLevel: colorCyan,
		logrus.InfoLevel:  colorGreen,
		logrus.WarnLevel:  colorYellow,
		logrus.ErrorLevel: colorRed,
		logrus.FatalLevel: colorPurple,
		logrus.TraceLevel: "",
	}
	for lvl, want := range cases {
		if got := levelColorFor(lvl); got != want {
			t.Errorf("levelColorFor(%v) = %q, want %q", lvl, got, want)
		}
	}
}

func TestCloneContextPreservesPrincipal(t *testing.T) {
	t.Parallel()

	ctx := types.WithPrincipal(context.Background(), types.EmbedSessionPrincipal(10000, "ch1", "sess1"))
	cloned := CloneContext(ctx)

	if got := types.SessionOwnerIDFromContext(cloned); got != "embed_session:10000:ch1:sess1" {
		t.Fatalf("SessionOwnerIDFromContext(cloned) = %q", got)
	}
}

func TestCloneContextPreservesTenantAPIKeyScope(t *testing.T) {
	t.Parallel()

	want := types.TenantAPIKeyScope{
		KeyID:            7,
		KnowledgeBaseIDs: types.StringArray{"kb-1"},
	}
	ctx := types.WithTenantAPIKeyScope(context.Background(), want)
	cloned := CloneContext(ctx)

	got, ok := types.TenantAPIKeyScopeFromContext(cloned)
	if !ok {
		t.Fatal("TenantAPIKeyScopeFromContext(cloned) = false, want true")
	}
	if got.KeyID != want.KeyID || !got.AllowsKnowledgeBase("kb-1") || got.AllowsKnowledgeBase("kb-2") {
		t.Fatalf("cloned scope = %#v, want key_id=7 scoped to kb-1", got)
	}
}

// setupSSEStream builds its async context through CloneContext, so dropping
// this key here would silently re-key every session→sandbox binding onto the
// tenant a shared agent borrowed — stranding the MicroVM at session deletion.
func TestCloneContextPreservesSandboxTenantID(t *testing.T) {
	t.Parallel()

	const sessionOwner, agentOwner = uint64(7), uint64(99)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, agentOwner)
	ctx = types.WithSandboxTenantID(ctx, sessionOwner)
	cloned := CloneContext(ctx)

	got, ok := types.SandboxTenantIDFromContext(cloned)
	if !ok || got != sessionOwner {
		t.Fatalf("SandboxTenantIDFromContext(cloned) = (%d, %v), want (%d, true)",
			got, ok, sessionOwner)
	}
}

// The agent-level memory opt-out is set before setupSSEStream clones, while
// extraction and the explicit remember route run after. Losing the key here
// would leave an agent that cannot read memory still writing to it.
func TestCloneContextPreservesTheAgentMemoryOptOut(t *testing.T) {
	t.Parallel()

	optedOut := CloneContext(types.WithMemoryDisabled(context.Background()))
	if types.MemoryAllowedForAgent(optedOut) {
		t.Fatal("MemoryAllowedForAgent(cloned) = true, want the opt-out to survive the clone")
	}

	// The absence of the marker still has to read as allowed, so agents that
	// never touched the switch keep their memory.
	if !types.MemoryAllowedForAgent(CloneContext(context.Background())) {
		t.Fatal("MemoryAllowedForAgent(cloned) = false for an unmarked context, want true")
	}
}

// Document ingestion detaches mid-flight: knowledge_create hands processChunks
// to a goroutine on a cloned context, and processChunks vectorises every chunk.
// If the background mark does not survive, that work stops being visible to the
// per-model concurrency governor and the ingestion storm it exists to contain
// runs past it, starving interactive chat.
func TestCloneContextKeepsBackgroundWorkThrottled(t *testing.T) {
	t.Parallel()

	if !types.IsBackgroundTask(CloneContext(types.WithBackgroundTask(context.Background()))) {
		t.Fatal("IsBackgroundTask(cloned) = false, want background work to stay throttled across a detach")
	}
	if types.IsBackgroundTask(CloneContext(context.Background())) {
		t.Fatal("IsBackgroundTask(cloned) = true for an unmarked context, want interactive work left ungated")
	}
}

// An IM bot has no live client to click "Authorize", so losing this mark makes
// the agent block on the OAuth wait for every unauthorized service instead of
// replying with its one-shot notice.
func TestCloneContextKeepsTheNonInteractiveOAuthMark(t *testing.T) {
	t.Parallel()

	if !types.IsMCPOAuthNonInteractive(CloneContext(types.WithMCPOAuthNonInteractive(context.Background()))) {
		t.Fatal("IsMCPOAuthNonInteractive(cloned) = false, want the mark to survive a detach")
	}
	if types.IsMCPOAuthNonInteractive(CloneContext(context.Background())) {
		t.Fatal("IsMCPOAuthNonInteractive(cloned) = true for an unmarked context, want interactive prompts to stay possible")
	}
}

func TestCloneContextWithoutTraceDropsTheChatTrace(t *testing.T) {
	t.Parallel()

	const tenantID uint64 = 42
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, types.LangfuseTraceContextKey, "trace-handle")

	kept := CloneContext(ctx)
	if kept.Value(types.LangfuseTraceContextKey) != "trace-handle" {
		t.Fatal("CloneContext must keep the Langfuse trace so same-request work stays nested")
	}

	detached := CloneContextWithoutTrace(ctx)
	if detached.Value(types.LangfuseTraceContextKey) != nil {
		t.Fatal("CloneContextWithoutTrace must drop the Langfuse trace so " +
			"background sandbox RPCs do not join the chat tree")
	}
	got, _ := detached.Value(types.TenantIDContextKey).(uint64)
	if got != tenantID {
		t.Fatalf("TenantID = %d, want %d (identity must survive the detach)", got, tenantID)
	}
}
