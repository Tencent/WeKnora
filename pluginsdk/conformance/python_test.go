package conformance_test

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/conformance"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// The Python SDK speaks the same protocol: its test plugin must pass the
// suite in both modes, and a Go client must read its answers.

func python(t *testing.T) string {
	t.Helper()
	names := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		names = []string{"python", "python3"} // python3 may be the Store alias
	}
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("python3 is not installed")
	return ""
}

var fixture = filepath.Join("..", "python", "tests", "fixture.py")

func startPython(t *testing.T, env ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(python(t), fixture)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	cmd.Stderr = os.Stderr
	return cmd
}

func stopPython(t *testing.T, cmd *exec.Cmd) {
	t.Cleanup(func() {
		if runtime.GOOS == "windows" {
			_ = cmd.Process.Kill()
		} else {
			_ = cmd.Process.Signal(os.Interrupt)
		}
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
}

func report(t *testing.T, rep conformance.Report) {
	t.Helper()
	for _, r := range rep.Results {
		if !r.Passed {
			t.Errorf("%s: %s", r.Name, r.Detail)
		}
	}
	if len(rep.Results) < 13 {
		t.Fatalf("only %d checks ran", len(rep.Results))
	}
}

func TestPythonPluginConformsInHostMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Python SDK serves host mode on unix sockets")
	}
	dir, err := os.MkdirTemp("", "wkpy")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "p.sock")
	cmd := startPython(t, pluginapi.EnvSocket+"="+sock, pluginapi.EnvToken+"=tok")
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopPython(t, cmd)
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("no handshake: %v", err)
	}
	hs, ok, err := pluginapi.ParseHandshake(line)
	if !ok || err != nil || hs.Address != sock {
		t.Fatalf("handshake %q: %v", line, err)
	}
	c := client.ForHandshake(hs, client.Bearer("tok"))
	report(t, conformance.Run(context.Background(), conformance.Target{
		Client: c, Unauthenticated: client.ForHandshake(hs, nil),
		Raw: func(ctx context.Context, path string, body []byte) (*http.Response, error) {
			tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			}}
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://plugin"+path, bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer tok")
			return (&http.Client{Transport: tr}).Do(req)
		},
	}))

	// A Go client reads the Python stream and errors like a Go plugin's.
	var items int
	cursor, err := c.Stream(context.Background(), pluginapi.ConnectorFetchPath("notes"), pluginapi.Envelope{},
		pluginapi.FetchInput{Mode: pluginapi.FetchFull}, func(ev pluginapi.Event) error {
			if ev.Type == pluginapi.EventItem {
				items++
			}
			return nil
		})
	if err != nil || items != 3 || string(cursor) != `{"state":{"after":3}}` {
		t.Fatalf("fetch = %d items, cursor %s, %v", items, cursor, err)
	}
	var out pluginapi.ParseOutput
	err = c.Call(context.Background(), pluginapi.ParsePath("upper"), pluginapi.Envelope{},
		pluginapi.ParseInput{FileType: "txt", Content: []byte("hi")}, &out)
	if err != nil || len(out.Images) != 1 || string(out.Images[0].Data) != "\x89PNG" {
		t.Fatalf("parse = %+v, %v", out, err)
	}
	err = c.Call(context.Background(), pluginapi.SearchPath("echo"), pluginapi.Envelope{},
		pluginapi.SearchInput{Query: "down"}, nil)
	if pe, ok := pluginapi.AsError(err); !ok || pe.Code != pluginapi.CodeUnavailable || !pe.Retryable {
		t.Fatalf("search error = %v", err)
	}
}

func TestPythonPluginConformsInRemoteMode(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	cmd := startPython(t, pluginapi.EnvAddr+"="+addr, pluginapi.EnvSecret+"=s3cret")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopPython(t, cmd)
	base := "http://" + addr
	c := client.New(base, nil, client.Signed("s3cret"))
	deadline := time.Now().Add(30 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := c.Health(ctx)
		cancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the Python plugin did not come up: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	report(t, conformance.Run(context.Background(), conformance.Target{
		Client: c, Unauthenticated: client.New(base, nil, nil),
		Raw: func(ctx context.Context, path string, body []byte) (*http.Response, error) {
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
			_ = client.Signed("s3cret").Apply(req, body)
			return http.DefaultClient.Do(req)
		},
	}))
}
