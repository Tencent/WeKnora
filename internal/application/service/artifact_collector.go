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
//     turns; deletion would break that ("不清空输出目录").
//   - Never lazy-create a sandbox: the collector reads from an already-live
//     sandbox and returns an empty slice when none exists.
//   - Best-effort: individual errors are logged and skipped, never returned,
//     so a stray unreadable file cannot block the assistant reply.
//   - De-duplication by (SourcePath, ModTime), with a content-hash check
//     when mtime changed: git checkout refreshes mtime on unchanged bytes
//     and must not re-attach; a same-size in-place rewrite must.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
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

// SessionArtifactStore is the minimal repository surface the collector needs
// to compute the "already recorded" set for a session. In production it is
// backed by the message repository; in tests it is stubbed with an in-memory
// map so the collector can be exercised without a database.
type SessionArtifactStore interface {
	// KnownArtifacts returns every artifact already attached to any prior
	// message of the session. The collector uses SourcePath + ModTime as the
	// cheap identity and ContentHash (or the stored blob) when mtime moved.
	KnownArtifacts(ctx context.Context, sessionID string) ([]types.MessageArtifact, error)
	// RecordRestoredMtime writes the sandbox mtime observed after a same-content
	// restore onto this session's copied artifacts so the next collect can skip
	// by path+mtime. It must not touch any other session. Best-effort.
	RecordRestoredMtime(ctx context.Context, sessionID, sourcePath string, mod time.Time, hash string) error
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

// ArtifactCollector drains skill-generated files from the sandbox when a
// turn completes.
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
	// host answers "is this an unpinned host session?" so Collect can drain
	// Lite workspaces that never write a sandbox pin. Optional: nil keeps
	// the remote "no pin, nothing to attach" path.
	host *HostSessionResolver
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
	host *HostSessionResolver,
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
	collector.host = host
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
	sessionTenantID, _ := types.TenantIDFromContext(ctx)
	pin, err := sandboxConfigForExistingSandbox(ctx, c.pinner, sessionID)
	if err != nil {
		logger.Warnf(ctx, "[ArtifactCollector] read sandbox pin failed: %v", err)
		return nil
	}
	if pin.IsZero() {
		return c.hostSessionSource(ctx, sessionID)
	}
	mgr, err := resolveTenantSandboxForConfig(
		ctx, c.resolver, c.fallbackMgr, pin.TenantOr(sessionTenantID), pin.ConfigID, nil,
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
	if pin.ConfigID == types.SandboxConfigIDGlobalDefault {
		return c.source
	}
	return nil
}

func (c *ArtifactCollector) hostSessionSource(ctx context.Context, sessionID string) SandboxArtifactSource {
	if c.host == nil {
		return nil
	}
	mgr := c.host.HostManagerFor(ctx, sessionID)
	if mgr == nil {
		return nil
	}
	if source, ok := mgr.(SandboxArtifactSource); ok {
		return source
	}
	return nil
}

// CollectTarget reports the directory Collect should scan for this session,
// and whether collection must be skipped entirely.
//
// skip is true when collecting would scan the user's project: the backend
// advertised a workspace with no separate output tree, OutputDir is Root, or
// the layout provider failed. A nil source is also skip: there is nothing to
// drain, and skip=false would let the caller fill /workspace/output. An empty
// dir with skip false means the backend advertises no layout at all — the
// caller's remote default applies.
//
// The directory and the skip decision come from one lookup on purpose. Asking
// twice re-resolved the session's sandbox (a pin read plus a manager resolve)
// and let the two answers disagree: a second lookup that failed after the
// first succeeded returned no directory, sending a host session's collection
// back to the remote /workspace/output.
func (c *ArtifactCollector) CollectTarget(ctx context.Context, sessionID string) (string, bool) {
	if c == nil {
		return "", true
	}
	source := c.sessionSource(ctx, sessionID)
	if source == nil {
		return "", true
	}
	provider, ok := source.(sandbox.SessionWorkspaceLayoutProvider)
	if !ok || provider == nil {
		return "", false
	}
	layout, err := provider.SessionWorkspaceLayout(ctx, sessionID)
	if err != nil {
		return "", true
	}
	layout = layout.Normalized()
	if layout.Root == "" || layout.OutputDir == "" || layout.OutputDir == layout.Root {
		return "", true
	}
	return layout.OutputDir, false
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

// CollectWithNotify is Collect plus a progress hook fired after files have
// been hashed and we know they will be uploaded. The frontend uses this to
// show a toolbar placeholder while object-storage uploads (often a few
// seconds for HTML charts) finish. notify is skipped when nothing will be
// persisted, so a fork restore that only refreshes mtime stays quiet.
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
) (artifacts types.MessageArtifacts, err error) {
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

	ctx = sandbox.WithSessionFileOperation(ctx)
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
	if known.len() > 0 {
		logger.Infof(ctx, "[ArtifactCollector] known set size=%d (session=%s)", known.len(), sessionID)
	}

	// pending uploads are files that passed the hash check. Counting
	// acceptEntry (mtime miss) before the read would treat a git restore as
	// "about to persist" and spin the toolbar on an empty attach.
	var uploads []pendingArtifactUpload
	for _, entry := range entries {
		data, hash, ok := c.readNewContent(ctx, source, sessionID, entry, known)
		if !ok {
			continue
		}
		uploads = append(uploads, pendingArtifactUpload{entry: entry, data: data, hash: hash})
		known.remember(types.MessageArtifact{
			SourcePath: entry.Path, ModTime: entry.ModTime, ContentHash: hash, FileSize: int64(len(data)),
		})
	}
	if len(uploads) > 0 && notify != nil {
		notify(len(uploads))
	}

	artifacts = make(types.MessageArtifacts, 0, len(uploads))
	for _, item := range uploads {
		art, ok := c.persistBytes(ctx, sessionID, messageID, tenantID, item.entry, item.data, item.hash)
		if !ok {
			continue
		}
		artifacts = append(artifacts, art)
		known.remember(art)
	}
	logger.Infof(ctx, "[ArtifactCollector] done session=%s listed=%d attached=%d",
		sessionID, len(entries), len(artifacts))
	return artifacts, nil
}

// loadKnownSet returns the artifacts already recorded against the session.
// Empty on error so the caller can proceed.
func (c *ArtifactCollector) loadKnownSet(ctx context.Context, sessionID string) *artifactKnownSet {
	set := newArtifactKnownSet()
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
		if p.Deleted() {
			set.rememberDeleted(p)
			continue
		}
		set.remember(p)
	}
	return set
}

