package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func liteHost(tree HostSkillTree, installer HostSkillInstaller) HostSandboxManager {
	return HostSandboxManager{
		Manager:        &capableManager{typ: sandbox.SandboxTypeHost},
		Desktop:        true,
		SkillTree:      tree,
		SkillInstaller: installer,
	}
}

func requireNotFound(t *testing.T, err error) {
	t.Helper()
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, apperrors.ErrNotFound, appErr.Code)
}

func TestRequireSkillTargetMatrix(t *testing.T) {
	ctx := context.Background()
	configs := &installConfigRepo{entity: &types.TenantSandboxConfigEntity{ID: "cfg-1", TenantID: 7, Name: "cube"}}

	web := &TenantSkillService{configs: configs}
	require.NoError(t, web.requireSkillTarget(ctx, 7, "cfg-1"))
	requireNotFound(t, web.requireSkillTarget(ctx, 7, sandbox.HostSkillTargetID))
	requireNotFound(t, web.requireSkillTarget(ctx, 7, "cfg-missing"))

	lite := &TenantSkillService{configs: configs, host: liteHost(&fakeHostSkillTree{}, &fakeHostSkillInstaller{})}
	require.NoError(t, lite.requireSkillTarget(ctx, 7, sandbox.HostSkillTargetID))
	requireNotFound(t, lite.requireSkillTarget(ctx, 7, "cfg-1"))

	liteNoSandbox := &TenantSkillService{configs: configs, host: HostSandboxManager{Desktop: true}}
	requireNotFound(t, liteNoSandbox.requireSkillTarget(ctx, 7, sandbox.HostSkillTargetID))
}

func TestInstallViewNamesTheHostTarget(t *testing.T) {
	v := installView(&types.TenantSkillEntity{ID: "a", SandboxConfigID: sandbox.HostSkillTargetID}, nil)
	require.Equal(t, hostSkillTargetName, v.SandboxConfigName)
	require.Equal(t, string(sandbox.SandboxTypeHost), v.SandboxType)
}

type fakeHostSkillTree struct {
	root      string
	versions  []string
	active    map[string]string
	discarded []string
	pruned    map[string][]string
	removed   []string
}

func newFakeHostSkillTree(root string) *fakeHostSkillTree {
	return &fakeHostSkillTree{root: root, active: map[string]string{}, pruned: map[string][]string{}}
}

func (f *fakeHostSkillTree) Root() string         { return f.root }
func (f *fakeHostSkillTree) VersionsRoot() string { return f.root + "/.versions" }
func (f *fakeHostSkillTree) NewVersion(name string) (string, error) {
	dir := fmt.Sprintf("%s/%s-%d", f.VersionsRoot(), name, len(f.versions)+1)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	f.versions = append(f.versions, dir)
	return dir, nil
}

func (f *fakeHostSkillTree) Activate(name, dir string) (string, error) {
	prev := f.active[name]
	f.active[name] = dir
	return prev, nil
}

func (f *fakeHostSkillTree) Discard(dir string) error {
	f.discarded = append(f.discarded, dir)
	return os.RemoveAll(dir)
}

func (f *fakeHostSkillTree) Prune(name string, keep ...string) error {
	f.pruned[name] = keep
	return nil
}

func (f *fakeHostSkillTree) Remove(name string) error {
	f.removed = append(f.removed, name)
	delete(f.active, name)
	return nil
}
func (f *fakeHostSkillTree) Installed(name string) bool  { return f.active[name] != "" }
func (f *fakeHostSkillTree) Sweep() error                { return nil }
func (f *fakeHostSkillTree) Lock(string) (func(), error) { return func() {}, nil }

type fakeHostSkillInstaller struct {
	capableManager
	bound    map[string]string
	commands []string
	fail     func(command string) bool
}

