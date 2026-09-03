//go:build e2b_integration

// E2B integration tests use a dedicated build tag so they can run independently
// of the legacy Cube integration suite, which currently has a separate
// newCubeClient compile blocker in this workspace.
//
// Run with:
//
//	go test -tags=e2b_integration ./internal/sandbox \
//	  -run '^TestE2BIntegration' -count=1 -v -timeout=15m

package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	e2bIntegrationTTL          = 30 * time.Second
	e2bIntegrationHTTPTimeout  = 30 * time.Second
	e2bIntegrationPauseTimeout = 3 * time.Minute
	e2bIntegrationPollInterval = 5 * time.Second
)

func TestE2BIntegrationLifecycleParity(t *testing.T) {
	cfg := e2bIntegrationConfig(t)
	store := NewMemorySessionSandboxBindingStore()
	key := SessionSandboxKey{
		TenantID:  1,
		SessionID: fmt.Sprintf("e2b-integration-%d", time.Now().UnixNano()),
	}
	createRequest := RemoteCreateRequest{
		TemplateID: cfg.E2BTemplate,
		Timeout: RemoteTimeoutPolicy{
			Mode:       RemoteTimeoutExplicit,
			Value:      e2bIntegrationTTL,
			Action:     RemoteOnTimeoutPause,
			AutoResume: true,
		},
	}

	firstClient := newE2BIntegrationClient(t, cfg)
	firstLifecycle := newE2BIntegrationLifecycle(t, firstClient, store, createRequest)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	var sandboxID string
	destroyed := false
	t.Cleanup(func() {
		if sandboxID == "" || destroyed {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		if err := firstClient.Delete(cleanupCtx, sandboxID); err != nil {
			if CanReplaceRemoteBinding(err) {
				t.Logf("best-effort cleanup found E2B sandbox %s already absent", sandboxID)
				return
			}
			t.Errorf("best-effort cleanup failed for E2B sandbox %s", sandboxID)
			return
		}
		t.Logf("best-effort cleanup deleted E2B sandbox %s", sandboxID)
	})

	firstHandle, err := firstLifecycle.Resolve(ctx, key)
	if err != nil {
		t.Fatalf("initial lifecycle Resolve: %v", err)
	}
	sandboxID = firstHandle.ID()
	if sandboxID == "" {
		t.Fatal("initial lifecycle Resolve returned an empty sandbox ID")
	}

	expectedMetadata := firstLifecycle.metadata(key)
	if !metadataMatches(firstHandle.Metadata(), expectedMetadata) {
		t.Fatalf("lifecycle-created sandbox handle omitted ownership metadata: got=%v want=%v",
			firstHandle.Metadata(), expectedMetadata)
	}

	base := "/tmp/weknora-e2b-integration"
	path := base + "/state.txt"
	payload := []byte("e2b lifecycle state persists\n")
	if err := firstClient.MakeDir(ctx, firstHandle, base); err != nil {
		t.Fatalf("MakeDir: %v", err)
	}
	if err := firstClient.WriteFile(ctx, firstHandle, path, payload); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	entries, err := firstClient.ListDir(ctx, firstHandle, base)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	found := false
	for _, entry := range entries {
		if entry.Name == "state.txt" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ListDir did not return state.txt: %#v", entries)
	}
	stat, err := firstClient.Stat(ctx, firstHandle, path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if stat == nil || stat.Type != RemoteEntryFile || stat.Size != int64(len(payload)) {
		t.Fatalf("Stat returned unexpected file metadata: %#v", stat)
	}
	content, err := firstClient.ReadFile(ctx, firstHandle, path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(content, payload) {
		t.Fatalf("ReadFile content mismatch: got=%q want=%q", content, payload)
	}

	waitForE2BSandboxPause(t, ctx, firstClient, sandboxID, expectedMetadata)

	secondClient := newE2BIntegrationClient(t, cfg)
	secondLifecycle := newE2BIntegrationLifecycle(t, secondClient, store, createRequest)
	secondHandle, err := secondLifecycle.Resolve(ctx, key)
	if err != nil {
		t.Fatalf("Resolve after simulated process restart: %v", err)
	}
	if secondHandle.ID() != sandboxID {
		t.Fatalf("Resolve after restart returned sandbox %s, want %s", secondHandle.ID(), sandboxID)
	}
	content, err = secondClient.ReadFile(ctx, secondHandle, path)
	if err != nil {
		t.Fatalf("ReadFile after automatic resume: %v", err)
	}
	if !bytes.Equal(content, payload) {
		t.Fatalf("filesystem state did not survive pause/resume: got=%q want=%q", content, payload)
	}

	deleted, err := store.DeleteIfMatch(ctx, key, SandboxTypeE2B, sandboxID)
	if err != nil {
		t.Fatalf("delete binding while preserving remote sandbox: %v", err)
	}
	if !deleted {
		t.Fatal("binding disappeared before metadata-recovery test")
	}

	thirdClient := newE2BIntegrationClient(t, cfg)
	thirdLifecycle := newE2BIntegrationLifecycle(t, thirdClient, store, createRequest)
	thirdHandle, err := thirdLifecycle.Resolve(ctx, key)
	if err != nil {
		t.Fatalf("Resolve after binding loss: %v", err)
	}
	if thirdHandle.ID() != sandboxID {
		t.Fatalf("metadata recovery claimed sandbox %s, want original %s", thirdHandle.ID(), sandboxID)
	}

	candidates, err := thirdClient.List(ctx, RemoteListFilter{Metadata: expectedMetadata})
	if err != nil {
		t.Fatalf("List ownership metadata candidates: %v", err)
	}
	nonterminal := make([]RemoteSandboxSummary, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.State != RemoteStateTerminal {
			nonterminal = append(nonterminal, candidate)
		}
	}
	if len(nonterminal) != 1 || nonterminal[0].ID != sandboxID {
		t.Fatalf("metadata recovery left non-unique candidates: got=%#v want only %s",
			nonterminal, sandboxID)
	}

	if err := thirdClient.Remove(ctx, thirdHandle, path); err != nil {
		t.Fatalf("Remove file: %v", err)
	}
	if err := thirdClient.Remove(ctx, thirdHandle, base); err != nil {
		t.Fatalf("Remove directory: %v", err)
	}
	if err := thirdLifecycle.Destroy(ctx, key); err != nil {
		t.Fatalf("lifecycle Destroy: %v", err)
	}
	binding, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("read binding after lifecycle Destroy: %v", err)
	}
	if binding != nil {
		t.Fatalf("lifecycle Destroy left binding behind: %#v", binding)
	}
	waitForE2BSandboxDeletion(t, ctx, thirdClient, sandboxID)
	destroyed = true
}

func e2bIntegrationConfig(t *testing.T) *Config {
	t.Helper()
	apiKey := firstNonEmptyEnvironment(
		"E2B_INTEGRATION_API_KEY",
		"E2B_API_KEY",
	)
	template := firstNonEmptyEnvironment(
		"E2B_INTEGRATION_TEMPLATE",
		"E2B_TEMPLATE",
	)
	if apiKey == "" || template == "" {
		t.Skip("E2B integration requires an API key and template")
	}

	cfg := DefaultConfig()
	cfg.Type = SandboxTypeE2B
	cfg.E2BAPIKey = apiKey
	cfg.E2BAPIURL = strings.TrimSpace(os.Getenv("E2B_INTEGRATION_API_URL"))
	cfg.E2BSandboxDomain = strings.TrimSpace(os.Getenv("E2B_INTEGRATION_SANDBOX_DOMAIN"))
	cfg.E2BTemplate = template
	cfg.E2BSandboxTTL = e2bIntegrationTTL
	cfg.E2BHTTPTimeout = e2bIntegrationHTTPTimeout
	return cfg
}

func firstNonEmptyEnvironment(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func newE2BIntegrationClient(t *testing.T, cfg *Config) *E2BRemoteClient {
	t.Helper()
	client, err := NewE2BRemoteClient(cfg)
	if err != nil {
		t.Fatalf("NewE2BRemoteClient: %v", err)
	}
	return client
}

func newE2BIntegrationLifecycle(
	t *testing.T,
	client *E2BRemoteClient,
	store SessionSandboxBindingStore,
	createRequest RemoteCreateRequest,
) *remoteSessionLifecycle {
	t.Helper()
	lifecycle, err := newRemoteSessionLifecycle(
		client,
		store,
		PermissiveSessionExistenceChecker{},
		createRequest,
		time.Minute,
		"",
	)
	if err != nil {
		t.Fatalf("newRemoteSessionLifecycle: %v", err)
	}
	return lifecycle
}

func waitForE2BSandboxPause(
	t *testing.T,
	parent context.Context,
	client *E2BRemoteClient,
	sandboxID string,
	expectedMetadata map[string]string,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, e2bIntegrationPauseTimeout)
	defer cancel()
	ticker := time.NewTicker(e2bIntegrationPollInterval)
	defer ticker.Stop()

	var lastState RemoteSandboxState
	var lastRawState string
	for {
		summary, err := client.Get(ctx, sandboxID)
		if err != nil {
			t.Fatalf("E2B control plane did not return the sandbox as paused with lifecycle "+
				"metadata; Get stopped returning sandbox %s before the %s deadline: %v",
				sandboxID, e2bIntegrationPauseTimeout, err)
		}
		if summary == nil {
			t.Fatal("control-plane Get returned nil while waiting for pause")
		}
		lastState = summary.State
		lastRawState = summary.RawState
		if summary.State == RemoteStatePaused {
			if !metadataMatches(summary.Metadata, expectedMetadata) {
				t.Fatalf("paused sandbox omitted lifecycle metadata: got=%v want=%v",
					summary.Metadata, expectedMetadata)
			}
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("E2B control plane did not report paused sandbox with metadata within %s; "+
				"last state=%s raw_state=%q", e2bIntegrationPauseTimeout, lastState, lastRawState)
		case <-ticker.C:
		}
	}
}

func waitForE2BSandboxDeletion(
	t *testing.T,
	parent context.Context,
	client *E2BRemoteClient,
	sandboxID string,
) {
	t.Helper()
	const deletionTimeout = time.Minute
	ctx, cancel := context.WithTimeout(parent, deletionTimeout)
	defer cancel()
	ticker := time.NewTicker(e2bIntegrationPollInterval)
	defer ticker.Stop()

	var lastState RemoteSandboxState
	for {
		summary, err := client.Get(ctx, sandboxID)
		if IsRemoteNotFound(err) {
			return
		}
		if err != nil {
			t.Fatalf("Get while verifying lifecycle Destroy: %v", err)
		}
		if summary != nil {
			lastState = summary.State
		}

		select {
		case <-ctx.Done():
			t.Fatalf("E2B sandbox %s remained visible for %s after lifecycle Destroy; "+
				"last state=%s", sandboxID, deletionTimeout, lastState)
		case <-ticker.C:
		}
	}
}
