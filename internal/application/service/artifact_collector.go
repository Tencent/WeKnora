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
//   - De-duplication by (SourcePath, ModTime): if a prior message in the
//     same session already recorded the same (path, mtime), skip it.
package service

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
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

// SessionArtifactStore is the minimal repository surface the collector needs
// to compute the "already recorded" set for a session. In production it is
// backed by the message repository; in tests it is stubbed with an in-memory
// map so the collector can be exercised without a database.
type SessionArtifactStore interface {
	// KnownArtifacts returns every (SourcePath, ModTime) pair already
	// attached to any prior message of the session. The returned set is
	// unordered and safe to mutate by the caller.
	KnownArtifacts(ctx context.Context, sessionID string) ([]types.MessageArtifact, error)
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
	if c.resolver == nil {
		return c.source
	}
	tenantID, _ := types.TenantIDFromContext(ctx)
	configID, err := sandboxConfigForExistingSandbox(ctx, c.pinner, sessionID)
	if err != nil {
		logger.Warnf(ctx, "[ArtifactCollector] read sandbox pin failed: %v", err)
		return nil
	}
	if configID == "" {
		return nil
	}
	mgr, err := resolveTenantSandboxForConfig(
		ctx, c.resolver, c.fallbackMgr, tenantID, configID, nil,
	)
	if err != nil {
		// Refusing to read is the safe failure: substituting another backend
		// would look in the wrong provider account and report "no artifacts".
		logger.Warnf(ctx, "[ArtifactCollector] resolve sandbox failed: %v", err)
		return nil
	}
	if mgr == nil {
		// The pin names the deployment-wide default, which has no per-config
		// manager of its own; the injected process-wide source IS that backend.
		return c.source
	}
	if source, ok := mgr.(SandboxArtifactSource); ok {
		return source
	}
	if configID == types.SandboxConfigIDGlobalDefault {
		return c.source
	}
	return nil
}

// newBoundedConfig fills in defaults so callers can pass a zero
// ArtifactCollectorConfig without hitting empty-value edge cases.
func newBoundedConfig(cfg ArtifactCollectorConfig) ArtifactCollectorConfig {
	if cfg.MaxFileBytes <= 0 {
		cfg.MaxFileBytes = defaultMaxArtifactFileBytes
	}
	return cfg
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
) (types.MessageArtifacts, error) {
	return c.collect(ctx, sessionID, messageID, tenantID, outputDir, nil)
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
	return c.collect(ctx, sessionID, messageID, tenantID, outputDir, notify)
}

func (c *ArtifactCollector) collect(
	ctx context.Context,
	sessionID string,
	messageID string,
	tenantID uint64,
	outputDir string,
	notify func(pending int),
) (types.MessageArtifacts, error) {
	if c == nil || c.fileService == nil {
		logger.Infof(ctx, "[ArtifactCollector] skipped: collector or dependencies nil (session=%s)", sessionID)
		return nil, nil
	}
	source := c.sessionSource(ctx, sessionID)
	if source == nil {
		logger.Infof(ctx,
			"[ArtifactCollector] skipped: sandbox backend has no session filesystem (session=%s)",
			sessionID)
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

	// Build a "already recorded" set so we don't double-attach the same
	// file when several turns share the sandbox. Errors here degrade to an
	// empty set: attaching duplicates is a soft failure, aborting is not.
	known := c.loadKnownSet(ctx, sessionID)
	if len(known) > 0 {
		logger.Infof(ctx, "[ArtifactCollector] known set size=%d (session=%s)", len(known), sessionID)
	}

	pending := 0
	for _, entry := range entries {
		if c.acceptEntry(entry, known) {
			pending++
		}
	}
	if pending > 0 && notify != nil {
		notify(pending)
	}

	artifacts := make(types.MessageArtifacts, 0, pending)
	for _, entry := range entries {
		art, ok := c.maybePersist(ctx, source, sessionID, messageID, tenantID, entry, known)
		if !ok {
			continue
		}
		artifacts = append(artifacts, art)
		// Adding to the known set inside the loop protects us against the
		// pathological case where ListSessionFiles returns the same path
		// twice (envd hasn't been observed to do so, but future-proofing
		// the loop is cheap).
		known[artifactKey(art.SourcePath, art.ModTime)] = struct{}{}
	}
	logger.Infof(ctx, "[ArtifactCollector] done session=%s listed=%d attached=%d",
		sessionID, len(entries), len(artifacts))
	return artifacts, nil
}

// loadKnownSet returns the (source_path, mod_time) tuples already recorded
// against the session. Empty on error so the caller can proceed.
func (c *ArtifactCollector) loadKnownSet(ctx context.Context, sessionID string) map[string]struct{} {
	set := map[string]struct{}{}
	if c.store == nil {
		return set
	}
	prev, err := c.store.KnownArtifacts(ctx, sessionID)
	if err != nil {
		logger.Warnf(ctx, "[ArtifactCollector] load previous artifacts failed: session=%s err=%v",
			sessionID, err)
		return set
	}
	for _, p := range prev {
		set[artifactKey(p.SourcePath, p.ModTime)] = struct{}{}
	}
	return set
}

func (c *ArtifactCollector) acceptEntry(entry sandbox.RemoteDirEntry, known map[string]struct{}) bool {
	if entry.Type != sandbox.RemoteEntryFile {
		return false
	}
	if entry.Path == "" || entry.Name == "" {
		return false
	}
	if entry.Size > c.config.MaxFileBytes {
		return false
	}
	if _, seen := known[artifactKey(entry.Path, entry.ModTime)]; seen {
		return false
	}
	return true
}

// maybePersist runs the per-file pipeline (filter → download → upload →
// build metadata). Returns ok=false when the entry was skipped for any
// reason (already known, too large, upload failed). All skip reasons are
// logged so operators can diagnose empty artifact panels.
func (c *ArtifactCollector) maybePersist(
	ctx context.Context,
	source SandboxArtifactSource,
	sessionID string,
	messageID string,
	tenantID uint64,
	entry sandbox.RemoteDirEntry,
	known map[string]struct{},
) (types.MessageArtifact, bool) {
	if !c.acceptEntry(entry, known) {
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

// artifactKey is the string form of the (source_path, mtime) tuple used to
// de-duplicate artifacts across messages. mtime is normalised to UTC + RFC3339
// nano so equality is stable across time-zone or precision differences
// between sandbox envd builds.
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
