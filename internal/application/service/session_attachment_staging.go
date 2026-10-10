package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

type stagedSessionAttachment struct {
	Name     string
	FileType string
	Size     int64
	Path     string
}

// sessionSandboxFileStore returns the manager's effective session filesystem
// capability, or nil when the backend cannot support it. Centralised so
// callers never resurrect a Cube-specific type check.
func sessionSandboxFileStore(mgr sandbox.Manager) sandbox.SessionFileStore {
	provider, ok := mgr.(sandbox.SessionCapabilityProvider)
	if !ok || provider == nil {
		return nil
	}
	return provider.SessionFileStore()
}

// sessionSandboxShellExecutor is the shell-execution counterpart of
// sessionSandboxFileStore.
func sessionSandboxShellExecutor(mgr sandbox.Manager) sandbox.SessionShellExecutor {
	provider, ok := mgr.(sandbox.SessionCapabilityProvider)
	if !ok || provider == nil {
		return nil
	}
	return provider.SessionShellExecutor()
}

// sessionSandboxInstallShellExecutor is the install-mode counterpart of
// sessionSandboxShellExecutor. It is a separate accessor on a separate
// interface so a caller cannot obtain the privileged executor by accident.
func sessionSandboxInstallShellExecutor(mgr sandbox.Manager) sandbox.SessionInstallShellExecutor {
	provider, ok := mgr.(sandbox.SessionInstallCapabilityProvider)
	if !ok || provider == nil {
		return nil
	}
	return provider.SessionInstallShellExecutor()
}

// sessionAttachmentStager is the agentService surface the QA pipeline needs to
// stage attachments. Declared as a named interface so the runtime type
// assertion in session_agent_qa.go has one definition to drift against.
type sessionAttachmentStager interface {
	sessionSandboxInputStore(
		ctx context.Context, sessionID, agentSandboxConfigID string,
	) (sandbox.SessionFileStore, error)
	stageSessionAttachments(
		ctx context.Context, sessionID, agentSandboxConfigID string,
		tenantID uint64,
		attachments types.MessageAttachments,
		layout sandbox.WorkspaceLayout,
	) ([]stagedSessionAttachment, error)
}

// sessionSandboxInputStore resolves the session filesystem capability of the
// backend this session's sandbox actually runs on.
//
// Callers must gate staging on this rather than on the process-wide manager: a
// different workspace configs expose different capabilities. Remote backends
// need attachment staging; backends that do not advertise a session
// filesystem skip staging entirely.
func (s *agentService) sessionSandboxInputStore(
	ctx context.Context,
	sessionID string,
	agentSandboxConfigID string,
) (sandbox.SessionFileStore, error) {
	if s == nil {
		return nil, nil
	}
	tenantID, _ := types.TenantIDFromContext(ctx)
	mgr, _, err := resolveSandboxForExecution(
		ctx, s.sandboxResolver, s.sandboxMgr, s.sandboxPinner,
		tenantID, sessionID, agentSandboxConfigID, s.sandboxPolicy,
		withLiteHostSandbox(s.hostSandbox), withLiteDesktop(s.hostDesktop),
	)
	if err != nil {
		return nil, fmt.Errorf("resolve sandbox config for session %s: %w", sessionID, err)
	}
	return sessionSandboxFileStore(mgr), nil
}

