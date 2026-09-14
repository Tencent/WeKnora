package sandbox

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/stretchr/testify/require"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestWorkbenchTracePayloadRedaction(t *testing.T) {
	// Init installs a process-wide manager. Run the real exporter in a child so
	// this regression test cannot change tracing for other sandbox tests.
	if os.Getenv("WORKBENCH_TRACE_TEST_CHILD") != "1" {
		binary, err := os.Executable()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestWorkbenchTracePayloadRedaction$", "-test.v")
		cmd.Env = []string{"WORKBENCH_TRACE_TEST_CHILD=1"}
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		t.Logf("%s", out)
		return
	}
	var mu sync.Mutex
	var batches []*collectortrace.ExportTraceServiceRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			unzip, err := gzip.NewReader(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer func() { _ = unzip.Close() }()
			reader = unzip
		}
		body, err := io.ReadAll(io.LimitReader(reader, 2<<20))
		var batch collectortrace.ExportTraceServiceRequest
		if err != nil || proto.Unmarshal(body, &batch) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		batches = append(batches, &batch)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	tracer, err := langfuse.Init(langfuse.Config{
		Enabled: true, Host: server.URL, PublicKey: "public-test-key", SecretKey: "public-test-secret",
		FlushAt: 1, FlushInterval: time.Hour, QueueSize: 256, RequestTimeout: 5 * time.Second, SampleRate: 1,
	})
	require.NoError(t, err)
	defer func() { _ = tracer.Shutdown(context.Background()) }()
	mgr, client, ctx := workbenchHarness(t, true)
	const secretPath = "synthetic-sensitive-file-name.txt"
	const secretBody = "synthetic-sensitive-file-body"
	content := bytes.Repeat([]byte(secretBody), workbenchDirectBytes/len(secretBody)+1)
	fail := false
	client.run = func(_ context.Context, _ RemoteSandboxHandle, remote RemoteExecRequest) (*RemoteExecResult, error) {
		if remote.Command == "ordinary-command-marker" {
			return nil, errors.New("ordinary-error-marker")
		}
		var req workbenchWireRequest
		require.NoError(t, json.Unmarshal([]byte(remote.Stdin), &req))
		if fail {
			return nil, fmt.Errorf("echoed request: args=%q stdin=%s stderr=%s", remote.Args, remote.Stdin, secretBody)
		}
		reply := workbenchWireReply{OK: true}
		switch req.Operation {
		case "_upload_begin":
			req.Upload.Device, req.Upload.Inode = 1, 99
			reply.Upload = req.Upload
		case "_upload_chunk", "_upload_abort":
		default:
			path := req.Path
			if req.Operation == "rename" {
				path = req.NewPath
			}
			reply.Result = &WorkbenchFileResult{Path: path, Entries: []WorkbenchFileEntry{}}
			if req.Operation == "read" {
				reply.Result.Content = []byte(secretBody)
			}
		}
		payload, err := json.Marshal(reply)
		require.NoError(t, err)
		return &RemoteExecResult{Stdout: string(payload), Stderr: secretBody}, nil
	}
	_, err = mgr.client.Exec(ctx, &fakeTokenlessHandle{id: "ordinary", provider: SandboxTypeDocker}, RemoteExecRequest{
		Command: "ordinary-command-marker", Shell: true,
	})
	require.EqualError(t, err, "ordinary-error-marker")
	ctx, rootTrace := tracer.StartTrace(ctx, langfuse.TraceOptions{Name: "workbench-redaction-test"})
	for _, req := range []WorkbenchFileRequest{
		{Operation: "write", Path: secretPath, Content: []byte(secretBody)},
		{Operation: "read", Path: secretPath},
		{Operation: "rename", Path: secretPath, NewPath: "other-" + secretPath},
		{Operation: "write", Path: secretPath, Content: content},
	} {
		_, err := mgr.WorkbenchFiles(ctx, "workbench-test", req)
		require.NoError(t, err)
	}
	fail = true
	_, operationErr := mgr.WorkbenchFiles(ctx, "workbench-test", WorkbenchFileRequest{
		Operation: "write", Path: secretPath, Content: []byte(secretBody),
	})
	require.ErrorIs(t, operationErr, ErrWorkbenchUnavailable)
	_, err = mgr.client.Exec(ctx, &fakeTokenlessHandle{id: "ordinary", provider: SandboxTypeDocker}, RemoteExecRequest{
		Command: "ordinary-command-marker", Shell: true,
	})
	require.EqualError(t, err, "ordinary-error-marker")
	rootTrace.Finish(nil, nil)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, tracer.Shutdown(shutdownCtx))
	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, batches, "the real OTLP exporter must emit spans")
	var spans []*tracepb.Span
	for _, batch := range batches {
		for _, resource := range batch.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				spans = append(spans, scope.Spans...)
			}
		}
	}
	workbenchSpans, ordinarySpans := 0, 0
	for _, span := range spans {
		encoded, err := json.Marshal(span)
		require.NoError(t, err)
		for _, secret := range []string{
			secretPath, secretBody,
			base64.StdEncoding.EncodeToString([]byte(secretBody)), base64.StdEncoding.EncodeToString(content),
		} {
			if bytes.Contains(encoded, []byte(secret)) {
				t.Errorf("exported span %q contains synthetic path/body marker", span.Name)
			}
		}
		for _, attr := range span.Attributes {
			if attr.Key == "langfuse.observation.output" && attr.Value.GetStringValue() != "null" {
				var output map[string]any
				require.NoError(t, json.Unmarshal([]byte(attr.Value.GetStringValue()), &output))
				if _, hasCounts := output["stdout_bytes"]; hasCounts {
					require.Len(t, output, 5)
					for _, key := range []string{"exit_code", "killed", "duration_ms", "stdout_bytes", "stderr_bytes"} {
						require.Contains(t, output, key)
					}
				}
			}
			if attr.Key != "langfuse.observation.input" || attr.Value.GetStringValue() == "null" {
				continue
			}
			var input map[string]any
			require.NoError(t, json.Unmarshal([]byte(attr.Value.GetStringValue()), &input))
			if input["command"] == "python3" {
				workbenchSpans++
				require.Equal(t, "python3", input["command"])
				require.Equal(t, false, input["shell"])
				require.Equal(t, "/", input["work_dir"])
				require.Equal(t, "root", input["user"])
				require.Contains(t, []any{float64(10000), float64(15000)}, input["timeout_ms"])
			}
			if input["command"] == "ordinary-command-marker" {
				ordinarySpans++
				require.Equal(t, "ordinary-error-marker", span.Status.Message)
			}
		}
	}
	for _, marker := range []string{secretPath, secretBody} {
		if strings.Contains(operationErr.Error(), marker) {
			t.Error("workbench caller error contains synthetic path/body marker")
		}
	}
	require.GreaterOrEqual(t, workbenchSpans, 8)
	require.Equal(t, 1, ordinarySpans)
	if !t.Failed() {
		t.Logf("captured %d helper exec spans: no raw or base64 path/body markers; ordinary tracing preserved",
			workbenchSpans)
	}
}
