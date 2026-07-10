// Package sandbox: envd Connect-RPC wire helpers.
//
// envd (the in-sandbox daemon) exposes process.Process/Start as a Connect
// server-streaming RPC. Both the request body and the response stream are
// framed as Connect envelopes:
//
//	[1 flag byte][4 big-endian length bytes][payload]
//
// The upstream Go SDK (v0.0.0-20260709) frames the *response* correctly but
// sends the *request* as bare JSON, which envd rejects. This file provides
// the minimal framing/decoding logic WeKnora needs to send that one call
// itself (see cubeClient.startProcess). It intentionally mirrors the SDK's
// own internal helpers so behaviour stays identical once the SDK is fixed and
// this shim can be removed.
package sandbox

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	connectProtocolVersion = "1"
	connectContentType     = "application/connect+json"
	connectEndStreamFlag   = byte(0x02)
	connectCompressedFlag  = byte(0x01)
	// maxConnectEnvelopeSize bounds a single stream frame to guard against a
	// malformed length header allocating an enormous buffer.
	maxConnectEnvelopeSize = 64 * 1024 * 1024
	// defaultEnvdUser matches the SDK/Python default; envd rejects calls with
	// no user ("no user specified").
	defaultEnvdUser = "root"
)

// envdProcessStartRequest is the JSON body of a process.Process/Start call.
// Field shapes mirror the SDK's private processStartRequest so the wire
// contract is identical.
type envdProcessStartRequest struct {
	Process envdProcessConfig `json:"process"`
	Stdin   *bool             `json:"stdin,omitempty"`
}

type envdProcessConfig struct {
	Cmd  string            `json:"cmd"`
	Args []string          `json:"args"`
	Envs map[string]string `json:"envs"`
	Cwd  string            `json:"cwd,omitempty"`
}

// envd stream event shapes (subset WeKnora consumes).
type envdProcessResponse struct {
	Event *envdProcessEvent `json:"event"`
}

type envdProcessEvent struct {
	Start *envdStartEvent `json:"start,omitempty"`
	Data  *envdDataEvent  `json:"data,omitempty"`
	End   *envdEndEvent   `json:"end,omitempty"`
}

type envdStartEvent struct {
	PID int `json:"pid"`
}

type envdDataEvent struct {
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	PTY    string `json:"pty,omitempty"`
}

type envdEndEvent struct {
	ExitCode      *int   `json:"exitCode,omitempty"`
	ExitCodeSnake *int   `json:"exit_code,omitempty"`
	Exited        bool   `json:"exited,omitempty"`
	Status        string `json:"status,omitempty"`
	Error         string `json:"error,omitempty"`
}

func (e *envdEndEvent) exit() (int, bool) {
	if e == nil {
		return 0, false
	}
	if e.ExitCode != nil {
		return *e.ExitCode, true
	}
	if e.ExitCodeSnake != nil {
		return *e.ExitCodeSnake, true
	}
	// Some envd builds omit the numeric exitCode entirely and only report a
	// textual status like "exit status 0" / "exit status 137". Parse the code
	// out of that so a successful run isn't mistaken for a protocol error.
	if code, ok := exitCodeFromStatus(e.Status); ok {
		return code, true
	}
	// A clean exit with a blank status still means the process finished; treat
	// a bare "exited: true" as exit 0 rather than a stream error.
	if e.Exited {
		return 0, true
	}
	return 0, false
}

// exitCodeFromStatus extracts N from envd status strings of the form
// "exit status N". Returns ok=false for statuses it doesn't recognise (e.g.
// "signal: killed"), letting the caller fall back to other signals.
func exitCodeFromStatus(status string) (int, bool) {
	const prefix = "exit status "
	s := strings.TrimSpace(status)
	if !strings.HasPrefix(s, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(s, prefix)))
	if err != nil {
		return 0, false
	}
	return n, true
}

