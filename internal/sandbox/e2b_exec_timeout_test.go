package sandbox

import (
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type e2bTimeoutTestTransport struct{ target *url.URL }

func (t e2bTimeoutTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.URL.Scheme, r.URL.Host = t.target.Scheme, t.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

// Exercise the real SDK's command stream with a scaled-down HTTP budget.
// The successful command must outlive that budget, while execution deadlines
// and parent cancellation must still interrupt the same stream.
func TestE2BExecTimeoutBudgets(t *testing.T) {
	for _, mode := range []string{"long_command", "execution_deadline", "parent_cancel"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/sandboxes" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_, _ = io.WriteString(w, `{"sandboxID":"test","envdAccessToken":"token"}`)
					return
				}
				if r.URL.Path != "/process.Process/Start" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/connect+proto")
				writeFrame := func(payload []byte) {
					header := make([]byte, 5)
					binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
					_, _ = w.Write(append(header, payload...))
					w.(http.Flusher).Flush()
				}
				// StartResponse{event:{start:{pid:1}}}. The SDK's protobuf
				// types are internal, so these small fixtures use wire bytes.
				writeFrame([]byte{0x0a, 4, 0x0a, 2, 0x08, 1})
				close(started)
				select {
				case <-time.After(300 * time.Millisecond):
					// StartResponse{event:{end:{exited:true, exit_code:0}}}.
					writeFrame([]byte{0x0a, 4, 0x1a, 2, 0x10, 1})
					// Connect end-of-stream envelope with empty metadata.
					_, _ = w.Write([]byte{2, 0, 0, 0, 2, '{', '}'})
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			target, err := url.Parse(server.URL)
			require.NoError(t, err)
			client, err := newE2BRemoteClient(&Config{
				E2BAPIKey: "test", E2BAPIURL: server.URL,
				E2BHTTPTimeout: 100 * time.Millisecond,
			}, e2bTimeoutTestTransport{target: target}, NewInboundTokenRegistry())
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			handle, err := client.Create(ctx, RemoteCreateRequest{TemplateID: "test"})
			require.NoError(t, err)
			timeout := 2 * time.Second
			if mode == "execution_deadline" {
				timeout = 150 * time.Millisecond
			}
			if mode == "parent_cancel" {
				go func() {
					select {
					case <-started:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			result, err := client.Exec(ctx, handle, RemoteExecRequest{Command: "test", Timeout: timeout})
			switch mode {
			case "long_command":
				require.NoError(t, err)
				require.False(t, result.Killed, "HTTP timeout must not truncate the command stream")
				require.Zero(t, result.ExitCode)
			case "execution_deadline":
				require.NoError(t, err)
				require.True(t, result.Killed)
			case "parent_cancel":
				require.Error(t, err)
			}
		})
	}
}

func TestE2BRPCTimeoutBoundsOrdinaryResponseBodies(t *testing.T) {
	for _, route := range []string{"/sandboxes", "/process.Process/List", "/filesystem.Filesystem/MakeDir"} {
		t.Run(route, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer server.Close()
			client := &http.Client{Transport: &e2bRPCTimeoutTransport{
				next: http.DefaultTransport, timeout: 50 * time.Millisecond,
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+route, nil)
			require.NoError(t, err)
			response, err := client.Do(req)
			require.NoError(t, err)
			defer response.Body.Close()
			_, err = io.ReadAll(response.Body)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.NoError(t, ctx.Err(), "ordinary RPC must expire before the parent budget")
		})
	}
}

func TestE2BFileTransferUsesCallerContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	client := &http.Client{Transport: &e2bRPCTimeoutTransport{
		next: http.DefaultTransport, timeout: 50 * time.Millisecond,
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/files", nil)
	require.NoError(t, err)
	started := time.Now()
	response, err := client.Do(req)
	require.NoError(t, err)
	defer response.Body.Close()
	_, err = io.ReadAll(response.Body)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Error(t, ctx.Err(), "file transfer must follow the caller deadline, not the RPC budget")
	require.Greater(t, time.Since(started), 100*time.Millisecond)
}
