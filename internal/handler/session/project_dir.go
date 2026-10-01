package session

import (
	"path/filepath"
	"strings"
)

// HostProjectDirsLoader returns the user-approved project directories from
// desktop prefs. Nil means no directory is approved.
type HostProjectDirsLoader func() []string

func (h *Handler) approvedDirs() []string {
	if h == nil || h.approvedProjectDirs == nil {
		return nil
	}
	return h.approvedProjectDirs()
}

// MatchApprovedDir reports whether raw names a directory the user approved
// through the native picker, returning it cleaned.
//
// This is the authorization boundary for host workspaces, so it lives here
// rather than in localsandbox: session create runs in every edition, and the
// desktop-only sandbox must not be imported by cmd/server.
//
// Matching is exact after Clean. A subdirectory of an approved directory is
// not approved: the user picked a root, and honouring children would let a
// stored value reach deeper than the picker ever offered.
func MatchApprovedDir(raw string, approved []string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !filepath.IsAbs(raw) {
		return "", false
	}
	cleaned := filepath.Clean(raw)
	for _, dir := range approved {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if filepath.Clean(dir) == cleaned {
			return cleaned, true
		}
	}
	return "", false
}

// bindHostWorkspaceDir accepts an omitted/empty project_dir as unbound. A
// non-empty value must be an absolute path already on the approved list
// (compared after Clean). The stored value is the cleaned path.
func bindHostWorkspaceDir(raw string, approved []string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true
	}
	return MatchApprovedDir(raw, approved)
}