func (c *ArtifactCollector) acceptEntry(entry sandbox.RemoteDirEntry, known *artifactKnownSet) bool {
	if entry.Type != sandbox.RemoteEntryFile {
		return false
	}
	if entry.Path == "" || entry.Name == "" {
		return false
	}
	if entry.Size > c.config.MaxFileBytes {
		return false
	}
	return !known.seenMtime(entry.Path, entry.ModTime)
}

type pendingArtifactUpload struct {
	entry sandbox.RemoteDirEntry
	data  []byte
	hash  string
}

// readNewContent downloads an accepted file and returns its bytes when the
// content is new. Same-content restores update this session's mtime and
// return ok=false so they are not counted as pending uploads.
func (c *ArtifactCollector) readNewContent(
	ctx context.Context,
	source SandboxArtifactSource,
	sessionID string,
	entry sandbox.RemoteDirEntry,
	known *artifactKnownSet,
) ([]byte, string, bool) {
	if !c.acceptEntry(entry, known) {
		if entry.Type == sandbox.RemoteEntryFile && entry.Size > c.config.MaxFileBytes {
			logger.Warnf(ctx, "[ArtifactCollector] skip oversize artifact: session=%s path=%s size=%d limit=%d",
				sessionID, entry.Path, entry.Size, c.config.MaxFileBytes)
		}
		return nil, "", false
	}

	data, err := source.ReadSessionFile(ctx, sessionID, entry.Path)
	if err != nil {
		logger.Warnf(ctx, "[ArtifactCollector] read artifact failed: session=%s path=%s err=%v",
			sessionID, entry.Path, err)
		return nil, "", false
	}
	hash := artifactContentHash(data)
	if c.knownSameContent(ctx, known, entry.Path, hash) {
		known.remember(types.MessageArtifact{
			SourcePath: entry.Path, ModTime: entry.ModTime, ContentHash: hash, FileSize: int64(len(data)),
		})
		if c.store != nil {
			if err := c.store.RecordRestoredMtime(ctx, sessionID, entry.Path, entry.ModTime, hash); err != nil {
				logger.Warnf(ctx, "[ArtifactCollector] persist restored mtime failed: session=%s path=%s err=%v",
					sessionID, entry.Path, err)
			}
		}
		return nil, "", false
	}
	if int64(len(data)) > c.config.MaxFileBytes {
		logger.Warnf(ctx, "[ArtifactCollector] skip oversize artifact after read: session=%s path=%s size=%d limit=%d",
			sessionID, entry.Path, len(data), c.config.MaxFileBytes)
		return nil, "", false
	}
	return data, hash, true
}

