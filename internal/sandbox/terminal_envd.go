package sandbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// The pinned go-e2b keeps envd credentials/domain private. Capture only the
// exact SDK create/connect response requested by this caller, and replay it
// unchanged to the SDK. No extra connect, credential cache, or SDK fork.
type terminalCredentialKey struct{}
type terminalCredentials struct {
	SandboxID   string `json:"sandboxID"`
	AccessToken string `json:"envdAccessToken"`
	Domain      string `json:"domain"`
}

type terminalCredentialTransport struct{ next http.RoundTripper }

func (t *terminalCredentialTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	credentials, ok := req.Context().Value(terminalCredentialKey{}).(*terminalCredentials)
	if !ok || isEnvdDataPlaneRequest(req) || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if len(raw) > 1024*1024 {
		return nil, errors.New("sandbox: oversized envd credential response")
	}
	_ = json.Unmarshal(raw, credentials)
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	return resp, nil
}

func (t *terminalCredentialTransport) CloseIdleConnections() {
	if closer, ok := t.next.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

type terminalEnvdClient struct {
	http         *http.Client
	base         string
	accessToken  string
	trafficToken string
}

func (c *E2BRemoteClient) OpenTerminal(ctx context.Context, handle RemoteSandboxHandle, req TerminalRequest) (Terminal, error) {
	h, ok := handle.(*e2bRemoteHandle)
	if !ok || h == nil || h.ID() == "" || h.terminal.SandboxID != h.ID() || c.terminalHTTP == nil {
		return nil, errors.New("sandbox: E2B terminal requires a lifecycle-issued handle")
	}
	domain := h.terminal.Domain
	if domain == "" {
		domain = c.terminalDomain
	}
	if domain == "" {
		domain = "e2b.app"
	}
	wire := &terminalEnvdClient{http: c.terminalHTTP, base: fmt.Sprintf("https://49983-%s.%s", h.ID(), domain),
		accessToken: h.terminal.AccessToken, trafficToken: h.TrafficAccessToken()}
	return startCommandTerminal(ctx, req, c, handle, wire.open)
}

func (c *CubeRemoteClient) OpenTerminal(ctx context.Context, handle RemoteSandboxHandle, req TerminalRequest) (Terminal, error) {
	h, ok := handle.(*cubeRemoteHandle)
	if !ok || h == nil || h.sb == nil || h.ID() == "" || c.terminalHTTP == nil {
		return nil, errors.New("sandbox: Cube terminal requires a lifecycle-issued handle")
	}
	wire := &terminalEnvdClient{http: c.terminalHTTP, base: "https://" + h.sb.GetHost(CubeEnvdPort),
		accessToken: h.sb.EnvdAccessToken, trafficToken: h.TrafficAccessToken()}
	return startCommandTerminal(ctx, req, c, handle, wire.open)
}

// Wire shapes follow the pinned SDK's proto/envd/process/process.proto.
// Connect streaming JSON uses a flags byte, big-endian length and JSON body;
// unary methods use plain JSON. Protobuf bytes fields are base64 JSON strings.
type terminalEnvdSize struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}
type terminalEnvdPTY struct {
	Size terminalEnvdSize `json:"size"`
}
type terminalEnvdSelector struct {
	Tag string `json:"tag"`
}
type terminalEnvdStart struct {
	Process struct {
		Cmd  string            `json:"cmd"`
		Args []string          `json:"args"`
		Envs map[string]string `json:"envs"`
		Cwd  string            `json:"cwd"`
	} `json:"process"`
	PTY   terminalEnvdPTY `json:"pty"`
	Tag   string          `json:"tag"`
	Stdin bool            `json:"stdin"`
}
type terminalEnvdEvent struct {
	Start *struct {
		PID uint32 `json:"pid"`
	} `json:"start"`
	Data *struct {
		PTY    []byte `json:"pty"`
		Stdout []byte `json:"stdout"`
		Stderr []byte `json:"stderr"`
	} `json:"data"`
	End *struct {
		ExitCode      int    `json:"exitCode"`
		ExitCodeSnake *int   `json:"exit_code"`
		Exited        bool   `json:"exited"`
		Error         string `json:"error"`
	} `json:"end"`
}

func (c *terminalEnvdClient) request(ctx context.Context, method string, payload any, stream bool) (*http.Response, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	contentType := "application/json"
	if stream {
		frame := make([]byte, 5+len(raw))
		binary.BigEndian.PutUint32(frame[1:5], uint32(len(raw)))
		copy(frame[5:], raw)
		raw = frame
		contentType = "application/connect+json"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/process.Process/"+method, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Connect-Accept-Encoding", "identity")
	req.Header.Set("Authorization", basicAuthorizationFor(DefaultSandboxExecUser))
	if c.accessToken != "" {
		req.Header.Set("X-Access-Token", c.accessToken)
	}
	if c.trafficToken != "" {
		req.Header.Set(InboundTokenHeader, c.trafficToken)
	}
	// Do not follow a redirect carrying envd credentials to a different host.
	httpClient := *c.http
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("sandbox: envd %s returned HTTP %d", method, resp.StatusCode)
	}
	return resp, nil
}