// stageSessionAttachments reconciles the layout's InputDir with the durable
// attachment inventory. Remote backends pass RemoteWorkspaceLayout() so
// InputDir is still /workspace/input. Host layouts leave InputDir empty:
// staging is skipped entirely — never list /workspace/input, and never copy
// chat attachments into the user's directory.
func (s *agentService) stageSessionAttachments(
	ctx context.Context,
	sessionID string,
	agentSandboxConfigID string,
	tenantID uint64,
	attachments types.MessageAttachments,
	layout sandbox.WorkspaceLayout,
) ([]stagedSessionAttachment, error) {
	if s == nil {
		return nil, nil
	}
	if strings.TrimSpace(layout.InputDir) == "" {
		return nil, nil
	}
	store, err := s.sessionSandboxInputStore(ctx, sessionID, agentSandboxConfigID)
	if err != nil {
		return nil, err
	}
	if store == nil {
		return nil, nil
	}
	if s.fileService == nil && len(attachments) > 0 {
		return nil, fmt.Errorf("file service is unavailable for session input staging")
	}

	resolved, err := s.resolveSessionAttachmentURLs(ctx, tenantID, sessionID, attachments)
	if err != nil {
		return nil, err
	}
	attachments = deduplicateSessionAttachments(resolved)
	existingEntries, err := store.ListSessionFiles(ctx, sessionID, layout.InputDir)
	if err != nil {
		return nil, fmt.Errorf("list staged session inputs: %w", err)
	}
	existing := make(map[string]sandbox.RemoteDirEntry, len(existingEntries))
	for _, entry := range existingEntries {
		existing[path.Clean(entry.Path)] = entry
	}

	desired := make(map[string]struct{}, len(attachments))
	staged := make([]stagedSessionAttachment, 0, len(attachments))
	inputs := make([]stagedInput, 0, len(attachments))
	maxBytes := int64(secutils.GetMaxFileSizeMB()) * 1024 * 1024
	for _, attachment := range attachments {
		remotePath, pathErr := sandboxAttachmentPath(attachment, layout.InputDir)
		if pathErr != nil {
			return nil, pathErr
		}
		desired[remotePath] = struct{}{}

		entry, present := existing[remotePath]
		if !present || attachment.FileSize <= 0 || entry.Size != attachment.FileSize {
			content, readErr := s.readAttachmentContent(ctx, attachment, maxBytes)
			if readErr != nil {
				return nil, readErr
			}
			rebound, writeErr := writeSessionInputWithRetry(ctx, store, sessionID, remotePath, content)
			if writeErr != nil {
				return nil, fmt.Errorf("stage attachment %q: %w", attachment.FileName, writeErr)
			}
			attachment.FileSize = int64(len(content))
			inputs = append(inputs, stagedInput{
				remotePath: remotePath,
				name:       attachment.FileName,
				url:        attachment.URL,
				content:    content,
				size:       attachment.FileSize,
			})
			if rebound {
				// The retry re-resolved the session, which may have
				// rebound it onto a fresh container: everything staged
				// before this attachment lived in the old one.
				if restoreErr := s.reestablishStagedInputs(ctx, store, sessionID, layout.InputDir, inputs, maxBytes); restoreErr != nil {
					return nil, restoreErr
				}
			}
		} else {
			// Reused from the previous pass: no content kept in memory,
			// but the rebinding recovery must still be able to
			// re-create it from durable storage.
			inputs = append(inputs, stagedInput{
				remotePath: remotePath,
				name:       attachment.FileName,
				url:        attachment.URL,
				size:       attachment.FileSize,
			})
		}

		staged = append(staged, stagedSessionAttachment{
			Name:     attachment.FileName,
			FileType: attachment.FileType,
			Size:     attachment.FileSize,
			Path:     remotePath,
		})
	}

	// Remove inputs whose durable message attachment no longer exists.
	//
	// This delete pass is only safe because InputDir is a session-private
	// application-data directory, never the user's project tree. If InputDir
	// were ever pointed at the workspace, this loop would delete the user's
	// files.
	for filePath := range existing {
		if _, keep := desired[filePath]; !keep {
			if err := store.RemoveSessionInputPath(ctx, sessionID, filePath); err != nil {
				return nil, fmt.Errorf("remove stale session input %s: %w", filePath, err)
			}
		}
	}

	sort.SliceStable(staged, func(i, j int) bool { return staged[i].Path < staged[j].Path })
	return staged, nil
}

// sandboxStagingRetryDelay separates the two attempts a transient sandbox
// filesystem stall gets before staging fails the turn.
const sandboxStagingRetryDelay = 2 * time.Second

// sandboxStagingRebindPasses bounds how many times the rebinding recovery
// below re-verifies the input set after a retry landed on yet another fresh
// container. A session whose inputs keep getting killed this many times is
// not going to heal; the error says so instead of looping forever.
const sandboxStagingRebindPasses = 3

// stagedInput tracks one attachment staged during this pass so the rebinding
// recovery can re-establish what an earlier container already held. content
// is nil for inputs reused from the previous pass; those re-read from
// durable storage when the recovery needs to rewrite them.
type stagedInput struct {
	remotePath string
	name       string
	url        string
	content    []byte
	size       int64
}

// readAttachmentContent reads one attachment from durable storage, bounded by
// the sandbox staging limit.
func (s *agentService) readAttachmentContent(
	ctx context.Context,
	attachment types.MessageAttachment,
	maxBytes int64,
) ([]byte, error) {
	reader, getErr := s.fileService.GetFile(ctx, attachment.URL)
	if getErr != nil {
		return nil, fmt.Errorf("open attachment %q: %w", attachment.FileName, getErr)
	}
	content, readErr := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read attachment %q: %w", attachment.FileName, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close attachment %q: %w", attachment.FileName, closeErr)
	}
	if int64(len(content)) > maxBytes {
		return nil, fmt.Errorf("attachment %q exceeds sandbox staging limit of %d bytes", attachment.FileName, maxBytes)
	}
	return content, nil
}