func (c *ArtifactCollector) persistBytes(
	ctx context.Context,
	sessionID string,
	messageID string,
	tenantID uint64,
	entry sandbox.RemoteDirEntry,
	data []byte,
	hash string,
) (types.MessageArtifact, bool) {
	storageName := "artifact_" + uuid.NewString() + "_" + safeFileName(entry.Name)
	storagePath, err := c.fileService.SaveBytes(ctx, data, tenantID, storageName, false)
	if err != nil {
		logger.Warnf(ctx, "[ArtifactCollector] upload artifact failed: session=%s path=%s err=%v",
			sessionID, entry.Path, err)
		return types.MessageArtifact{}, false
	}

	c.bindArtifactResource(ctx, storagePath, messageID)

	return types.MessageArtifact{
		URL:         storagePath,
		FileName:    entry.Name,
		FileType:    strings.ToLower(filepath.Ext(entry.Name)),
		FileSize:    int64(len(data)),
		ContentHash: hash,
		SourcePath:  entry.Path,
		ModTime:     entry.ModTime,
		CreatedAt:   time.Now().UTC(),
	}, true
}

func (c *ArtifactCollector) knownSameContent(ctx context.Context, known *artifactKnownSet, path, hash string) bool {
	if known == nil || hash == "" {
		return false
	}
	if known.seenHash(path, hash) {
		return true
	}
	for _, art := range known.byPath[path] {
		if art.ContentHash == hash {
			return true
		}
		if art.ContentHash != "" {
			continue
		}
		stored := c.hashStoredArtifact(ctx, art)
		if stored != "" {
			known.keys[artifactHashKey(path, stored)] = struct{}{}
			if stored == hash {
				return true
			}
		}
	}
	return false
}

