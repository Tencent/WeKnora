// Package service provides browser-safe access to a session sandbox's live output files.
package service

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// SandboxLiveFilesService resolves the sandbox configuration pinned to a
// session and delegates to its provider-neutral live-file capability. It is
// lookup-only: opening the Files tab never provisions or resumes a sandbox.
type SandboxLiveFilesService struct {
	pinner   *SessionSandboxPinner
	resolver sandbox.TenantSandboxResolver
	fallback sandbox.Manager
	policy   WorkspaceSandboxPolicy
}

// NewSandboxLiveFilesService wires lookup-only access to pinned session sandboxes.
func NewSandboxLiveFilesService(
	pinner *SessionSandboxPinner,
	resolver sandbox.TenantSandboxResolver,
	fallback sandbox.Manager,
	policy WorkspaceSandboxPolicy,
) *SandboxLiveFilesService {
	return &SandboxLiveFilesService{
		pinner: pinner, resolver: resolver, fallback: fallback, policy: policy,
	}
}

// List returns one directory level below the fixed output root.
func (s *SandboxLiveFilesService) List(
	ctx context.Context, sessionID, relativeDir string,
) ([]sandbox.SessionLiveFileEntry, error) {
	manager, err := s.manager(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return manager.ListSessionLiveFiles(ctx, sessionID, relativeDir)
}

// Read downloads one regular file below the fixed output root.
func (s *SandboxLiveFilesService) Read(
	ctx context.Context, sessionID, relativePath string,
) ([]byte, error) {
	manager, err := s.manager(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return manager.ReadSessionLiveFile(ctx, sessionID, relativePath)
}

// Write uploads one new regular file without overwriting an existing entry.
func (s *SandboxLiveFilesService) Write(
	ctx context.Context, sessionID, relativePath string, content []byte,
) error {
	manager, err := s.manager(ctx, sessionID)
	if err != nil {
		return err
	}
	return manager.WriteSessionLiveFile(ctx, sessionID, relativePath, content)
}

// Rename atomically renames one safe entry without overwriting its target.
func (s *SandboxLiveFilesService) Rename(
	ctx context.Context, sessionID, source, target string,
) error {
	manager, err := s.manager(ctx, sessionID)
	if err != nil {
		return err
	}
	return manager.RenameSessionLiveFile(ctx, sessionID, source, target)
}

// Remove deletes one safe file or directory tree.
func (s *SandboxLiveFilesService) Remove(
	ctx context.Context, sessionID, relativePath string,
) error {
	manager, err := s.manager(ctx, sessionID)
	if err != nil {
		return err
	}
	return manager.RemoveSessionLiveFile(ctx, sessionID, relativePath)
}

func (s *SandboxLiveFilesService) manager(
	ctx context.Context, sessionID string,
) (sandbox.SessionLiveFileManager, error) {
	if s == nil || s.pinner == nil {
		return nil, sandbox.ErrNoLiveSessionSandbox
	}
	configID, err := s.pinner.Read(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if configID == "" {
		return nil, sandbox.ErrNoLiveSessionSandbox
	}
	tenantID, _ := types.TenantIDFromContext(ctx)
	mgr, err := resolveTenantSandboxForConfig(
		ctx, s.resolver, s.fallback, tenantID, configID, s.policy,
	)
	if err != nil {
		return nil, err
	}
	provider, ok := mgr.(sandbox.SessionLiveFileProvider)
	if !ok {
		return nil, ErrLiveFilesUnsupported
	}
	files := provider.SessionLiveFileManager()
	if files == nil {
		return nil, ErrLiveFilesUnsupported
	}
	return files, nil
}

// ErrLiveFilesUnsupported means the resolved backend cannot offer safe live-file access.
var ErrLiveFilesUnsupported = errors.New("sandbox live files are unsupported")
