// Package service - artifact collector.
//
// ArtifactCollector drains a session sandbox's output directory into the
// tenant file service after a skill turn finishes. It is intentionally kept
// stateless and testable: a narrow SandboxArtifactSource interface abstracts
// the sandbox side so unit tests can stub in a fake filesystem, and the
// file service is passed in so blobs land in the same storage backend the
// tenant already uses for uploaded attachments.
//
// Contract:
//   - Never delete files from the sandbox — skills can share files across
//     turns; deletion would break that (spec §2, "不清空输出目录").
//   - Never lazy-create a sandbox: the collector reads from an already-live
//     sandbox and returns an empty slice when none exists.
//   - Best-effort: individual errors are logged and skipped, never returned,
//     so a stray unreadable file cannot block the assistant reply.
//   - Source attribution uses a turn-start filesystem baseline. Files already
//     present before the agent runs are not artifacts of that turn, even when
//     they were uploaded or renamed through Workbench.
package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// SandboxArtifactSource is the narrow subset of sandbox behaviour the
// collector needs. SessionBoundManager satisfies it in production; tests
// use a fake to drive the diff logic without a real sandbox.
type SandboxArtifactSource interface {
	ListSessionFiles(ctx context.Context, sessionID, dir string) ([]sandbox.RemoteDirEntry, error)
	ReadSessionFile(ctx context.Context, sessionID, path string) ([]byte, error)
}

// SessionArtifactStore is the minimal repository surface used to resolve
// explicit references to artifacts from earlier messages.
type SessionArtifactStore interface {
	// KnownArtifacts returns artifacts attached to prior session messages.
	KnownArtifacts(ctx context.Context, sessionID string) ([]types.MessageArtifact, error)
}

type artifactFileState struct {
	modTime time.Time
	size    int64
}

// ArtifactTurnBaseline is an immutable snapshot of output files immediately
// before one agent turn starts. Its fields stay private so callers can only
// obtain a valid baseline through CaptureTurnBaseline.
type ArtifactTurnBaseline struct {
	outputDir string
	files     map[string]artifactFileState
	valid     bool
}

type artifactTurnBaselineContextKey struct{}

// WithArtifactTurnBaseline carries one immutable baseline from stream setup to
// the completion handler without process-wide or session-persistent state.
func WithArtifactTurnBaseline(
	ctx context.Context, baseline ArtifactTurnBaseline,
) context.Context {
	return context.WithValue(ctx, artifactTurnBaselineContextKey{}, baseline)
}

func artifactTurnBaselineFromContext(ctx context.Context) ArtifactTurnBaseline {
	baseline, _ := ctx.Value(artifactTurnBaselineContextKey{}).(ArtifactTurnBaseline)
	return baseline
}

// ArtifactCollectorConfig bounds the collector's I/O and storage footprint.
// All fields have safe zero-value defaults applied by newBoundedConfig.
type ArtifactCollectorConfig struct {
	// OutputDir is the absolute path inside the sandbox to scan. Empty
	// falls back to skills.ArtifactOutputDir() at call time.
	OutputDir string

	// MaxFileBytes is the largest single file that will be persisted.
	// Files larger than this are logged and skipped so a runaway skill
	// cannot exhaust the WeKnora process's memory.
	MaxFileBytes int64
}

// defaultMaxArtifactFileBytes caps a single artifact at 50 MiB. Larger files
// stream from the sandbox in a single ReadFile call today, so we cap here to
// protect the process from OOM. The cap can be raised via config once the
// sandbox client learns to stream to disk.
const defaultMaxArtifactFileBytes int64 = 50 * 1024 * 1024

// Resource binding coordinates for collected artifacts. The owner is the
// assistant message that produced the file (mirrors how knowledge uploads
// bind to "knowledge" and chat attachments bind to "temporary_document"),
// so the resource registry can enumerate / garbage-collect artifacts by
// their owning message instead of only via the messages.artifacts JSONB.
const (
	artifactBindingOwnerType = types.ResourceOwnerMessage
	artifactBindingRelation  = types.ResourceRelationArtifact
)

// ArtifactCollector implements the "drain sandbox artifacts on turn
// completion" step described in
// docs/superpowers/specs/2026-07-10-skill-artifact-download-design.md §4.
type ArtifactCollector struct {
	source      SandboxArtifactSource
	fileService interfaces.FileService
	store       SessionArtifactStore
	// catalog binds each persisted artifact resource to its owning message.
	// Optional: nil when the deployment runs without a resource registry, in
	// which case artifacts are still saved and downloadable — they just are
	// not tracked as owned resources.
	catalog interfaces.ResourceCatalog
	config  ArtifactCollectorConfig
	// resolver lets Collect use the workspace's own sandbox backend. Optional:
	// nil keeps the process-wide source for every workspace.
	resolver sandbox.TenantSandboxResolver
	pinner   *SessionSandboxPinner
	// fallbackMgr is the deployment-wide SessionBoundManager. Sentinel pins
	// ("-") resolve to it rather than a per-config manager.
	fallbackMgr sandbox.Manager
}

