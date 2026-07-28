//go:build acceptance_e2e

// Package e2e_test drives the WeKnora CLI binary against a real running
// server to validate the RAG closing loop end-to-end.
//
// Build tag isolation: //go:build acceptance_e2e excludes this file from
// the default `go test ./...` so the e2e suite only runs when explicitly
// requested. To run:
//
//	cd cli
//	WEKNORA_E2E_HOST=https://kb.example.com \
//	WEKNORA_E2E_TOKEN=eyJhbGc... \
//	go test -tags=acceptance_e2e -v ./acceptance/e2e/...
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

// TestRAGFullLoop walks the demo MVP path: link a profile, create a KB,
// upload a doc, wait for indexing, search it, then chat against it. Each
// step parses the CLI's bare JSON to extract IDs for the next step -
// validating both functional behavior and wire-contract stability.
func TestRAGFullLoop(t *testing.T) {
	host := mustEnv(t, "WEKNORA_E2E_HOST")
	token := mustEnv(t, "WEKNORA_E2E_TOKEN")
	prefix := envOr("WEKNORA_E2E_KB_NAME_PREFIX", "cli-e2e-")

	bin := buildBinary(t)
	xdg := t.TempDir()

	// Inject host + token as WEKNORA_HOST / WEKNORA_TOKEN env vars. The
	// CLI's env-credential path (buildClientFromEnv) takes precedence
	// over profile config, so this bypasses the secrets-store dance
	// entirely — no keychain access, no file:// ref plumbing. This is
	// the most robust path for CI and local e2e alike.
	env := append(os.Environ(),
		"XDG_CONFIG_HOME="+xdg,
		"XDG_CACHE_HOME="+filepath.Join(xdg, "cache"),
		"WEKNORA_HOST="+host,
		"WEKNORA_TOKEN="+token,
		// SDK debug off - explicit so the CI run isn't noisy.
		"WEKNORA_LOG_LEVEL=error",
	)

	// 1. kb create → KnowledgeBase object (envelope-wrapped: {ok, data})
	// The CLI now requires explicit model binding so uploaded docs are
	// immediately searchable. Use the server's builtin model IDs (they
	// exist on every deployment that configures builtin_models.yaml).
	embeddingModel := envOr("WEKNORA_E2E_EMBEDDING_MODEL", "Qwen/Qwen3-Embedding-8B")
	chatModel := envOr("WEKNORA_E2E_CHAT_MODEL", "deepseek-ai/DeepSeek-V3.2")
	kbName := prefix + fmt.Sprintf("%d", time.Now().UnixNano())
	var created struct {
		OK   bool `json:"ok"`
		Data struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	runJSONInto(t, bin, env, &created, "kb", "create", kbName,
		"--embedding-model", embeddingModel,
		"--chat-model", chatModel,
		"--format", "json")
	if created.Data.ID == "" {
		t.Fatalf("kb create returned no id")
	}
	t.Logf("created KB: %s (%s)", created.Data.ID, kbName)

	t.Cleanup(func() {
		// Best-effort cleanup; a 404 means the KB was already gone.
		out, err := run(bin, env, "kb", "delete", created.Data.ID, "-y", "--format", "json")
		if err != nil {
			t.Logf("cleanup kb delete: %v\n%s", err, out)
		}
	})

	// 2. doc upload → Knowledge object (envelope-wrapped: {ok, data})
	docPath := writeSampleDoc(t)
	var uploaded struct {
		OK   bool `json:"ok"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	runJSONInto(t, bin, env, &uploaded, "doc", "upload", docPath, "--kb", created.Data.ID, "--format", "json")
	if uploaded.Data.ID == "" {
		t.Fatalf("doc upload returned no id")
	}
	t.Logf("uploaded doc: %s", uploaded.Data.ID)

	// 3. poll until indexing finishes (status changes from "pending" / "processing" to "ready" / similar)
	waitDocReady(t, bin, env, created.Data.ID, uploaded.Data.ID, 90*time.Second)

	// 4. search chunks → SearchResult list (envelope-wrapped: {ok, data})
	var searchResp struct {
		OK   bool              `json:"ok"`
		Data []map[string]any `json:"data"`
	}
	runJSONInto(t, bin, env, &searchResp, "search", "chunks", "sample", "--kb", created.Data.ID, "--limit", "5", "--format", "json")
	results := searchResp.Data
	if len(results) == 0 {
		t.Fatalf("search returned no results")
	}
	t.Logf("search returned %d results", len(results))

	// 5. chat --format json → bounded answer-event envelope (--reference for citations)
	var chatEnv struct {
		OK   bool `json:"ok"`
		Data struct {
			Events []struct {
				ResponseType        string `json:"response_type"`
				Content             string `json:"content"`
				KnowledgeReferences []struct {
					ChunkID string `json:"chunk_id"`
				} `json:"knowledge_references"`
			} `json:"events"`
			SessionID string `json:"session_id"`
		} `json:"data"`
	}
	runJSONInto(t, bin, env, &chatEnv, "chat", "summarize the document briefly", "--kb", created.Data.ID, "--format", "json", "--reference")
	if !chatEnv.OK {
		t.Fatal("chat ok=false")
	}
	var answer strings.Builder
	refCount := 0
	for _, ev := range chatEnv.Data.Events {
		if ev.ResponseType == "answer" {
			answer.WriteString(ev.Content)
		}
		refCount += len(ev.KnowledgeReferences)
	}
	if strings.TrimSpace(answer.String()) == "" {
		t.Fatalf("chat returned empty answer")
	}
	t.Logf("chat answer (%d chars), %d reference indexes", len(answer.String()), refCount)
	if refCount == 0 {
		// Soft warning - some servers may not surface references for every
		// question, but the demo flow is supposed to.
		t.Logf("warning: chat returned 0 reference indexes (server may have a different config)")
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
	type docItem struct {
		ID          string `json:"id"`
		ParseStatus string `json:"parse_status"`
	}
	for time.Now().Before(deadline) {
		// doc list is envelope-wrapped: {ok, data: [...]} (bare array)
		var resp struct {
			OK   bool      `json:"ok"`
			Data []docItem `json:"data"`
		}
		runJSONInto(t, bin, env, &resp, "doc", "list", "--kb", kbID, "--page-size", "100", "--format", "json")
		for _, d := range resp.Data {
			if d.ID != docID {
				continue
			}
			low := strings.ToLower(d.ParseStatus)
			switch {
			case low == "failed", low == "error":
				t.Fatalf("doc %s indexing failed: status=%q", docID, d.ParseStatus)
			case low == "pending", low == "processing", low == "finalizing", low == "":
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
