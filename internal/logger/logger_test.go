package logger

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
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

func TestLogFileRotationConfigDefaults(t *testing.T) {
	t.Setenv("LOG_FILE_MAX_SIZE_MB", "")
	t.Setenv("LOG_FILE_MAX_BACKUPS", "")
	t.Setenv("LOG_FILE_MAX_AGE_DAYS", "")
	t.Setenv("LOG_FILE_COMPRESS", "")

	config, warnings := resolveLogFileRotationConfigFromEnv()
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if config != (logFileRotationConfig{
		maxSizeMB:  defaultLogFileMaxSizeMB,
		maxBackups: defaultLogFileMaxBackups,
		maxAgeDays: defaultLogFileMaxAgeDays,
		compress:   defaultLogFileCompress,
	}) {
		t.Fatalf("config = %#v, want defaults", config)
	}
}

func TestLogFileRotationConfigUsesEnvironmentValues(t *testing.T) {
	t.Setenv("LOG_FILE_MAX_SIZE_MB", "17")
	t.Setenv("LOG_FILE_MAX_BACKUPS", "4")
	t.Setenv("LOG_FILE_MAX_AGE_DAYS", "9")
	t.Setenv("LOG_FILE_COMPRESS", "false")

	config, warnings := resolveLogFileRotationConfigFromEnv()
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if config != (logFileRotationConfig{maxSizeMB: 17, maxBackups: 4, maxAgeDays: 9, compress: false}) {
		t.Fatalf("config = %#v, want configured values", config)
	}
}

func TestLogFileRotationConfigRejectsUnsafeValues(t *testing.T) {
	t.Setenv("LOG_FILE_MAX_SIZE_MB", "0")
	t.Setenv("LOG_FILE_MAX_BACKUPS", "-1")
	t.Setenv("LOG_FILE_MAX_AGE_DAYS", "not-a-number")
	t.Setenv("LOG_FILE_COMPRESS", "sometimes")

	config, warnings := resolveLogFileRotationConfigFromEnv()
	if len(warnings) != 4 {
		t.Fatalf("warnings = %v, want four warnings", warnings)
	}
	if config != (logFileRotationConfig{
		maxSizeMB:  defaultLogFileMaxSizeMB,
		maxBackups: defaultLogFileMaxBackups,
		maxAgeDays: defaultLogFileMaxAgeDays,
		compress:   defaultLogFileCompress,
	}) {
		t.Fatalf("config = %#v, want defaults", config)
	}
}

func TestOpenLogFileUsesRotationConfig(t *testing.T) {
	config := logFileRotationConfig{maxSizeMB: 1, maxBackups: 2, maxAgeDays: 3, compress: false}
	writer, err := openLogFile(filepath.Join(t.TempDir(), "app.log"), config)
	if err != nil {
		t.Fatalf("openLogFile returned error: %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	rotationWriter, ok := writer.(*lumberjack.Logger)
	if !ok {
		t.Fatalf("writer type = %T, want *lumberjack.Logger", writer)
	}
	if rotationWriter.MaxSize != config.maxSizeMB || rotationWriter.MaxBackups != config.maxBackups ||
		rotationWriter.MaxAge != config.maxAgeDays || rotationWriter.Compress != config.compress {
		t.Fatalf("rotation writer = %#v, want %#v", rotationWriter, config)
	}
}

func TestOpenLogFileRotatesAtConfiguredSize(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "app.log")
	writer, err := openLogFile(logPath, logFileRotationConfig{
		maxSizeMB:  1,
		maxBackups: 3,
		maxAgeDays: 28,
		compress:   false,
	})
	if err != nil {
		t.Fatalf("openLogFile returned error: %v", err)
	}

	if _, err := io.WriteString(writer, strings.Repeat("x", 1024*1024)); err != nil {
		t.Fatalf("first log write returned error: %v", err)
	}
	if _, err := io.WriteString(writer, "y"); err != nil {
		t.Fatalf("rotation-triggering write returned error: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close returned error: %v", err)
	}

	rotated, err := filepath.Glob(filepath.Join(filepath.Dir(logPath), "app-*.log"))
	if err != nil {
		t.Fatalf("filepath.Glob returned error: %v", err)
	}
	if len(rotated) != 1 {
		t.Fatalf("rotated files = %v, want one file", rotated)
	}
}

func TestLogDiskThresholdsAndState(t *testing.T) {
	t.Setenv("LOG_DISK_WARNING_FREE_PERCENT", "30")
	t.Setenv("LOG_DISK_CRITICAL_FREE_PERCENT", "15")
	t.Setenv("LOG_DISK_MIN_FREE_GB", "7")

	thresholds, warnings := resolveLogDiskThresholdsFromEnv()
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}

	const gib = 1024 * 1024 * 1024
	tests := []struct {
		name  string
		usage logDiskUsage
		want  logDiskState
	}{
		{name: "healthy", usage: logDiskUsage{totalBytes: 100 * gib, freeBytes: 40 * gib}, want: logDiskStateHealthy},
		{name: "warning", usage: logDiskUsage{totalBytes: 100 * gib, freeBytes: 20 * gib}, want: logDiskStateWarning},
		{name: "critical percent", usage: logDiskUsage{totalBytes: 100 * gib, freeBytes: 10 * gib}, want: logDiskStateCritical},
		{name: "critical capacity", usage: logDiskUsage{totalBytes: 10 * gib, freeBytes: 6 * gib}, want: logDiskStateCritical},
		{name: "unknown", usage: logDiskUsage{}, want: logDiskStateUnknown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := evaluateLogDiskState(test.usage, thresholds); got != test.want {
				t.Fatalf("evaluateLogDiskState(%#v) = %q, want %q", test.usage, got, test.want)
			}
		})
	}
}

func TestLogDiskThresholdsRejectUnsafeValues(t *testing.T) {
	t.Setenv("LOG_DISK_WARNING_FREE_PERCENT", "10")
	t.Setenv("LOG_DISK_CRITICAL_FREE_PERCENT", "10")
	t.Setenv("LOG_DISK_MIN_FREE_GB", "-1")

	thresholds, warnings := resolveLogDiskThresholdsFromEnv()
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want invalid capacity and invalid ordering warnings", warnings)
	}
	if thresholds != (logDiskThresholds{
		warningFreePercent:  defaultLogDiskWarningFreePercent,
		criticalFreePercent: defaultLogDiskCriticalFreePercent,
		minFreeGB:           defaultLogDiskMinFreeGB,
	}) {
		t.Fatalf("thresholds = %#v, want defaults", thresholds)
	}
}

func TestGetFileLogRuntimeStatus(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(logPath, []byte("hello"), 0o600); err != nil {
		t.Fatalf("os.WriteFile returned error: %v", err)
	}
	t.Setenv("LOG_PATH", logPath)

	status, err := GetFileLogRuntimeStatus()
	if err != nil {
		t.Fatalf("GetFileLogRuntimeStatus returned error: %v", err)
	}
	if !status.Enabled || status.SizeBytes != 5 || status.DiskTotalBytes == 0 || status.DiskFreeBytes == 0 || status.DiskState == "" {
		t.Fatalf("unexpected file log status: %#v", status)
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