// NewArtifactCollector wires up an ArtifactCollector. Callers keep a single
// instance per process; the collector holds no per-turn state. catalog may be
// nil (resource registry disabled); binding is then skipped.
func NewArtifactCollector(
	source SandboxArtifactSource,
	fileService interfaces.FileService,
	store SessionArtifactStore,
	catalog interfaces.ResourceCatalog,
	config ArtifactCollectorConfig,
) *ArtifactCollector {
	return &ArtifactCollector{
		source:      source,
		fileService: fileService,
		store:       store,
		catalog:     catalog,
		config:      newBoundedConfig(config),
	}
}

// NewArtifactCollectorFromSandboxManager is the DI-friendly constructor
// that promotes a sandbox.Manager to SandboxArtifactSource only when the
// underlying implementation actually supports per-session file inspection
// (currently *sandbox.SessionBoundManager). For any other backend the
// returned *ArtifactCollector is nil, which the AgentStreamHandler treats
// as "no artifacts to attach" — matching the graceful-degradation contract
// documented in the design spec.
// The resolver is consulted per turn so a workspace whose own backend supports
// artifacts still gets them even when the process-wide default does not. The
// collector is therefore built whenever either side could supply a source, and
// Collect degrades to "nothing to attach" when neither does.
func NewArtifactCollectorFromSandboxManager(
	sandboxMgr sandbox.Manager,
	sandboxResolver sandbox.TenantSandboxResolver,
	pinner *SessionSandboxPinner,
	fileService interfaces.FileService,
	repo interfaces.MessageRepository,
	catalog interfaces.ResourceCatalog,
) *ArtifactCollector {
	if fileService == nil {
		return nil
	}
	source, _ := sandboxMgr.(SandboxArtifactSource)
	if source == nil && sandboxResolver == nil {
		return nil
	}
	collector := NewArtifactCollector(
		source,
		fileService,
		NewMessageRepoArtifactStore(repo),
		catalog,
		ArtifactCollectorConfig{},
	)
	collector.resolver = sandboxResolver
	collector.pinner = pinner
	collector.fallbackMgr = sandboxMgr
	return collector
}

// sessionSource returns the artifact source for the sandbox pinned to
// sessionID, never the one the agent points at today: the sandbox being drained
// was created earlier and may live on a config the agent no longer selects.
//
// Returns nil when the session has no pin, which Collect treats as "nothing to
// attach".
func (c *ArtifactCollector) sessionSource(ctx context.Context, sessionID string) SandboxArtifactSource {
	source, _, err := c.resolveSessionSource(ctx, sessionID)
	if err != nil {
		logger.Warnf(ctx, "[ArtifactCollector] resolve session source failed: %v", err)
		return nil
	}
	return source
}

// resolveSessionSource also reports whether a sandbox pin existed. A missing
// pin at turn start is a valid empty baseline: the agent may create and pin
// its first sandbox later in the same turn.
func (c *ArtifactCollector) resolveSessionSource(
	ctx context.Context, sessionID string,
) (SandboxArtifactSource, bool, error) {
	if c.resolver == nil {
		return c.source, c.source != nil, nil
	}
	tenantID, _ := types.SandboxTenantIDFromContext(ctx)
	configID, err := sandboxConfigForExistingSandbox(ctx, c.pinner, sessionID)
	if err != nil {
		return nil, false, fmt.Errorf("read sandbox pin: %w", err)
	}
	if configID == "" {
		return nil, false, nil
	}
	mgr, err := resolveTenantSandboxForConfig(
		ctx, c.resolver, c.fallbackMgr, tenantID, configID, nil,
	)
	if err != nil {
		// Refusing to read is the safe failure: substituting another backend
		// would look in the wrong provider account and report "no artifacts".
		return nil, true, fmt.Errorf("resolve sandbox: %w", err)
	}
	if mgr == nil {
		// The pin names the deployment-wide default, which has no per-config
		// manager of its own; the injected process-wide source IS that backend.
		if c.source == nil {
			return nil, true, fmt.Errorf("pinned sandbox has no session filesystem")
		}
		return c.source, true, nil
	}
	if source, ok := mgr.(SandboxArtifactSource); ok {
		return source, true, nil
	}
	if configID == types.SandboxConfigIDGlobalDefault {
		if c.source != nil {
			return c.source, true, nil
		}
	}
	return nil, true, fmt.Errorf("pinned sandbox has no session filesystem")
}

