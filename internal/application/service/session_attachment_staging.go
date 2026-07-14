package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

type sessionInputFileStore interface {
	ListSessionFiles(ctx context.Context, sessionID, dir string) ([]sandbox.DirEntry, error)
	WriteSessionInputFile(ctx context.Context, sessionID, filePath string, content []byte) error
	RemoveSessionInputPath(ctx context.Context, sessionID, targetPath string) error
}

type stagedSessionAttachment struct {
	Name     string
	FileType string
	Size     int64
	Path     string
}

// stageSessionAttachments reconciles /workspace/input with the durable
// attachment inventory. It is Cube-only; other backends retain prompt-extracted
// attachment content and never receive host file paths.
func (s *agentService) stageSessionAttachments(
	ctx context.Context,
	sessionID string,
	attachments types.MessageAttachments,
) ([]stagedSessionAttachment, error) {
	if s == nil || s.sandboxMgr == nil || s.sandboxMgr.GetType() != sandbox.SandboxTypeCube {
		return nil, nil
	}
	store, ok := s.sandboxMgr.(sessionInputFileStore)
	if !ok {
		return nil, fmt.Errorf("cube sandbox does not support session input staging")
	}
	if s.fileService == nil && len(attachments) > 0 {
		return nil, fmt.Errorf("file service is unavailable for session input staging")
	}

	attachments = deduplicateSessionAttachments(attachments)
	existingEntries, err := store.ListSessionFiles(ctx, sessionID, sandbox.SessionInputRoot)
	if err != nil {
		return nil, fmt.Errorf("list staged session inputs: %w", err)
	}
	existing := make(map[string]sandbox.DirEntry, len(existingEntries))
	for _, entry := range existingEntries {
		existing[path.Clean(entry.Path)] = entry
	}

	desired := make(map[string]struct{}, len(attachments))
	staged := make([]stagedSessionAttachment, 0, len(attachments))
	maxBytes := int64(secutils.GetMaxFileSizeMB()) * 1024 * 1024
	for _, attachment := range attachments {
		remotePath, pathErr := sandboxAttachmentPath(attachment)
		if pathErr != nil {
			return nil, pathErr
		}
		desired[remotePath] = struct{}{}

		entry, present := existing[remotePath]
		if !present || attachment.FileSize <= 0 || entry.Size != attachment.FileSize {
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
			if writeErr := store.WriteSessionInputFile(ctx, sessionID, remotePath, content); writeErr != nil {
				return nil, fmt.Errorf("stage attachment %q: %w", attachment.FileName, writeErr)
			}
			attachment.FileSize = int64(len(content))
		}

		staged = append(staged, stagedSessionAttachment{
			Name:     attachment.FileName,
			FileType: attachment.FileType,
			Size:     attachment.FileSize,
			Path:     remotePath,
		})
	}

	// Remove inputs whose durable message attachment no longer exists.
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

func sandboxAttachmentPath(attachment types.MessageAttachment) (string, error) {
	url := strings.TrimSpace(attachment.URL)
	if url == "" {
		return "", fmt.Errorf("attachment %q has no durable storage URL", attachment.FileName)
	}
	fileName, err := secutils.SafeFileName(attachment.FileName)
	if err != nil {
		return "", fmt.Errorf("unsafe attachment filename %q: %w", attachment.FileName, err)
	}
	sum := sha256.Sum256([]byte(url))
	return path.Join(sandbox.SessionInputRoot, fmt.Sprintf("%x", sum[:6]), fileName), nil
}

func buildSandboxAttachmentsPrompt(attachments []stagedSessionAttachment) string {
	if len(attachments) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n<sandbox_attachments root=\"/workspace/input\">\n")
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
	b.WriteString("  <instruction>Use these absolute paths as read-only inputs for shell commands or skill script arguments. Write generated files only under $WEKNORA_SKILL_OUTPUT_DIR.</instruction>\n")
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
	return replacer.Replace(value)
}
