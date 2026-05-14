//go:build acceptance_e2e

// Package e2e_test drives the WeKnora CLI binary against a real running
// server to validate the RAG closing loop end-to-end.
//
// Build tag isolation: //go:build acceptance_e2e excludes this file from
// the default `go test ./...` (mirrors gh's acceptance/ build tag pattern;
// see https://github.com/cli/cli/tree/trunk/acceptance). To run:
//
//   cd cli
//   WEKNORA_E2E_HOST=https://kb.example.com \
//   WEKNORA_E2E_TOKEN=eyJhbGc... \
//   go test -tags=acceptance_e2e -v ./acceptance/e2e/...
//
// Optional WEKNORA_E2E_KB_NAME_PREFIX customizes the throwaway KB name (default
// "cli-e2e-"). Cleanup runs even on test failure via t.Cleanup so the server
// doesn't accumulate test debris.
package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRAGFullLoop walks the demo MVP path: link a context, create a KB,
// upload a doc, wait for indexing, search it, then chat against it. Each
// step parses the CLI's bare JSON to extract IDs for the next step -
// validating both functional behavior and wire-contract stability.
func TestRAGFullLoop(t *testing.T) {
	host := mustEnv(t, "WEKNORA_E2E_HOST")
	token := mustEnv(t, "WEKNORA_E2E_TOKEN")
	prefix := envOr("WEKNORA_E2E_KB_NAME_PREFIX", "cli-e2e-")

	bin := buildBinary(t)
	xdg := t.TempDir()
	writeContextYAML(t, xdg, host, token)

	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+xdg,
		"XDG_CACHE_HOME="+filepath.Join(xdg, "cache"),
		// SDK debug off - explicit so the CI run isn't noisy.
		"WEKNORA_SDK_DEBUG=",
	)

	// 1. kb create → bare KnowledgeBase object
	kbName := prefix + fmt.Sprintf("%d", time.Now().UnixNano())
	var created struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	runJSONInto(t, bin, env, &created, "kb", "create", "--name", kbName, "--json")
	if created.ID == "" {
		t.Fatalf("kb create returned no id")
	}
	t.Logf("created KB: %s (%s)", created.ID, kbName)

	t.Cleanup(func() {
		// Best-effort cleanup; a 404 means the KB was already gone.
		out, err := run(bin, env, "kb", "delete", created.ID, "-y", "--json")
		if err != nil {
			t.Logf("cleanup kb delete: %v\n%s", err, out)
		}
	})

	// 2. doc upload → bare Knowledge object
	docPath := writeSampleDoc(t)
	var uploaded struct {
		ID string `json:"id"`
	}
	runJSONInto(t, bin, env, &uploaded, "doc", "upload", docPath, "--kb", created.ID, "--json")
	if uploaded.ID == "" {
		t.Fatalf("doc upload returned no id")
	}
	t.Logf("uploaded doc: %s", uploaded.ID)

	// 3. poll until indexing finishes (status changes from "pending" / "processing" to "ready" / similar)
	waitDocReady(t, bin, env, created.ID, uploaded.ID, 90*time.Second)

	// 4. search chunks → bare []SearchResult
	var results []map[string]any
	runJSONInto(t, bin, env, &results, "search", "chunks", "sample", "--kb", created.ID, "--limit", "5", "--json")
	if len(results) == 0 {
		t.Fatalf("search returned no results")
	}
	t.Logf("search returned %d results", len(results))

	// 5. chat --no-stream --json → bare {answer, references, ...} object
	var chat struct {
		Answer     string           `json:"answer"`
		References []map[string]any `json:"references"`
	}
	runJSONInto(t, bin, env, &chat, "chat", "summarize the document briefly", "--kb", created.ID, "--no-stream", "--json")
	if strings.TrimSpace(chat.Answer) == "" {
		t.Fatalf("chat returned empty answer")
	}
	t.Logf("chat answer (%d chars), %d references", len(chat.Answer), len(chat.References))
	if len(chat.References) == 0 {
		// Soft warning - some servers may not surface references for every
		// question, but the demo flow is supposed to.
		t.Logf("warning: chat returned 0 references (server may have a different config)")
	}
}

