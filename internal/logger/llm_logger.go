package logger

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var llmDebug struct {
	mu      sync.Mutex
	enabled bool
	dir     string
}

func init() {
	configureLLMDebugLog()
}

func configureLLMDebugLog() {
	val := strings.TrimSpace(os.Getenv("LLM_DEBUG_LOG"))
	if val == "" || val == "false" || val == "0" {
		return
	}

	var dir string
	if val == "true" || val == "1" {
		dir = resolveLLMDebugDir()
	} else {
		dir = val
	}
	if dir == "" {
		dir = "llm_debug"
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "llm_debug: failed to create dir %s: %v\n", dir, err)
		return
	}

	llmDebug.dir = dir
	llmDebug.enabled = true

	go cleanupOldDebugFiles(dir, 7*24*time.Hour)

	fmt.Fprintf(os.Stderr, "llm_debug: LLM debug log enabled → %s/\n", dir)
}

func resolveLLMDebugDir() string {
	if logPath := strings.TrimSpace(os.Getenv("LOG_PATH")); logPath != "" {
		return filepath.Join(filepath.Dir(logPath), "llm_debug")
	}
	if macPath := defaultMacAppLogPath(); macPath != "" {
		return filepath.Join(filepath.Dir(macPath), "llm_debug")
	}
	return "llm_debug"
}

// cleanupOldDebugFiles removes files older than maxAge from the debug directory.
func cleanupOldDebugFiles(dir string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// LLMDebugEnabled returns true when the dedicated LLM debug log is active.
func LLMDebugEnabled() bool {
	return llmDebug.enabled
}

// LLMDebugLog writes a complete model call record to a per-request log file.
// All calls sharing the same request_id are appended to the same file.
func LLMDebugLog(ctx context.Context, record *LLMCallRecord) {
	if !llmDebug.enabled || record == nil {
		return
	}

	text := formatRecord(record)

	reqID := extractRequestID(ctx)
	filename := buildFilename(reqID)

	llmDebug.mu.Lock()
	defer llmDebug.mu.Unlock()

	path := filepath.Join(llmDebug.dir, filename)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "llm_debug: open %s: %v\n", path, err)
		return
	}
	defer f.Close()
	_, _ = f.WriteString(text)
}

func buildFilename(reqID string) string {
	if reqID != "" {
		return reqID + ".log"
	}
	return time.Now().Format("20060102_150405.000") + ".log"
}

func formatRecord(r *LLMCallRecord) string {
	var b strings.Builder
	b.Grow(4096)

	separator := fmt.Sprintf("================ %s ================", r.CallType)
	b.WriteString("\n")
	b.WriteString(separator)
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("Time:     %s\n", time.Now().Format("2006-01-02 15:04:05.000")))
	b.WriteString(fmt.Sprintf("Model:    %s\n", r.Model))
	if r.Duration > 0 {
		b.WriteString(fmt.Sprintf("Duration: %s\n", r.Duration.Round(time.Millisecond)))
	}

	for _, s := range r.Sections {
		b.WriteString(fmt.Sprintf("\n---------- %s ----------\n", s.Title))
		b.WriteString(s.Content)
		if !strings.HasSuffix(s.Content, "\n") {
			b.WriteString("\n")
		}
	}

	if r.Error != "" {
		b.WriteString("\n---------- Error ----------\n")
		b.WriteString(r.Error)
		b.WriteString("\n")
	}

	b.WriteString(strings.Repeat("=", len(separator)))
	b.WriteString("\n")

	return b.String()
}

func extractRequestID(ctx context.Context) string {
	entry := GetLogger(ctx)
	if v, ok := entry.Data["request_id"]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// ---------- Record types ----------

// LLMCallRecord holds all information for one model API call.
type LLMCallRecord struct {
	CallType string        // "Chat", "Chat Stream", "Embedding", "Rerank", "VLM"
	Model    string
	Duration time.Duration
	Sections []RecordSection
	Error    string
}

// RecordSection is a titled block of text within a call record.
type RecordSection struct {
	Title   string
	Content string
}

// ---------- Shared helpers for building sections ----------

// LLMMessage is a simplified chat message for logging.
type LLMMessage struct {
	Role       string
	Content    string
	Name       string
	ToolCallID string
	Images     []string
	ToolCalls  []LLMToolCallInfo
}

// LLMToolCallInfo holds tool call info for logging.
type LLMToolCallInfo struct {
	ID        string
	FuncName  string
	Arguments string
}

// FormatMessages formats chat messages into a readable block.
func FormatMessages(messages []LLMMessage) string {
	var b strings.Builder
	for _, m := range messages {
		b.WriteString(fmt.Sprintf("[%s]", m.Role))
		if m.Name != "" {
			b.WriteString(fmt.Sprintf(" name=%s", m.Name))
		}
		if m.ToolCallID != "" {
			b.WriteString(fmt.Sprintf(" tool_call_id=%s", m.ToolCallID))
		}
		b.WriteString("\n")

		if m.Content != "" {
			b.WriteString(m.Content)
			b.WriteString("\n")
		}
		for _, img := range m.Images {
			if len([]rune(img)) > 80 {
				b.WriteString(fmt.Sprintf("[image: %s (%d bytes)]\n", TruncateRunes(img, 80), len(img)))
			} else {
				b.WriteString(fmt.Sprintf("[image: %s]\n", img))
			}
		}
		for _, tc := range m.ToolCalls {
			b.WriteString(fmt.Sprintf("  -> tool_call: id=%s, func=%s, args=%s\n", tc.ID, tc.FuncName, tc.Arguments))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// FormatToolCalls formats response tool calls into a readable block.
func FormatToolCalls(tcs []LLMToolCallInfo) string {
	var b strings.Builder
	for _, tc := range tcs {
		b.WriteString(fmt.Sprintf("[tool_call] id=%s, func=%s\n%s\n\n", tc.ID, tc.FuncName, tc.Arguments))
	}
	return b.String()
}

// TruncateRunes truncates a string to maxRunes runes, appending "..." if truncated.
// This is safe for multi-byte UTF-8 characters (e.g. Chinese).
func TruncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