func (c *ArtifactCollector) hashStoredArtifact(ctx context.Context, art types.MessageArtifact) string {
	if c == nil || c.fileService == nil || strings.TrimSpace(art.URL) == "" {
		return ""
	}
	file, err := c.fileService.GetFile(ctx, art.URL)
	if err != nil || file == nil {
		return ""
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return ""
	}
	return artifactContentHash(data)
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

// SessionArtifacts returns every artifact already recorded against the session.
// Empty on error, so the caller degrades to "nothing to resolve" rather than
// dropping the turn.
//
// Callers need this to resolve an answer's file references: a turn can name a
// file it did not (re)generate — the first turn after a session fork points the
// sandbox back at an earlier commit, so every unchanged file is de-duplicated
// out of that turn's own artifact list — and the reference still has to be bound
// to that file's stable handle.
//
// Files the user deleted are filtered out: they are still in the store as
// tombstones so loadKnownSet will not re-collect them, but an answer must not
// resolve a name to a file whose bytes are gone.
func (c *ArtifactCollector) SessionArtifacts(ctx context.Context, sessionID string) types.MessageArtifacts {
	if c == nil || c.store == nil || sessionID == "" {
		return nil
	}
	previous, err := c.store.KnownArtifacts(ctx, sessionID)
	if err != nil {
		logger.Warnf(ctx, "Read session artifacts failed: %v", err)
		return nil
	}
	return types.MessageArtifacts(previous).Live()
}

// BindArtifactsToMessage makes messageID an owner of each artifact's resource
// handle as well. Deleting the message that originally produced the file then
// cannot invalidate a reference made from a later one. Best-effort: a binding
// failure never discards the artifact, which is already stored and downloadable
// through the /artifacts endpoint.
func (c *ArtifactCollector) BindArtifactsToMessage(
	ctx context.Context, messageID string, artifacts types.MessageArtifacts,
) {
	if c == nil || c.catalog == nil || messageID == "" {
		return
	}
	for _, artifact := range artifacts {
		c.bindArtifactResource(ctx, artifact.URL, messageID)
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

func artifactHashKey(path, hash string) string {
	return path + "\x00h" + hash
}

func artifactContentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// artifactKnownSet is the per-collect de-dupe index. path+mtime is the cheap
// skip for an untouched file; path+hash catches a restore that only changed
// mtime. Size is never an identity key.
type artifactKnownSet struct {
	keys   map[string]struct{}
	byPath map[string][]types.MessageArtifact
}

func newArtifactKnownSet() *artifactKnownSet {
	return &artifactKnownSet{
		keys:   map[string]struct{}{},
		byPath: map[string][]types.MessageArtifact{},
	}
}

func (k *artifactKnownSet) len() int {
	if k == nil {
		return 0
	}
	return len(k.keys)
}

func (k *artifactKnownSet) remember(art types.MessageArtifact) {
	if k == nil {
		return
	}
	if k.keys == nil {
		k.keys = map[string]struct{}{}
	}
	if k.byPath == nil {
		k.byPath = map[string][]types.MessageArtifact{}
	}
	k.keys[artifactKey(art.SourcePath, art.ModTime)] = struct{}{}
	if art.ContentHash != "" {
		k.keys[artifactHashKey(art.SourcePath, art.ContentHash)] = struct{}{}
	}
	if art.SourcePath != "" {
		k.byPath[art.SourcePath] = append(k.byPath[art.SourcePath], art)
	}
}

// rememberDeleted registers a tombstone: a file the user deleted, whose sandbox
// copy may still be sitting there untouched.
//
// Only the (path, mtime) key goes in. That is what stops an unchanged sandbox
// file being re-collected, which is the whole reason tombstones stay in the
// known set. The content hash is deliberately left out, and so is byPath: those
// two drive the same-content short circuit in knownSameContent, and a turn that
// rewrites the file with identical bytes is a genuine regeneration the user
// should get back — not a restore to skip. Keeping the hash here would make a
// deleted file impossible to reproduce byte-for-byte ever again.
func (k *artifactKnownSet) rememberDeleted(art types.MessageArtifact) {
	if k == nil {
		return
	}
	if k.keys == nil {
		k.keys = map[string]struct{}{}
	}
	k.keys[artifactKey(art.SourcePath, art.ModTime)] = struct{}{}
}

func (k *artifactKnownSet) seenMtime(path string, mod time.Time) bool {
	if k == nil {
		return false
	}
	_, ok := k.keys[artifactKey(path, mod)]
	return ok
}

func (k *artifactKnownSet) seenHash(path, hash string) bool {
	if k == nil || hash == "" {
		return false
	}
	_, ok := k.keys[artifactHashKey(path, hash)]
	return ok
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