// reestablishStagedInputs re-writes staged inputs that went missing after a
// timeout retry rebound the session onto a fresh container: the retry's
// WriteSessionInputFile re-resolves the session, and the new container's
// input directory does not carry anything the old one held. Without this
// pass, staging would report success while the sandbox only has the one
// attachment whose retry happened to land.
//
// Each pass re-lists the input directory and rewrites whatever is missing or
// has the wrong size. A rewrite that itself only succeeds after a timeout
// retry may have rebound onto yet another container, so the pass repeats
// until a full pass lands without a rebind (or the pass budget runs out).
func (s *agentService) reestablishStagedInputs(
	ctx context.Context,
	store sandbox.SessionFileStore,
	sessionID, inputDir string,
	inputs []stagedInput,
	maxBytes int64,
) error {
	for pass := 0; pass < sandboxStagingRebindPasses; pass++ {
		entries, listErr := store.ListSessionFiles(ctx, sessionID, inputDir)
		if listErr != nil {
			return fmt.Errorf("relist staged session inputs after filesystem-op timeout: %w", listErr)
		}
		present := make(map[string]int64, len(entries))
		for _, entry := range entries {
			present[path.Clean(entry.Path)] = entry.Size
		}
		reboundAgain := false
		for _, input := range inputs {
			if size, ok := present[input.remotePath]; ok && size == input.size {
				continue
			}
			content := input.content
			if content == nil {
				var readErr error
				content, readErr = s.readAttachmentContent(ctx, types.MessageAttachment{
					URL:      input.url,
					FileName: input.name,
				}, maxBytes)
				if readErr != nil {
					return fmt.Errorf("re-stage attachment %q: %w", input.name, readErr)
				}
				if int64(len(content)) != input.size {
					return fmt.Errorf(
						"re-stage attachment %q: durable content is %d bytes, expected %d",
						input.name, len(content), input.size)
				}
			}
			rebound, writeErr := writeSessionInputWithRetry(ctx, store, sessionID, input.remotePath, content)
			if writeErr != nil {
				return fmt.Errorf("re-stage attachment %q after filesystem-op timeout: %w", input.name, writeErr)
			}
			reboundAgain = reboundAgain || rebound
		}
		if !reboundAgain {
			return nil
		}
	}
	return fmt.Errorf(
		"session inputs kept getting killed past the filesystem-op timeout across %d rebinding passes",
		sandboxStagingRebindPasses)
}

// writeSessionInputWithRetry retries once when the sandbox killed the write
// past its filesystem-op timeout (issue #3910): a killed op means the
// container filesystem stalled — the request itself was valid, and such
// stalls are usually transient. Any other failure surfaces immediately. When
// the retry also times out, both failures stay attached to the returned error
// so the log shows it was not a one-off.
//
// The returned rebound flag reports that the write only succeeded on the
// retry: WriteSessionInputFile re-resolves the session, so the second attempt
// may have landed in a fresh container. Callers staging more than one input
// must treat a rebound as "everything staged before this write may be gone"
// and re-establish it.
func writeSessionInputWithRetry(
	ctx context.Context,
	store sandbox.SessionFileStore,
	sessionID, remotePath string,
	content []byte,
) (bool, error) {
	err := store.WriteSessionInputFile(ctx, sessionID, remotePath, content)
	if !sandbox.IsRemoteTimeout(err) {
		return false, err
	}
	select {
	case <-ctx.Done():
		return false, err
	case <-time.After(sandboxStagingRetryDelay):
	}
	if retryErr := store.WriteSessionInputFile(ctx, sessionID, remotePath, content); retryErr != nil {
		return false, fmt.Errorf("%w (retry after filesystem-op timeout failed: %v)", err, retryErr)
	}
	return true, nil
}

