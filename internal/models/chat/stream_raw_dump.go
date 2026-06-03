package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// streamRawDumpDir returns the directory for per-stream raw packet dumps.
// Enabled when WEKNORA_LLM_STREAM_RAW_DUMP_DIR is set, or when
// WEKNORA_LLM_STREAM_RAW_DUMP=1 (defaults to ~/.weknora/investigate/llm-stream).
func streamRawDumpDir() string {
	if dir := strings.TrimSpace(os.Getenv("WEKNORA_LLM_STREAM_RAW_DUMP_DIR")); dir != "" {
		return dir
	}
	v := strings.TrimSpace(os.Getenv("WEKNORA_LLM_STREAM_RAW_DUMP"))
	if v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes") {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, ".weknora", "investigate", "llm-stream")
	}
	return ""
}

// streamPacketDumper writes one stream session to a dedicated JSONL file:
// line 1 = request wrapper; following lines = raw provider chunk JSON.
type streamPacketDumper struct {
	mu    sync.Mutex
	file  *os.File
	path  string
	model string
	seq   int
}

func newStreamPacketDumper(modelName string, request any) *streamPacketDumper {
	dir := streamRawDumpDir()
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}

	safeModel := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, modelName)
	if safeModel == "" {
		safeModel = "model"
	}

	name := fmt.Sprintf("llm_stream_%s_%s.jsonl", safeModel, time.Now().Format("20060102T150405.000000000"))
	path := filepath.Join(dir, name)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}

	d := &streamPacketDumper{file: f, path: path, model: modelName}
	_ = d.writeRequest(request)
	return d
}

func (d *streamPacketDumper) writeRequest(request any) error {
	line, err := json.Marshal(map[string]any{
		"type":      "request",
		"model":     d.model,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"data":      request,
	})
	if err != nil {
		return err
	}
	return d.writeLine(line)
}

func (d *streamPacketDumper) writeLine(line []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, err := d.file.Write(line); err != nil {
		return err
	}
	_, err := d.file.Write([]byte{'\n'})
	return err
}

// WritePacketRaw appends one provider chunk as a single JSONL line (valid JSON written as-is).
func (d *streamPacketDumper) WritePacketRaw(raw []byte) {
	if d == nil || d.file == nil || len(raw) == 0 {
		return
	}
	raw = bytesTrimSpace(raw)
	if len(raw) == 0 {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.seq++

	if json.Valid(raw) {
		_, _ = d.file.Write(raw)
		_, _ = d.file.Write([]byte{'\n'})
		return
	}

	line, _ := json.Marshal(map[string]any{
		"type":      "packet",
		"seq":       d.seq,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"data_raw":  string(raw),
	})
	_, _ = d.file.Write(line)
	_, _ = d.file.Write([]byte{'\n'})
}

// WritePacket marshals v as one JSON object per line (SDK stream Recv path).
func (d *streamPacketDumper) WritePacket(v any) {
	if d == nil || v == nil {
		return
	}
	line, err := json.Marshal(v)
	if err != nil {
		return
	}
	d.WritePacketRaw(line)
}

func (d *streamPacketDumper) Path() string {
	if d == nil {
		return ""
	}
	return d.path
}

func (d *streamPacketDumper) Close() {
	if d == nil || d.file == nil {
		return
	}
	_ = d.file.Close()
	d.file = nil
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