// newBoundedConfig fills in defaults so callers can pass a zero
// ArtifactCollectorConfig without hitting empty-value edge cases.
func newBoundedConfig(cfg ArtifactCollectorConfig) ArtifactCollectorConfig {
	if cfg.MaxFileBytes <= 0 {
		cfg.MaxFileBytes = defaultMaxArtifactFileBytes
	}
	return cfg
}

// CaptureTurnBaseline snapshots output metadata before the agent starts. A
// session without a pin is a valid empty baseline because its first sandbox
// can be created later in the turn. Any real lookup failure leaves the
// baseline invalid so collection fails closed instead of claiming pre-existing
// Workbench files as agent output.
func (c *ArtifactCollector) CaptureTurnBaseline(
	ctx context.Context, sessionID, outputDir string,
) (ArtifactTurnBaseline, error) {
	baseline := ArtifactTurnBaseline{}
	if c == nil || sessionID == "" {
		return baseline, fmt.Errorf("artifact baseline requires collector and session")
	}
	if outputDir == "" {
		outputDir = c.config.OutputDir
	}
	if outputDir == "" {
		return baseline, fmt.Errorf("artifact baseline requires output directory")
	}
	baseline.outputDir = outputDir
	baseline.files = make(map[string]artifactFileState)

	source, pinned, err := c.resolveSessionSource(ctx, sessionID)
	if err != nil {
		return baseline, err
	}
	if source == nil {
		if pinned {
			return baseline, fmt.Errorf("artifact baseline source unavailable")
		}
		baseline.valid = true
		return baseline, nil
	}

	entries, err := source.ListSessionFiles(ctx, sessionID, outputDir)
	if err != nil {
		return baseline, fmt.Errorf("list artifact baseline: %w", err)
	}
	for _, entry := range entries {
		if entry.Type != sandbox.RemoteEntryFile || entry.Path == "" {
			continue
		}
		baseline.files[entry.Path] = artifactFileState{
			modTime: entry.ModTime,
			size:    entry.Size,
		}
	}
	baseline.valid = true
	return baseline, nil
}

// Collect scans the session sandbox's output directory and persists any
// newly-created or newly-modified files to the tenant file service.
//
// The returned slice contains one MessageArtifact per persisted file. When
// no sandbox is bound to the session, or when the source is nil (skill
// backend disabled), Collect returns nil, nil — the caller is expected to
// treat both cases as "nothing to attach" and NOT set message.Artifacts.
//
// Errors returned by Collect are limited to internal invariants (nil
// dependencies). Per-file errors (unreadable, too-large, upload failure)
// are logged and skipped so a single misbehaving artifact never breaks the
// turn.
func (c *ArtifactCollector) Collect(
	ctx context.Context,
	sessionID string,
	messageID string,
	tenantID uint64,
	outputDir string,
	baseline ArtifactTurnBaseline,
) (types.MessageArtifacts, error) {
	return c.collect(ctx, sessionID, messageID, tenantID, outputDir, baseline, nil)
}

// CollectWithNotify is Collect plus a progress hook fired after the sandbox
// listing is filtered, before any file is read or uploaded. The frontend uses
// this to show a toolbar placeholder while object-storage uploads (often a
// few seconds for HTML charts) finish. notify is skipped when nothing will
// be persisted, so ordinary sandbox turns without new files stay quiet.
func (c *ArtifactCollector) CollectWithNotify(
	ctx context.Context,
	sessionID string,
	messageID string,
	tenantID uint64,
	outputDir string,
	notify func(pending int),
) (types.MessageArtifacts, error) {
	baseline := artifactTurnBaselineFromContext(ctx)
	return c.collect(ctx, sessionID, messageID, tenantID, outputDir, baseline, notify)
}