// connectEndStream is the trailing frame (flag 0x02) of a Connect stream; a
// non-nil Error means the RPC failed after the headers were sent.
type connectEndStream struct {
	Error *connectStreamError `json:"error,omitempty"`
}

type connectStreamError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// encodeConnectEnvelope wraps payload in a single Connect frame:
// [flags][uint32 big-endian length][payload].
func encodeConnectEnvelope(flags byte, payload []byte) []byte {
	out := make([]byte, 0, 5+len(payload))
	out = append(out, flags)
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(payload)))
	out = append(out, length[:]...)
	out = append(out, payload...)
	return out
}

// envdBasicAuth builds the "Basic <user>:" header envd expects.
func envdBasicAuth(user string) string {
	if user == "" {
		user = defaultEnvdUser
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"))
}

// parseProcessStartStream drains a Connect server-streaming response and
// aggregates stdout/stderr/exit code into a CommandResult. It mirrors the
// SDK's own stream parser so decoding stays byte-for-byte compatible.
func parseProcessStartStream(r io.Reader) (*CommandResult, error) {
	var (
		stdout strings.Builder
		stderr strings.Builder
		exit   int
		sawEnd bool
	)

	for {
		flags, payload, err := readConnectEnvelope(r)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		if flags&connectCompressedFlag != 0 {
			return nil, fmt.Errorf("unsupported compressed Connect stream message")
		}
		if flags&connectEndStreamFlag != 0 {
			if err := parseConnectEndStream(payload); err != nil {
				return nil, err
			}
			continue
		}

		var resp envdProcessResponse
		if err := json.Unmarshal(payload, &resp); err != nil {
			return nil, fmt.Errorf("decode process event: %w", err)
		}
		if resp.Event == nil {
			continue
		}
		if resp.Event.Data != nil {
			if resp.Event.Data.Stdout != "" {
				text, err := decodeProcessBytes(resp.Event.Data.Stdout)
				if err != nil {
					return nil, fmt.Errorf("decode stdout: %w", err)
				}
				stdout.WriteString(text)
			}
			if resp.Event.Data.Stderr != "" {
				text, err := decodeProcessBytes(resp.Event.Data.Stderr)
				if err != nil {
					return nil, fmt.Errorf("decode stderr: %w", err)
				}
				stderr.WriteString(text)
			}
		}
		if resp.Event.End != nil {
			code, ok := resp.Event.End.exit()
			if !ok {
				if resp.Event.End.Error != "" {
					return nil, fmt.Errorf("process failed: %s", resp.Event.End.Error)
				}
				return nil, fmt.Errorf("process EndEvent missing exit code")
			}
			exit = code
			sawEnd = true
		}
	}

	if !sawEnd {
		return nil, fmt.Errorf("process stream ended without EndEvent")
	}
	return &CommandResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exit,
	}, nil
}

// readConnectEnvelope reads one framed message from r, returning its flag byte
// and payload. io.EOF at a frame boundary signals a clean end of stream.
func readConnectEnvelope(r io.Reader) (byte, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}
	size := binary.BigEndian.Uint32(header[1:])
	if size > maxConnectEnvelopeSize {
		return 0, nil, fmt.Errorf("Connect stream message too large: %d bytes", size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return header[0], payload, nil
}

// parseConnectEndStream inspects the trailing frame; a populated Error field
// turns into a Go error so callers see stream-level failures.
func parseConnectEndStream(raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var end connectEndStream
	if err := json.Unmarshal(raw, &end); err != nil {
		return fmt.Errorf("decode Connect end stream: %w", err)
	}
	if end.Error == nil {
		return nil
	}
	msg := strings.TrimSpace(end.Error.Message)
	if msg == "" {
		msg = "Connect stream error"
	}
	if end.Error.Code != "" {
		return fmt.Errorf("%s: %s", end.Error.Code, msg)
	}
	return fmt.Errorf("%s", msg)
}

// decodeProcessBytes base64-decodes the stdout/stderr chunks envd emits.
func decodeProcessBytes(value string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