// resolveSessionAttachmentURLs fills in the storage handle for attachments
// whose URL was not persisted on the message row. MessageAttachment.URL is
// deliberately excluded from DB serialization (json:"-") so a cross-session
// downloadable reference cannot leak; the temporary-document row keyed by
// attachment.ID is the authoritative source of the storage reference.
// Lookup is tenant+id (not session-scoped) so a forked session can still
// stage attachments whose temporary_documents row remains on the parent.
// Attachments that already carry a URL, or that have no temporary-document ID,
// pass through unchanged so callers with other attachment sources keep working.
func (s *agentService) resolveSessionAttachmentURLs(
	ctx context.Context,
	tenantID uint64,
	_ string,
	attachments types.MessageAttachments,
) (types.MessageAttachments, error) {
	if len(attachments) == 0 || s == nil || s.db == nil {
		return attachments, nil
	}
	repo := repository.NewTemporaryDocumentRepository(s.db)
	out := make(types.MessageAttachments, 0, len(attachments))
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.URL) != "" || strings.TrimSpace(attachment.ID) == "" {
			out = append(out, attachment)
			continue
		}
		document, err := repo.GetByID(ctx, tenantID, attachment.ID)
		if err != nil {
			return nil, fmt.Errorf("resolve attachment %q storage reference: %w", attachment.FileName, err)
		}
		if document == nil || strings.TrimSpace(document.ResourceRef) == "" {
			// Expired or already-deleted temporary document: leave the URL
			// empty so deduplicateSessionAttachments skips it instead of
			// failing the whole staging pass.
			continue
		}
		attachment.URL = document.ResourceRef
		out = append(out, attachment)
	}
	return out, nil
}

func deduplicateSessionAttachments(attachments types.MessageAttachments) types.MessageAttachments {
	seen := make(map[string]struct{}, len(attachments))
	out := make(types.MessageAttachments, 0, len(attachments))
	for _, attachment := range attachments {
		url := strings.TrimSpace(attachment.URL)
		if url == "" {
			continue
		}
		if _, exists := seen[url]; exists {
			continue
		}
		seen[url] = struct{}{}
		out = append(out, attachment)
	}
	return out
}

func sandboxAttachmentPath(attachment types.MessageAttachment, inputDir string) (string, error) {
	url := strings.TrimSpace(attachment.URL)
	if url == "" {
		return "", fmt.Errorf("attachment %q has no durable storage URL", attachment.FileName)
	}
	fileName, err := secutils.SafeFileName(attachment.FileName)
	if err != nil {
		return "", fmt.Errorf("unsafe attachment filename %q: %w", attachment.FileName, err)
	}
	sum := sha256.Sum256([]byte(url))
	return path.Join(inputDir, fmt.Sprintf("%x", sum[:6]), fileName), nil
}

func buildSandboxAttachmentsPrompt(attachments []stagedSessionAttachment, layout sandbox.WorkspaceLayout) string {
	if len(attachments) == 0 {
		return ""
	}
	inputDir := strings.TrimSpace(layout.InputDir)
	if inputDir == "" {
		return ""
	}
	outputDir := strings.TrimSpace(layout.OutputDir)
	workspace := strings.TrimSpace(layout.Root)
	var b strings.Builder
	fmt.Fprintf(&b, "\n\n<sandbox_attachments root=\"%s\">\n", escapeAttachmentXML(inputDir))
	for _, attachment := range attachments {
		fmt.Fprintf(
			&b,
			"  <file name=\"%s\" type=\"%s\" size_bytes=\"%d\" path=\"%s\" />\n",
			escapeAttachmentXML(attachment.Name),
			escapeAttachmentXML(attachment.FileType),
			attachment.Size,
			escapeAttachmentXML(attachment.Path),
		)
	}
	// The instruction body carries the same host-chosen paths as the
	// attributes above, so it gets the same escaping: on a host layout these
	// are directory names the user picked, and unescaped markup in one would
	// close the element and read as instructions.
	b.WriteString("  <instruction>These are the user's files: read them at the absolute paths above " +
		"and do not write into " + escapeAttachmentXML(inputDir) + ". Inspect them with read_file, " +
		"or with shell_exec (ls/find) when a shell is available. " +
		"Create generated files with write_sandbox_file " +
		"and patch existing ones with edit_sandbox_file.")
	if layout.IsHost() {
		if workspace != "" {
			b.WriteString(" Edit files in place under " + escapeAttachmentXML(workspace) + ".")
		}
	} else {
		remote := sandbox.RemoteWorkspaceLayout()
		if outputDir == "" {
			outputDir = remote.OutputDir
		}
		if workspace == "" {
			workspace = remote.Root
		}
		b.WriteString(" $WEKNORA_SKILL_OUTPUT_DIR (" + escapeAttachmentXML(outputDir) + ") " +
			"is the only directory collected for download, so put finished deliverables there " +
			"and keep drafts and intermediate files in any other directory under " +
			escapeAttachmentXML(workspace) + ".")
	}
	b.WriteString("</instruction>\n")
	b.WriteString("</sandbox_attachments>")
	return b.String()
}

func escapeAttachmentXML(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(stripControlRunes(value))
}

// stripControlRunes drops the characters entity escaping does not neutralize.
// One element per line is what makes this block readable to the model, and a
// newline inside a filename or a host directory name would forge a second one.
func stripControlRunes(value string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
}
