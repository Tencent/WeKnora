//go:build integration

// Integration coverage for SessionBoundManager's artifact-collection
// primitives (ListSessionFiles / ReadSessionFile / EnsureSessionDir). These
// tests exercise the same code path the ArtifactCollector uses in
// production; failure here means a real CubeSandbox deployment would fail
// to surface skill-generated files even though the unit tests pass.
//
// Run:
//
//	CUBE_API_URL=http://127.0.0.1:33000 \
//	CUBE_PROXY_URL=http://127.0.0.1:12088 \
//	go test -tags=integration -run TestIntegrationSessionArtifacts \
//	    -count=1 ./internal/sandbox/...
package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestIntegrationSessionArtifacts_ListAndRead pins the happy-path contract
// used by the ArtifactCollector:
//   - EnsureSessionDir provisions the output directory on the live sandbox.
//   - A skill-style Execute writes into that directory.
//   - ListSessionFiles surfaces the file with a non-empty absolute path,
//     positive size and RFC3339 mtime.
//   - ReadSessionFile returns the exact bytes the script wrote.
func TestIntegrationSessionArtifacts_ListAndRead(t *testing.T) {
	cfg := integrationConfig(t)
	// Keep the reaper out of the way so the artifact list survives the
	// second RPC.
	cfg.CubeIdleTTL = 10 * time.Minute

	mgr, err := NewSessionBoundManager(cfg)
	if err != nil {
		t.Fatalf("NewSessionBoundManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Cleanup(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	const (
		sessID    = "integration-artifact-happy"
		outputDir = "/workspace/output-artifact-happy"
		payload   = "hello-weknora-artifact\n"
	)

	// Warm up the sandbox so EnsureSessionDir has a live MicroVM to talk
	// to. We keep the executed script minimal on purpose — the artifact
	// path itself is exercised by the plain WriteFile below.
	warmup := writeIntegrationScript(t, "warmup.py", "print('warmup-ok')\n")
	if r, err := mgr.Execute(ctx, &ExecuteConfig{
		Script: warmup, SessionID: sessID, SkipValidation: true,
	}); err != nil || r.ExitCode != 0 {
		t.Fatalf("warmup Execute: err=%v exit=%d stderr=%q", err, safeExit(r), safeStderr(r))
	}

	if err := mgr.EnsureSessionDir(ctx, sessID, outputDir); err != nil {
		t.Fatalf("EnsureSessionDir: %v", err)
	}

	// Second Execute drops a file into the output directory. Using a
	// deterministic filename keeps the assertions below simple.
	writer := writeIntegrationScript(t, "write.py", strings.Join([]string{
		"import os",
		"path = os.environ.get('WEKNORA_SKILL_OUTPUT_DIR')",
		"assert path, 'WEKNORA_SKILL_OUTPUT_DIR not injected'",
		"os.makedirs(path, exist_ok=True)",
		"with open(path + '/note.txt', 'w') as f:",
		"    f.write('" + payload + "')",
		"print('wrote-note')",
		"",
	}, "\n"))

	if r, err := mgr.Execute(ctx, &ExecuteConfig{
		Script:         writer,
		SessionID:      sessID,
		SkipValidation: true,
		Env:            map[string]string{"WEKNORA_SKILL_OUTPUT_DIR": outputDir},
	}); err != nil || r.ExitCode != 0 {
		t.Fatalf("writer Execute: err=%v exit=%d stderr=%q", err, safeExit(r), safeStderr(r))
	}

	// List — the file must show up with a stable absolute path.
	entries, err := mgr.ListSessionFiles(ctx, sessID, outputDir)
	if err != nil {
		t.Fatalf("ListSessionFiles: %v", err)
	}
	var target *DirEntry
	for i := range entries {
		if entries[i].Name == "note.txt" {
			target = &entries[i]
			break
		}
	}
	if target == nil {
		t.Fatalf("note.txt not listed under %s (got %d entries: %+v)", outputDir, len(entries), entries)
	}
	if target.Path == "" || !strings.HasPrefix(target.Path, outputDir) {
		t.Fatalf("listed path %q must be absolute and rooted at %s", target.Path, outputDir)
	}
	if target.Size <= 0 {
		t.Fatalf("listed size = %d, want > 0", target.Size)
	}
	if target.Type == "dir" || target.Type == "directory" {
		t.Fatalf("note.txt reported as directory: %+v", target)
	}
	// The collector's parseModTime tolerates missing values, but production
	// envd is expected to return a non-empty timestamp; assert it here so
	// dedupe by (path, mtime) stays reliable.
	if strings.TrimSpace(target.ModifiedAt) == "" {
		t.Fatalf("listed mod_time empty: %+v", target)
	}

	// Read — byte-identical round-trip.
	got, err := mgr.ReadSessionFile(ctx, sessID, target.Path)
	if err != nil {
		t.Fatalf("ReadSessionFile: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("ReadSessionFile mismatch: got=%q want=%q", got, payload)
	}
}

// TestIntegrationSessionArtifacts_EmptyOnUnknownSession pins the "no live
// sandbox" branch: sessions that have never executed anything must return
// an empty list (nil error) so the collector can treat them as no-ops
// without needing to distinguish "sandbox missing" from "sandbox empty".
func TestIntegrationSessionArtifacts_EmptyOnUnknownSession(t *testing.T) {
	cfg := integrationConfig(t)
	mgr, err := NewSessionBoundManager(cfg)
	if err != nil {
		t.Fatalf("NewSessionBoundManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Cleanup(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	entries, err := mgr.ListSessionFiles(ctx, "integration-nonexistent-session", "/workspace/output")
	if err != nil {
		t.Fatalf("ListSessionFiles on unknown session: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty list for unknown session; got %d entries", len(entries))
	}

	// ReadSessionFile has stricter semantics (a caller here means "I hold
	// a path from ListSessionFiles"), so it must fail loudly rather than
	// silently returning empty bytes.
	if _, err := mgr.ReadSessionFile(ctx, "integration-nonexistent-session", "/workspace/output/anything"); err == nil {
		t.Fatal("ReadSessionFile on unknown session should have errored")
	}
}
