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
// step parses the CLI's JSON envelope to extract IDs for the next step —
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
		// SDK debug off — explicit so the CI run isn't noisy. C1 SDK silence
		// makes this redundant in practice but the explicit flag documents
		// intent.
		"WEKNORA_SDK_DEBUG=",
	)

	// 1. kb create
	kbName := prefix + fmt.Sprintf("%d", time.Now().UnixNano())
	createOut := runJSON(t, bin, env, "kb", "create", "--name", kbName, "--json")
	kbData, ok := createOut["data"].(map[string]any)
	if !ok {
		t.Fatalf("kb create envelope: data not an object: %v", createOut)
	}
	kbID, _ := kbData["id"].(string)
	if kbID == "" {
		t.Fatalf("kb create returned no id: %v", createOut)
	}
	t.Logf("created KB: %s (%s)", kbID, kbName)

	t.Cleanup(func() {
		// Best-effort cleanup; a 404 means the KB was already gone.
		out, err := run(bin, env, "kb", "delete", kbID, "-y", "--json")
		if err != nil {
			t.Logf("cleanup kb delete: %v\n%s", err, out)
		}
	})

	// 2. doc upload
	docPath := writeSampleDoc(t)
	uploadOut := runJSON(t, bin, env, "doc", "upload", docPath, "--kb-id", kbID, "--json")
	docData, _ := uploadOut["data"].(map[string]any)
	docID, _ := docData["id"].(string)
	if docID == "" {
		t.Fatalf("doc upload returned no id: %v", uploadOut)
	}
	t.Logf("uploaded doc: %s", docID)

	// 3. poll until indexing finishes (status changes from "pending" / "processing" to "ready" / similar)
	waitDocReady(t, bin, env, kbID, docID, 90*time.Second)

	// 4. search — verify retrieval returns chunks
	searchOut := runJSON(t, bin, env, "search", "sample", "--kb-id", kbID, "--top-k", "5", "--json")
	searchData, _ := searchOut["data"].(map[string]any)
	results, _ := searchData["results"].([]any)
	if len(results) == 0 {
		t.Fatalf("search returned no results: %v", searchOut)
	}
	t.Logf("search returned %d results", len(results))

	// 5. chat — verify LLM answer + references in --json + --no-stream mode
	//    (--no-stream forces accumulator path; --json gates envelope output)
	chatOut := runJSON(t, bin, env, "chat", "summarize the document briefly", "--kb-id", kbID, "--no-stream", "--json")
	chatData, _ := chatOut["data"].(map[string]any)
	answer, _ := chatData["answer"].(string)
	if strings.TrimSpace(answer) == "" {
		t.Fatalf("chat returned empty answer: %v", chatOut)
	}
	refs, _ := chatData["references"].([]any)
	t.Logf("chat answer (%d chars), %d references", len(answer), len(refs))
	if len(refs) == 0 {
		// Soft warning — some servers may not surface references for every
		// question, but the demo flow is supposed to.
		t.Logf("warning: chat returned 0 references (server may have a different config)")
	}
}

// mustEnv reads an env var and skips the test if missing — keeps the
// suite friendly to community contributors who clone the repo without
// access to the maintainer's E2E secrets.
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
// ("ready", "completed", "ok") — accept any non-pending/non-processing/non-failed
// state so we don't break on a server-side rename. Failed status fails the test
// fast.
func waitDocReady(t *testing.T, bin string, env []string, kbID, docID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	tick := 2 * time.Second
	for time.Now().Before(deadline) {
		out := runJSON(t, bin, env, "doc", "list", "--kb-id", kbID, "--page-size", "100", "--json")
		data, _ := out["data"].(map[string]any)
		items, _ := data["items"].([]any)
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			id, _ := m["id"].(string)
			if id != docID {
				continue
			}
			status, _ := m["status"].(string)
			low := strings.ToLower(status)
			switch {
			case low == "failed", low == "error":
				t.Fatalf("doc %s indexing failed: status=%q", docID, status)
			case low == "pending", low == "processing", low == "":
				// keep waiting
			default:
				t.Logf("doc %s ready (status=%q)", docID, status)
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

// runJSON runs the CLI expecting --json output and parses the envelope.
// Test fails immediately on non-zero exit or unparseable JSON.
func runJSON(t *testing.T, bin string, env []string, args ...string) map[string]any {
	t.Helper()
	out, err := run(bin, env, args...)
	if err != nil {
		t.Fatalf("%v", err)
	}
	var env_ map[string]any
	if err := json.Unmarshal(out, &env_); err != nil {
		t.Fatalf("parse envelope from %v: %v\nstdout:\n%s", args, err, string(out))
	}
	if ok, _ := env_["ok"].(bool); !ok {
		t.Fatalf("envelope ok=false from %v: %s", args, string(out))
	}
	return env_
}