func (c *terminalEnvdClient) unary(ctx context.Context, method string, payload any) error {
	resp, err := c.request(ctx, method, payload, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024+1))
	if err != nil {
		return err
	}
	if len(raw) > 64*1024 {
		return errors.New("sandbox: oversized envd response")
	}
	var result struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if result.Code != "" {
		return fmt.Errorf("sandbox: envd %s: %s", method, result.Code)
	}
	return nil
}

func readTerminalEnvdEvent(src io.Reader) (*terminalEnvdEvent, error) {
	var header [5]byte
	if _, err := io.ReadFull(src, header[:]); err != nil {
		return nil, err
	}
	if header[0] != 0 && header[0] != 2 {
		return nil, errors.New("sandbox: unsupported envd frame flags")
	}
	size := binary.BigEndian.Uint32(header[1:])
	if size > 1024*1024 {
		return nil, errors.New("sandbox: envd frame exceeds 1 MiB")
	}
	raw := make([]byte, size)
	if _, err := io.ReadFull(src, raw); err != nil {
		return nil, err
	}
	if header[0] == 2 {
		var trailer struct {
			Error *struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &trailer); err != nil {
			return nil, err
		}
		if trailer.Error != nil {
			return nil, fmt.Errorf("sandbox: envd stream: %s", trailer.Error.Code)
		}
		return nil, io.EOF
	}
	var message struct {
		Event terminalEnvdEvent `json:"event"`
	}
	if err := json.Unmarshal(raw, &message); err != nil {
		return nil, err
	}
	return &message.Event, nil
}

func (c *terminalEnvdClient) open(ctx context.Context, argv []string, token string, req TerminalRequest) (*terminalProcess, error) {
	var payload terminalEnvdStart
	payload.Process.Cmd, payload.Process.Args = argv[0], argv[1:]
	payload.Process.Envs = map[string]string{"TERM": "xterm-256color", "LANG": "C.UTF-8"}
	payload.Process.Cwd = SessionWorkspaceRoot
	payload.PTY.Size = terminalEnvdSize{Cols: req.Cols, Rows: req.Rows}
	payload.Tag, payload.Stdin = token, true
	resp, err := c.request(ctx, "Start", payload, true)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/connect+json") {
		_ = resp.Body.Close()
		return nil, errors.New("sandbox: envd returned a non-JSON process stream")
	}
	for {
		event, err := readTerminalEnvdEvent(resp.Body)
		if err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("sandbox: envd start: %w", err)
		}
		if event.Start != nil && event.Start.PID != 0 {
			break
		}
		if event.End != nil || event.Data != nil {
			_ = resp.Body.Close()
			return nil, errors.New("sandbox: envd omitted process start")
		}
	}
	selector := terminalEnvdSelector{Tag: token}
	writes := make(chan struct{}, 1)
	return &terminalProcess{
		close: func() { _ = resp.Body.Close() },
		read: func(dst io.Writer) (int, error) {
			for {
				event, err := readTerminalEnvdEvent(resp.Body)
				if err != nil {
					return -1, fmt.Errorf("sandbox: envd stream ended before exit: %w", err)
				}
				if event.Data != nil {
					for _, data := range [][]byte{event.Data.PTY, event.Data.Stdout, event.Data.Stderr} {
						if _, err := dst.Write(data); err != nil {
							return -1, err
						}
					}
				}
				if event.End != nil {
					end := event.End
					if end.ExitCodeSnake != nil {
						end.ExitCode = *end.ExitCodeSnake
					}
					// envd includes os/exec's "exit status N" as error for normal
					// nonzero exits. Only a missing exit status is a wire failure.
					if end.Error != "" && end.ExitCode == 0 && !end.Exited {
						return -1, errors.New("sandbox: envd process reported an execution error")
					}
					return end.ExitCode, nil
				}
			}
		},
		input: func(ctx context.Context, p []byte) error {
			select {
			case writes <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
			defer func() { <-writes }()
			return c.unary(ctx, "SendInput", struct {
				Process terminalEnvdSelector `json:"process"`
				Input   struct {
					PTY []byte `json:"pty"`
				} `json:"input"`
			}{Process: selector, Input: struct {
				PTY []byte `json:"pty"`
			}{PTY: p}})
		},
		resize: func(ctx context.Context, cols, rows uint16) error {
			return c.unary(ctx, "Update", struct {
				Process terminalEnvdSelector `json:"process"`
				PTY     terminalEnvdPTY      `json:"pty"`
			}{Process: selector, PTY: terminalEnvdPTY{Size: terminalEnvdSize{Cols: cols, Rows: rows}}})
		},
		stop: func(ctx context.Context) error {
			return c.unary(ctx, "SendSignal", struct {
				Process terminalEnvdSelector `json:"process"`
				Signal  string               `json:"signal"`
			}{Process: selector, Signal: "SIGNAL_SIGTERM"})
		},
	}, nil
}

var _ remoteTerminalProvider = (*E2BRemoteClient)(nil)
var _ remoteTerminalProvider = (*CubeRemoteClient)(nil)