func (c *ArtifactCollector) collect(
	ctx context.Context,
	sessionID string,
	messageID string,
	tenantID uint64,
	outputDir string,
	baseline ArtifactTurnBaseline,
	notify func(pending int),
) (artifacts types.MessageArtifacts, err error) {
	if c == nil || c.fileService == nil {
		logger.Infof(ctx, "[ArtifactCollector] skipped: collector or dependencies nil (session=%s)", sessionID)
		return nil, nil
	}
	if sessionID == "" {
		logger.Infof(ctx, "[ArtifactCollector] skipped: empty sessionID")
		return nil, nil
	}
	if outputDir == "" {
		outputDir = c.config.OutputDir
	}
	if outputDir == "" {
		// Callers should have resolved this via skills.ArtifactOutputDir
		// but we guard here anyway to keep Collect self-contained.
		logger.Infof(ctx, "[ArtifactCollector] skipped: empty outputDir (session=%s)", sessionID)
		return nil, nil
	}
	if !baseline.valid || baseline.outputDir != outputDir {
		logger.Warnf(ctx,
			"[ArtifactCollector] skipped: missing or mismatched turn baseline (session=%s dir=%s)",
			sessionID, outputDir)
		return nil, nil
	}
	source := c.sessionSource(ctx, sessionID)
	if source == nil {
		logger.Infof(ctx,
			"[ArtifactCollector] skipped: sandbox backend has no session filesystem (session=%s)",
			sessionID)
		return nil, nil
	}

	ctx, span := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name: "sandbox.collect_artifacts",
		Input: map[string]interface{}{
			"session_id": sessionID,
			"message_id": messageID,
			"output_dir": outputDir,
		},
	})
	defer func() {
		span.Finish(map[string]interface{}{
			"artifact_count": len(artifacts),
		}, nil, err)
	}()

	logger.Infof(ctx, "[ArtifactCollector] begin session=%s dir=%s", sessionID, outputDir)

	entries, err := source.ListSessionFiles(ctx, sessionID, outputDir)
	if err != nil {
		logger.Warnf(ctx, "[ArtifactCollector] list sandbox files failed: session=%s dir=%s err=%v",
			sessionID, outputDir, err)
		return nil, nil
	}
	if len(entries) == 0 {
		// The most common cause of "download button never appears" is
		// exactly this branch: either the sandbox was already reaped or
		// the skill wrote to a different directory. Logging the exact
		// (session, dir) pair makes it a 30-second grep to confirm.
		logger.Infof(ctx, "[ArtifactCollector] no entries under %s (session=%s) — sandbox reaped or skill wrote elsewhere",
			outputDir, sessionID)
		return nil, nil
	}
	logger.Infof(ctx, "[ArtifactCollector] listed %d entries under %s (session=%s)", len(entries), outputDir, sessionID)

	pending := 0
	for _, entry := range entries {
		if c.acceptEntry(entry, baseline) {
			pending++
		}
	}
	if pending > 0 && notify != nil {
		notify(pending)
	}

	artifacts = make(types.MessageArtifacts, 0, pending)
	persisted := make(map[string]struct{}, pending)
	for _, entry := range entries {
		key := artifactKey(entry.Path, entry.ModTime)
		if _, duplicate := persisted[key]; duplicate {
			continue
		}
		art, ok := c.maybePersist(
			ctx, source, sessionID, messageID, tenantID, entry, baseline,
		)
		if !ok {
			continue
		}
		artifacts = append(artifacts, art)
		persisted[key] = struct{}{}
	}
	logger.Infof(ctx, "[ArtifactCollector] done session=%s listed=%d attached=%d",
		sessionID, len(entries), len(artifacts))
	return artifacts, nil
}

func (c *ArtifactCollector) acceptEntry(
	entry sandbox.RemoteDirEntry, baseline ArtifactTurnBaseline,
) bool {
	if entry.Type != sandbox.RemoteEntryFile {
		return false
	}
	if entry.Path == "" || entry.Name == "" {
		return false
	}
	if entry.Size > c.config.MaxFileBytes {
		return false
	}
	if previous, existed := baseline.files[entry.Path]; existed &&
		previous.size == entry.Size && previous.modTime.Equal(entry.ModTime) {
		return false
	}
	return true
}