func (f *fakeHostSkillInstaller) Bind(sessionID, dir string) func() {
	if f.bound == nil {
		f.bound = map[string]string{}
	}
	f.bound[sessionID] = dir
	return func() { delete(f.bound, sessionID) }
}

func (f *fakeHostSkillInstaller) SessionInstallShellExecutor() sandbox.SessionInstallShellExecutor {
	return f
}

func (f *fakeHostSkillInstaller) ExecShellCommandWithOptions(
	_ context.Context, _ string, command string, _ sandbox.ShellExecOptions,
) (*sandbox.ExecuteResult, error) {
	f.commands = append(f.commands, command)
	if f.fail != nil && f.fail(command) {
		return &sandbox.ExecuteResult{ExitCode: 1, Stderr: "boom"}, nil
	}
	return &sandbox.ExecuteResult{}, nil
}

func (f *fakeHostSkillInstaller) ReadSessionFile(_ context.Context, sessionID, filePath string) ([]byte, error) {
	if dir := f.bound[sessionID]; dir != "" {
		target := filePath
		if !filepath.IsAbs(filePath) {
			target = filepath.Join(dir, filepath.FromSlash(filePath))
		}
		if raw, err := os.ReadFile(target); err == nil {
			return raw, nil
		}
	}
	// The remote install agent fake seeds this report onto sandboxMgr.files;
	// host verify reads through the installer instead.
	if strings.HasSuffix(filepath.ToSlash(filePath), ".weknora/install-report.json") {
		return []byte(`{"commands":[],"blockers":[]}`), nil
	}
	return nil, os.ErrNotExist
}

func (f *fakeHostSkillInstaller) StatSessionFile(context.Context, string, string) (*sandbox.RemoteStatEntry, error) {
	return nil, os.ErrNotExist
}

func (f *fakeHostSkillInstaller) WriteSessionFile(_ context.Context, sessionID, filePath string, content []byte) error {
	dir := f.bound[sessionID]
	if dir == "" {
		return os.ErrNotExist
	}
	target := filePath
	if !filepath.IsAbs(filePath) {
		target = filepath.Join(dir, filepath.FromSlash(filePath))
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, content, 0o644)
}

func hostInstallFixture(t *testing.T) (*installFixture, *fakeHostSkillTree, *fakeHostSkillInstaller) {
	t.Helper()
	fx := newInstallFixture(t)
	tree := newFakeHostSkillTree(filepath.Join(t.TempDir(), ".weknora", "skills"))
	installer := &fakeHostSkillInstaller{capableManager: capableManager{typ: sandbox.SandboxTypeHost}}
	fx.svc.host = liteHost(tree, installer)
	return fx, tree, installer
}

// installArchive builds the same zip the remote install fixture uses.
// The brief named this fx.archive(); the fixture has no such method.
func (fx *installFixture) installArchive() []byte {
	return zipBundle(fx.t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('hi')\n",
	})
}

// skillRow loads one install row. The brief named this fx.skillRow.
func (fx *installFixture) skillRow(t *testing.T, configID, skillID string) *types.TenantSkillEntity {
	t.Helper()
	row, err := fx.skillRepo.GetSkill(context.Background(), 7, configID, skillID)
	require.NoError(t, err)
	require.NotNil(t, row)
	return row
}

// snapshotLog returns provider snapshot events. The brief named this
// fx.snapshotLog(); the fixture records them on events as "create-snapshot".
func (fx *installFixture) snapshotLog() []string {
	var out []string
	for _, e := range fx.events {
		if e == "create-snapshot" {
			out = append(out, e)
		}
	}
	return out
}

// waitHostInstallDone waits until the background host install leaves installing.
// Adapted from waitBackgroundInstallReady, which also requires a non-empty
// InstalledSnapshotID (remote-only).
func waitHostInstallDone(t *testing.T, fx *installFixture, skillID string) *types.TenantSkillEntity {
	t.Helper()
	var skill *types.TenantSkillEntity
	require.Eventually(t, func() bool {
		got, err := fx.skillRepo.GetSkill(context.Background(), 7, sandbox.HostSkillTargetID, skillID)
		if err != nil || got == nil {
			return false
		}
		skill = got
		return got.Status == types.SkillStatusReady || got.Status == types.SkillStatusFailed
	}, 2*time.Second, 5*time.Millisecond)
	return skill
}

