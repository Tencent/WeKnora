package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

const maxSSEEventSize = 1024 * 1024

// Bound prefix copies and JSON rescans, including empty data lines. Providers
// exceeding this limit must emit less fragmented JSON events.
const maxSSEDataLines = 128

// SSEEvent 表示一个 Server-Sent Events 事件
type SSEEvent struct {
	Data []byte
	Done bool
}

// SSEReader reads SSE from JSON model providers. It also retains compatibility
// with providers that omit blank separators between complete JSON packets.
type SSEReader struct {
	scanner        *bufio.Scanner
	firstLine      bool
	pendingData    string
	hasPendingData bool
}

// NewSSEReader 创建 SSE 读取器
func NewSSEReader(reader io.Reader) *SSEReader {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, maxSSEEventSize), maxSSEEventSize)
	scanner.Split(newSSELineSplitter())
	return &SSEReader{scanner: scanner, firstLine: true}
}

// ReadEvent 读取下一个 SSE 事件
func (r *SSEReader) ReadEvent() (*SSEEvent, error) {
	var data []string
	if r.hasPendingData {
		payload := r.pendingData
		r.pendingData = ""
		r.hasPendingData = false
		return newSSEEvent(payload), nil
	}
	for r.scanner.Scan() {
		line := r.scanner.Text()
		if r.firstLine {
			line = strings.TrimPrefix(line, "\ufeff")
			r.firstLine = false
		}
		if line == "" {
			if len(data) > 0 {
				return newSSEEvent(strings.Join(data, "\n")), nil
			}
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		if len(data) >= maxSSEDataLines {
			return nil, errors.New("SSE event exceeds 128 data lines")
		}
		payload := strings.TrimPrefix(line[len("data:"):], " ")
		if len(data) > 0 {
			previous := strings.Join(data, "\n")
			if len(previous)+1+len(payload) > maxSSEEventSize {
				return nil, errors.New("SSE event exceeds 1 MiB")
			}
			joined := previous + "\n" + payload
			// Preserve malformed-line separation when the following line is a
			// healthy complete packet, but allow a valid JSON value inside a still
			// incomplete array/object to remain part of that multiline packet.
			if isCompleteProviderPacket(payload) && !json.Valid([]byte(joined)) && !isIncompleteJSON(joined) {
				r.pendingData = payload
				r.hasPendingData = true
				return newSSEEvent(previous), nil
			}
		}
		data = append(data, payload)
		combined := strings.Join(data, "\n")
		// Model providers may keep the connection open without a blank separator.
		// A complete packet must be available immediately, without waiting for the
		// next network read. Only incomplete JSON prefixes need additional lines.
		if isCompleteProviderPacket(combined) || !isIncompleteJSON(combined) {
			return newSSEEvent(combined), nil
		}

	}
	if err := r.scanner.Err(); err != nil {
		return nil, err
	}
	// Preserve the existing model-provider contract for a final packet at EOF.
	if len(data) > 0 {
		return newSSEEvent(strings.Join(data, "\n")), nil
	}
	return nil, io.EOF
}

func newSSEEvent(payload string) *SSEEvent {
	if strings.TrimSpace(payload) == "[DONE]" {
		return &SSEEvent{Done: true}
	}
	return &SSEEvent{Data: []byte(payload)}
}

func isCompleteProviderPacket(payload string) bool {
	return strings.TrimSpace(payload) == "[DONE]" || json.Valid([]byte(payload))
}

func isIncompleteJSON(payload string) bool {
	var value json.RawMessage
	err := json.NewDecoder(strings.NewReader(payload)).Decode(&value)
	return err == io.ErrUnexpectedEOF || err == io.EOF
}

// newSSELineSplitter accepts LF, CRLF, and CR line endings. A CR completes
// its line immediately, even when the connection pauses at that byte. The
// optional LF is consumed on the next scan without introducing an empty line.
func newSSELineSplitter() bufio.SplitFunc {
	skipLF := false
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		skipped := 0
		if skipLF {
			if len(data) == 0 && !atEOF {
				return 0, nil, nil
			}
			skipLF = false
			if len(data) > 0 && data[0] == '\n' {
				data = data[1:]
				skipped = 1
			}
		}
		if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
			if data[i] == '\r' {
				skipLF = true
			}
			return skipped + i + 1, data[:i], nil
		}
		if atEOF && len(data) > 0 {
			return skipped + len(data), data, nil
		}
		return skipped, nil, nil
	}
}