// maybePersist runs the per-file pipeline (filter → download → upload →
// build metadata). Returns ok=false when the entry was skipped for any
// reason (present in the turn baseline, too large, upload failed). Skip
// reasons are logged so operators can diagnose empty artifact panels.
func (c *ArtifactCollector) maybePersist(
	ctx context.Context,
	source SandboxArtifactSource,
	sessionID string,
	messageID string,
	tenantID uint64,
	entry sandbox.RemoteDirEntry,
	baseline ArtifactTurnBaseline,
) (types.MessageArtifact, bool) {
	if !c.acceptEntry(entry, baseline) {
		if entry.Type == sandbox.RemoteEntryFile && entry.Size > c.config.MaxFileBytes {
			logger.Warnf(ctx, "[ArtifactCollector] skip oversize artifact: session=%s path=%s size=%d limit=%d",
				sessionID, entry.Path, entry.Size, c.config.MaxFileBytes)
		}
		return types.MessageArtifact{}, false
	}

	data, err := source.ReadSessionFile(ctx, sessionID, entry.Path)
	if err != nil {
		logger.Warnf(ctx, "[ArtifactCollector] read artifact failed: session=%s path=%s err=%v",
			sessionID, entry.Path, err)
		return types.MessageArtifact{}, false
	}
	// A second guard: envd may report a stale size while the file is being
	// re-written; enforce the cap against the actual byte count too.
	if int64(len(data)) > c.config.MaxFileBytes {
		logger.Warnf(ctx, "[ArtifactCollector] skip oversize artifact after read: session=%s path=%s size=%d limit=%d",
			sessionID, entry.Path, len(data), c.config.MaxFileBytes)
		return types.MessageArtifact{}, false
	}

	// Give each blob a UUID-namespaced storage name so concurrent turns
	// cannot collide, and so the storage key itself is unguessable from
	// the outside (defence-in-depth on top of the /artifacts/:index
	// endpoint's ownership check).
	storageName := "artifact_" + uuid.NewString() + "_" + safeFileName(entry.Name)
	storagePath, err := c.fileService.SaveBytes(ctx, data, tenantID, storageName, false)
	if err != nil {
		logger.Warnf(ctx, "[ArtifactCollector] upload artifact failed: session=%s path=%s err=%v",
			sessionID, entry.Path, err)
		return types.MessageArtifact{}, false
	}

	c.bindArtifactResource(ctx, storagePath, messageID)

	return types.MessageArtifact{
		URL:        storagePath,
		FileName:   entry.Name,
		FileType:   strings.ToLower(filepath.Ext(entry.Name)),
		Kind:       types.ArtifactKindForFile(entry.Name),
		FileSize:   int64(len(data)),
		SourcePath: entry.Path,
		ModTime:    entry.ModTime,
		CreatedAt:  time.Now().UTC(),
	}, true
}

// bindArtifactResource records that the freshly-persisted artifact resource
// is owned by its assistant message. Best-effort: a binding failure never
// discards the artifact, because the file is already stored and remains
// downloadable through the /artifacts endpoint regardless of the binding.
//
// The binding is only attempted when (a) the catalog is wired in, (b) we have
// a message ID to own the resource, and (c) SaveBytes actually returned a
// resource:// reference — i.e. the file service is resource-catalog-backed.
// Raw provider paths (no catalog decorator) are left unbound rather than
// generating spurious "invalid resource reference" errors.
func (c *ArtifactCollector) bindArtifactResource(ctx context.Context, ref, messageID string) {
	if c.catalog == nil || messageID == "" {
		return
	}
	if _, ok := types.ParseResourcePath(ref); !ok {
		return
	}
	if err := c.catalog.Bind(ctx, ref, artifactBindingOwnerType, messageID, artifactBindingRelation); err != nil {
		logger.Warnf(ctx, "[ArtifactCollector] bind artifact resource failed: message=%s ref=%s err=%v",
			messageID, ref, err)
	}
}

// ReferencedHistory returns only artifacts from this session explicitly named
// in the answer. Bind these immutable versions to the new message as well, so
// deleting their original message cannot invalidate a later reference.
func (c *ArtifactCollector) ReferencedHistory(ctx context.Context, sessionID, messageID, content string) types.MessageArtifacts {
	if c == nil || c.store == nil {
		return nil
	}
	refs := make(map[string]bool)
	for _, ref := range types.ScanResourceReferences(content) {
		refs[ref] = true
	}
	if len(refs) == 0 {
		return nil
	}
	previous, err := c.store.KnownArtifacts(ctx, sessionID)
	if err != nil {
		logger.Warnf(ctx, "Read referenced artifact history failed: %v", err)
		return nil
	}
	var result types.MessageArtifacts
	for _, artifact := range previous {
		if refs[artifact.URL] {
			result = append(result, artifact)
			c.bindArtifactResource(ctx, artifact.URL, messageID)
			delete(refs, artifact.URL)
		}
	}
	return result
}

// artifactKey is the string form of the (source_path, mtime) tuple used to
// suppress duplicate entries within one sandbox listing.
func artifactKey(path string, mod time.Time) string {
	if mod.IsZero() {
		return path + "\x00"
	}
	return path + "\x00" + mod.UTC().Format(time.RFC3339Nano)
}

// safeFileName strips slashes and backslashes from the original name before
// concatenating it into the storage key. The FileService may or may not
// sanitise on its own; belt-and-suspenders here avoids provider-specific
// surprises (e.g. object stores that treat "/" as delimiter).
func safeFileName(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	if name == "" {
		return "unnamed"
	}
	return name
}