func TestHostInstallActivatesVersionAndMarksReady(t *testing.T) {
	fx, tree, installer := hostInstallFixture(t)
	id, err := fx.svc.InstallSkill(context.Background(), 7, sandbox.HostSkillTargetID, fx.installArchive())
	require.NoError(t, err)
	waitHostInstallDone(t, fx, id)

	row := fx.skillRow(t, sandbox.HostSkillTargetID, id)
	require.Equal(t, types.SkillStatusReady, row.Status)
	require.Empty(t, row.InstalledSnapshotID)
	require.Len(t, tree.versions, 1)
	require.Equal(t, tree.versions[0], tree.active[fx.bundle.Name])
	require.NoFileExists(t, filepath.Join(tree.versions[0], ".weknora", "install-report.json"))
	require.FileExists(t, filepath.Join(tree.versions[0], "SKILL.md"))
	require.Equal(t, []string{tree.versions[0], ""}, tree.pruned[fx.bundle.Name])
	require.Empty(t, installer.bound, "the install session must be unbound when the run ends")
	require.Empty(t, fx.snapshotLog(), "host installs never create provider snapshots")
	require.Empty(t, fx.staleMarks, "host installs never invalidate remote sandboxes")
}

func TestHostInstallFailureKeepsPreviousVersion(t *testing.T) {
	fx, tree, installer := hostInstallFixture(t)
	tree.active[fx.bundle.Name] = tree.VersionsRoot() + "/" + fx.bundle.Name + "-0"
	// No py_compile/compileall in tenant_skill_verify.go; the Python syntax
	// check is skillPythonVerifyCommand, which pipes a base64 script.
	installer.fail = func(command string) bool { return strings.Contains(command, "base64 -d") }

	id, err := fx.svc.InstallSkill(context.Background(), 7, sandbox.HostSkillTargetID, fx.installArchive())
	require.NoError(t, err)
	waitHostInstallDone(t, fx, id)

	row := fx.skillRow(t, sandbox.HostSkillTargetID, id)
	require.Equal(t, types.SkillStatusFailed, row.Status)
	require.Equal(t, tree.VersionsRoot()+"/"+fx.bundle.Name+"-0", tree.active[fx.bundle.Name])
	require.Equal(t, tree.versions, tree.discarded)
}

func TestHostInstallPromptNamesTheVersionDir(t *testing.T) {
	bundle := &SkillBundle{Name: "pdf", Files: map[string][]byte{"SKILL.md": []byte("# pdf")}}
	dir := "/Users/dev/.weknora/skills/.versions/pdf-2"
	prompt := buildHostInstallPrompt(dir, bundle, map[string]string{"python3": "/usr/bin/python3"})
	require.Contains(t, prompt, dir)
	require.Contains(t, prompt, dir+"/.weknora/requirements.json")
	require.Contains(t, prompt, ".weknora/install-report.json")
	require.NotContains(t, prompt, "already present")
	require.Contains(t, prompt, "write_skill_file")
	require.Contains(t, prompt, "sudo")
	require.Contains(t, prompt, "stays open for later chats")
	require.NotContains(t, prompt, "no network")
	require.NotContains(t, prompt, "/workspace")
	require.NotContains(t, prompt, sandbox.SkillsImageRoot)
	require.NotContains(t, prompt, "snapshot")
}