func mustEnv(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("e2e: %s not set; skipping (set the env var or run `gh workflow run cli-e2e.yml`)", key)
	}
	return v
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// buildBinary compiles the CLI to a temp dir once per test run. Re-using a
// single binary across sub-cases avoids the multi-second linker cost on each
// step and matches gh acceptance/ build behavior.
func buildBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "weknora")
	// Repo layout: this test sits at cli/acceptance/e2e/, so cli/ is two levels up.
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build cli: %v", err)
	}
	return out
}

// writeContextYAML drops a minimal config.yaml into XDG_CONFIG_HOME so the
// CLI finds a context without needing `weknora context add` (which prompts
// in interactive scenarios). Tests using `auth login` belong to a different
// suite; here we go straight to authenticated calls.
func writeContextYAML(t *testing.T, xdg, host, token string) {
	t.Helper()
	dir := filepath.Join(xdg, "weknora")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir xdg: %v", err)
	}
	yaml := fmt.Sprintf(`current_context: e2e
contexts:
  - name: e2e
    host: %s
    token: %s
`, host, token)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// writeSampleDoc emits a small bilingual doc that gives the embedder enough
// signal for retrieval but stays tiny so indexing finishes within the poll
// window.
func writeSampleDoc(t *testing.T) string {
	t.Helper()
	content := `WeKnora E2E Sample Document

This sample document is used by the WeKnora CLI acceptance test suite to
validate the end-to-end retrieval-augmented generation pipeline.

向量检索的核心思想是把文本通过 embedding 模型映射到高维向量空间,然后通过余弦相似度
等度量找出语义最接近的内容片段。WeKnora 支持 vector + keyword 的混合检索模式。

The hybrid search mode combines vector similarity (semantic) with keyword
matching (lexical) to balance recall and precision.
`
	dir := t.TempDir()
	p := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	return p
}

// waitDocReady polls `doc list` until the uploaded document's status indicates
// indexing is complete. WeKnora server uses a few status values across versions
// ("ready", "completed", "ok") - accept any non-pending/non-processing/non-failed
// state so we don't break on a server-side rename. Failed status fails the test
// fast.
func waitDocReady(t *testing.T, bin string, env []string, kbID, docID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	tick := 2 * time.Second
	for time.Now().Before(deadline) {
		var docs []struct {
			ID          string `json:"id"`
			ParseStatus string `json:"parse_status"`
		}
		runJSONInto(t, bin, env, &docs, "doc", "list", "--kb", kbID, "--page-size", "100", "--json")
		for _, d := range docs {
			if d.ID != docID {
				continue
			}
			low := strings.ToLower(d.ParseStatus)
			switch {
			case low == "failed", low == "error":
				t.Fatalf("doc %s indexing failed: status=%q", docID, d.ParseStatus)
			case low == "pending", low == "processing", low == "":
				// keep waiting
			default:
				t.Logf("doc %s ready (status=%q)", docID, d.ParseStatus)
				return
			}
		}
		time.Sleep(tick)
	}
	t.Fatalf("doc %s did not reach ready within %s", docID, timeout)
}

// run executes the CLI and returns combined stdout. Errors include stderr +
// exit code so failures are debuggable without re-running.
func run(bin string, env []string, args ...string) ([]byte, error) {
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("%s %s: %v\nstderr:\n%s", filepath.Base(bin), strings.Join(args, " "), err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// runJSONInto runs the CLI expecting bare JSON output and decodes stdout
// into out (a struct, slice, or map pointer). Test fails on non-zero exit
// or unparseable JSON.
func runJSONInto(t *testing.T, bin string, env []string, out any, args ...string) {
	t.Helper()
	stdout, err := run(bin, env, args...)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if err := json.Unmarshal(stdout, out); err != nil {
		t.Fatalf("parse JSON from %v: %v\nstdout:\n%s", args, err, string(stdout))
	}
}
