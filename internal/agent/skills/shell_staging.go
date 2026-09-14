package skills

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/sandbox"
)

const (
	maxStagedSkillFiles = 1000
	maxStagedSkillBytes = 32 * 1024 * 1024
)

// stageShellSkill copies an explicitly configured host skill's resources into
// this session. It never executes on the host or installs dependencies. Files
// are addressed by content revision and prepared once per manager/session;
// errors leave no ready cache entry, so a later corrected call can retry.
func (m *Manager) stageShellSkill(ctx context.Context, sessionID, name string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("a session is required to prepare skill %q", name)
	}
	if _, err := sandbox.SkillDirFor(name); err != nil {
		return "", err
	}
	listed := false
	for _, meta := range m.GetAllMetadata() {
		if meta != nil && meta.Name == name {
			listed = true
			break
		}
	}
	if !listed {
		return "", fmt.Errorf("skill %q is not available to this agent", name)
	}
	store := sessionFileStoreFromManager(m.sandboxMgr)
	if store == nil {
		return "", fmt.Errorf("preparing host skill %q requires a session filesystem", name)
	}
	m.stageMu.Lock()
	defer m.stageMu.Unlock()
	key := sessionID + "\x00" + name
	if dir := m.stagedSkills[key]; dir != "" {
		intact, err := m.stagedSkillStillIntact(ctx, store, sessionID, name, dir)
		if err != nil {
			return "", err
		}
		if intact {
			return dir, nil
		}
		delete(m.stagedSkills, key)
	}
	source := m.resolveSource(name)
	files, err := source.ListSkillFiles(name)
	if err != nil {
		return "", fmt.Errorf("list skill %q for execution: %w", name, err)
	}
	sort.Strings(files)
	if len(files) > maxStagedSkillFiles {
		return "", fmt.Errorf("skill %q exceeds the %d-file staging limit; install it into the sandbox image", name, maxStagedSkillFiles)
	}
	type resource struct {
		name string
		data []byte
	}
	var resources []resource
	total := 0
	digest := sha256.New()
	for _, rel := range files {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		rel = strings.ReplaceAll(rel, "\\", "/")
		if rel == "" || path.IsAbs(rel) || path.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, "../") {
			return "", fmt.Errorf("invalid skill resource path %q", rel)
		}
		if skipStagedSkillRel(rel) {
			continue
		}
		file, err := source.LoadSkillFile(name, rel)
		if err != nil {
			return "", fmt.Errorf("read skill resource %s: %w", rel, err)
		}
		total += len(file.Content)
		if total > maxStagedSkillBytes {
			return "", fmt.Errorf("skill %q exceeds the %d-byte staging limit; install it into the sandbox image", name, maxStagedSkillBytes)
		}
		fmt.Fprintf(digest, "%d:%s:%d:", len(rel), rel, len(file.Content))
		digest.Write([]byte(file.Content))
		resources = append(resources, resource{rel, []byte(file.Content)})
	}
	if len(resources) == 0 {
		return "", fmt.Errorf("skill %q has no resources to stage", name)
	}
	dir := path.Join(sandbox.SessionWorkspaceRoot, ".skills", name, fmt.Sprintf("%x", digest.Sum(nil)[:12]))
	payload := make([]sandbox.SessionWorkspaceFile, 0, len(resources))
	for _, file := range resources {
		payload = append(payload, sandbox.SessionWorkspaceFile{
			Path:    path.Join(dir, file.name),
			Content: file.data,
		})
	}
	if err := store.WriteSessionWorkspaceFiles(ctx, sessionID, payload); err != nil {
		return "", fmt.Errorf("prepare skill %q: %w; command was not started", name, err)
	}
	if m.stagedSkills == nil {
		m.stagedSkills = make(map[string]string)
	}
	m.stagedSkills[key] = dir
	return dir, nil
}

func skipStagedSkillRel(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		switch part {
		case ".venv", "node_modules", ".git", "__pycache__":
			return true
		}
	}
	return false
}

// stagedSkillStillIntact reports whether the cached staging directory still
// matches the host package. A missing or mutated SKILL.md, or a missing
// resource, forces a restage so a model-edited tree is not reused.
func (m *Manager) stagedSkillStillIntact(
	ctx context.Context,
	store sandbox.SessionFileStore,
	sessionID, name, dir string,
) (bool, error) {
	manifest := path.Join(dir, SkillFileName)
	stat, err := store.StatSessionFile(ctx, sessionID, manifest)
	if err != nil && !sandbox.IsRemoteNotFound(err) {
		return false, fmt.Errorf("inspect staged skill %q: %w; command was not started", name, err)
	}
	if stat == nil || sandbox.IsRemoteNotFound(err) {
		return false, nil
	}
	if stat.Type != sandbox.RemoteEntryFile {
		return false, fmt.Errorf("staged skill manifest is not a regular file; existing data was preserved")
	}
	source := m.resolveSource(name)
	expected, err := source.LoadSkillFile(name, SkillFileName)
	if err != nil {
		return false, nil
	}
	actual, err := store.ReadSessionFile(ctx, sessionID, manifest)
	if err != nil && !sandbox.IsRemoteNotFound(err) {
		return false, fmt.Errorf("inspect staged skill %q: %w; command was not started", name, err)
	}
	if err != nil || !bytes.Equal(actual, []byte(expected.Content)) {
		return false, nil
	}
	files, err := source.ListSkillFiles(name)
	if err != nil {
		return false, nil
	}
	for _, rel := range files {
		rel = strings.ReplaceAll(rel, "\\", "/")
		if skipStagedSkillRel(rel) {
			continue
		}
		entry, err := store.StatSessionFile(ctx, sessionID, path.Join(dir, rel))
		if err != nil && !sandbox.IsRemoteNotFound(err) {
			return false, fmt.Errorf("inspect staged skill %q: %w; command was not started", name, err)
		}
		if entry == nil || entry.Type != sandbox.RemoteEntryFile {
			return false, nil
		}
	}
	return true, nil
}