func TestWriteHostSkillFilesRejectsEscapes(t *testing.T) {
	dir := t.TempDir()
	require.Error(t, writeHostSkillFiles(dir, &SkillBundle{Files: map[string][]byte{"../x": []byte("x")}}))
	require.NoError(t, writeHostSkillFiles(dir, &SkillBundle{Files: map[string][]byte{
		"scripts/run.py": []byte("print(1)"),
	}}))
	info, err := os.Stat(filepath.Join(dir, "scripts", "run.py"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

// The installer writes the version dir from inside the sandbox; the host
// process must not follow a link it planted there.
func TestHostVersionDirSymlinksAreNotFollowed(t *testing.T) {
	versionDir, outside := t.TempDir(), t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(outside, "cache"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "requirements.json"), []byte(`{"env":[]}`), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(versionDir, ".weknora")))

	_, err := noSymlinkPath(versionDir, ".weknora", "cache")
	require.Error(t, err)
	_, err = readNoSymlinkFile(versionDir, sandbox.SkillRequirementsPathIn(versionDir))
	require.Error(t, err)

	missing, err := noSymlinkPath(t.TempDir(), ".weknora", "cache")
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(missing, filepath.Join(".weknora", "cache")))
}

// skillRowOrNil loads a row that may already be gone (post-remove).
func (fx *installFixture) skillRowOrNil(configID, skillID string) *types.TenantSkillEntity {
	row, err := fx.skillRepo.GetSkill(context.Background(), 7, configID, skillID)
	if err != nil {
		return nil
	}
	return row
}

// waitRemoveDone waits until the background RemoveSkill goroutine deletes the row.
func (fx *installFixture) waitRemoveDone(t *testing.T, skillID string) {
	t.Helper()
	require.Eventually(t, func() bool {
		got, err := fx.skillRepo.GetSkill(context.Background(), 7, sandbox.HostSkillTargetID, skillID)
		return err == nil && got == nil
	}, 2*time.Second, 5*time.Millisecond)
}

func TestHostRemoveDeletesFilesAndRow(t *testing.T) {
	fx, tree, _ := hostInstallFixture(t)
	id, err := fx.svc.InstallSkill(context.Background(), 7, sandbox.HostSkillTargetID, fx.installArchive())
	require.NoError(t, err)
	waitHostInstallDone(t, fx, id)

	require.NoError(t, fx.svc.RemoveSkill(context.Background(), 7, sandbox.HostSkillTargetID, id))
	fx.waitRemoveDone(t, id)
	require.Equal(t, []string{fx.bundle.Name}, tree.removed)
	require.Nil(t, fx.skillRowOrNil(sandbox.HostSkillTargetID, id))
	require.Empty(t, fx.snapshotLog())
}

func TestReaperSeesHostFilesOnDisk(t *testing.T) {
	tree := newFakeHostSkillTree(t.TempDir())
	tree.active["pdf"] = tree.VersionsRoot() + "/pdf-1"
	svc := &TenantSkillService{host: liteHost(tree, &fakeHostSkillInstaller{})}
	_, present, ok := svc.skillFilesInLiveImage(context.Background(),
		&types.TenantSkillEntity{Name: "pdf", SandboxConfigID: sandbox.HostSkillTargetID})
	require.True(t, ok)
	require.True(t, present)
	_, present, ok = svc.skillFilesInLiveImage(context.Background(),
		&types.TenantSkillEntity{Name: "docx", SandboxConfigID: sandbox.HostSkillTargetID})
	require.True(t, ok)
	require.False(t, present)
}

type sweepCountingTree struct {
	*fakeHostSkillTree
	sweeps int
}

func (s *sweepCountingTree) Sweep() error { s.sweeps++; return nil }

func TestStartSweepsTheLocalSkillTree(t *testing.T) {
	tree := &sweepCountingTree{fakeHostSkillTree: newFakeHostSkillTree(t.TempDir())}
	svc := NewTenantSkillService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		liteHost(tree, &fakeHostSkillInstaller{}))
	require.NoError(t, svc.Start(context.Background()))
	defer svc.Stop()
	require.Equal(t, 1, tree.sweeps)
}
