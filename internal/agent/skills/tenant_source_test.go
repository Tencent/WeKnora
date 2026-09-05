package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestTenantSkillSourceOnlyExposesUsableSkills(t *testing.T) {
	rows := []*types.TenantSkillEntity{
		{ID: "sk-1", Name: "ready-enabled", Status: types.SkillStatusReady, Enabled: true},
		{ID: "sk-2", Name: "ready-disabled", Status: types.SkillStatusReady, Enabled: false},
		{ID: "sk-3", Name: "still-installing", Status: types.SkillStatusInstalling, Enabled: true},
		{ID: "sk-4", Name: "failed", Status: types.SkillStatusFailed, Enabled: true},
	}
	src := NewTenantSkillSource(rows, nil)

	metadata, err := src.DiscoverSkills()

	require.NoError(t, err)
	require.Len(t, metadata, 1,
		"a skill the agent cannot actually run must never reach the system prompt")
	require.Equal(t, "ready-enabled", metadata[0].Name)
}

// The name is joined under the skills root to build every path this source
// hands out, so it has to be a single path segment. Execution rejects an
// escaping path anyway, but the base path travels to the model in metadata
// and in SkillFile.Path, which is the same reasoning guardSkillDir applies
// on the install side.
func TestTenantSkillSourceRejectsANameThatIsNotOnePathSegment(t *testing.T) {
	src := NewTenantSkillSource([]*types.TenantSkillEntity{
		{ID: "sk-2", Name: "../escaping", Status: types.SkillStatusReady, Enabled: true},
		{ID: "sk-3", Name: "nested/name", Status: types.SkillStatusReady, Enabled: true},
		{ID: "sk-4", Name: "..", Status: types.SkillStatusReady, Enabled: true},
	}, nil)

	metadata, err := src.DiscoverSkills()
	require.NoError(t, err)
	require.Empty(t, metadata)
	for _, name := range []string{"../escaping", "nested/name", ".."} {
		_, err := src.GetSkillBasePath(name)
		require.Error(t, err, "%s must be invisible, not merely unexecutable", name)
	}
}

func TestTenantSkillSourceBasePathIsTheImageDir(t *testing.T) {
	src := NewTenantSkillSource([]*types.TenantSkillEntity{
		{ID: "sk-1", Name: "pdf", Status: types.SkillStatusReady, Enabled: true},
	}, nil)

	base, err := src.GetSkillBasePath("pdf")

	require.NoError(t, err)
	require.Equal(t, "/opt/weknora/tenant/skills/pdf", base,
		"the path is the skill name: that is the directory the installer writes")
}

// A skill the source does not expose must be unreachable through every entry
// point, not just discovery: routing decisions elsewhere ask these methods.
func TestTenantSkillSourceHidesUnusableSkillsFromEveryLookup(t *testing.T) {
	src := NewTenantSkillSource([]*types.TenantSkillEntity{
		{ID: "sk-2", Name: "ready-disabled", Status: types.SkillStatusReady, Enabled: false},
	}, func(*types.TenantSkillEntity) ([]byte, error) { return nil, errors.New("must not be called") })

	_, err := src.GetSkillBasePath("ready-disabled")
	require.Error(t, err)
	_, err = src.LoadSkillInstructions("ready-disabled")
	require.Error(t, err)
	_, err = src.ListSkillFiles("ready-disabled")
	require.Error(t, err)
	_, err = src.LoadSkillFile("ready-disabled", "scripts/run.py")
	require.Error(t, err)
	_, err = src.RemoteScriptPath("ready-disabled", "scripts/run.py")
	require.Error(t, err)
}

// Level 2 comes from the database projection: the request path must not depend
// on downloading an archive to answer "what does this skill say".
func TestTenantSkillSourceLoadsInstructionsFromTheRow(t *testing.T) {
	src := NewTenantSkillSource([]*types.TenantSkillEntity{{
		ID: "sk-1", Name: "pdf", Description: "PDF helpers",
		Instructions: "Run scripts/extract.py.",
		Status:       types.SkillStatusReady, Enabled: true,
	}}, func(*types.TenantSkillEntity) ([]byte, error) { return nil, errors.New("must not be called") })

	skill, err := src.LoadSkillInstructions("pdf")

	require.NoError(t, err)
	require.Equal(t, "pdf", skill.Name)
	require.Equal(t, "PDF helpers", skill.Description)
	require.Equal(t, "Run scripts/extract.py.", skill.Instructions)
	require.True(t, skill.Loaded)
	require.Equal(t, "/opt/weknora/tenant/skills/pdf", skill.BasePath)
	require.Equal(t, "/opt/weknora/tenant/skills/pdf/SKILL.md", skill.FilePath)
}

func TestTenantSkillSourceReadsLevel3FilesFromTheBundle(t *testing.T) {
	archive := zipArchive(t, map[string]string{
		// Wrapped in a top-level directory, which is how people upload.
		"pdf-tools/SKILL.md":           "---\nname: pdf\ndescription: d\n---\nbody\n",
		"pdf-tools/reference/FORMS.md": "form notes",
		"pdf-tools/scripts/extract.py": "print('hi')\n",
	})
	src := NewTenantSkillSource([]*types.TenantSkillEntity{{
		ID: "sk-1", Name: "pdf", Status: types.SkillStatusReady, Enabled: true,
		BundleRef: "local://sk-1.zip", BundleSHA256: "sha-1",
	}}, func(row *types.TenantSkillEntity) ([]byte, error) {
		require.Equal(t, "local://sk-1.zip", row.BundleRef)
		return archive, nil
	})

	files, err := src.ListSkillFiles("pdf")
	require.NoError(t, err)
	require.Equal(t, []string{"SKILL.md", "reference/FORMS.md", "scripts/extract.py"}, files)

	file, err := src.LoadSkillFile("pdf", "reference/FORMS.md")
	require.NoError(t, err)
	require.Equal(t, "form notes", file.Content)
	require.Equal(t, "/opt/weknora/tenant/skills/pdf/reference/FORMS.md", file.Path,
		"the path the model is shown must be the one it can execute or read in the sandbox")
	require.False(t, file.IsScript)

	script, err := src.LoadSkillFile("pdf", "scripts/extract.py")
	require.NoError(t, err)
	require.True(t, script.IsScript)
}

func TestTenantSkillSourceDownloadsEachBundleOnce(t *testing.T) {
	archive := zipArchive(t, map[string]string{
		"SKILL.md":           "---\nname: pdf\ndescription: d\n---\nbody\n",
		"scripts/extract.py": "print('hi')\n",
	})
	downloads := 0
	src := NewTenantSkillSource([]*types.TenantSkillEntity{{
		ID: "sk-1", Name: "pdf", Status: types.SkillStatusReady, Enabled: true,
		BundleRef: "local://sk-1.zip", BundleSHA256: "sha-1",
	}}, func(*types.TenantSkillEntity) ([]byte, error) {
		downloads++
		return archive, nil
	})

	_, err := src.ListSkillFiles("pdf")
	require.NoError(t, err)
	_, err = src.LoadSkillFile("pdf", "scripts/extract.py")
	require.NoError(t, err)

	require.Equal(t, 1, downloads,
		"one read_skill call per file would re-download the whole archive every time")
}

func TestTenantSkillSourceRefusesPathsOutsideTheSkill(t *testing.T) {
	src := NewTenantSkillSource([]*types.TenantSkillEntity{{
		ID: "sk-1", Name: "pdf", Status: types.SkillStatusReady, Enabled: true,
		BundleRef: "local://sk-1.zip",
	}}, func(*types.TenantSkillEntity) ([]byte, error) {
		return nil, errors.New("must not be called")
	})

	for _, rel := range []string{"../other/secret", "/etc/passwd", ""} {
		_, err := src.LoadSkillFile("pdf", rel)
		require.Error(t, err, "rel=%q", rel)
		_, err = src.RemoteScriptPath("pdf", rel)
		require.Error(t, err, "rel=%q", rel)
	}
}

// A skill whose archive could not be stored is still installed and runnable in
// the image, so execution must not depend on the bundle being downloadable.
func TestTenantSkillSourceReportsAMissingBundleWithoutBlockingExecution(t *testing.T) {
	src := NewTenantSkillSource([]*types.TenantSkillEntity{{
		ID: "sk-1", Name: "pdf", Status: types.SkillStatusReady, Enabled: true,
	}}, nil)

	_, err := src.LoadSkillFile("pdf", "scripts/extract.py")
	require.Error(t, err)

	remote, err := src.RemoteScriptPath("pdf", "scripts/extract.py")
	require.NoError(t, err)
	require.Equal(t, "/opt/weknora/tenant/skills/pdf/scripts/extract.py", remote)
}

func TestManagerIgnoresHostSkillsWhenTenantSourceIsAttached(t *testing.T) {
	dir := hostSkillDir(t, "document-analyzer", "host description")
	mgr := NewManager(&ManagerConfig{SkillDirs: []string{dir}, Enabled: true}, nil)
	mgr.WithTenantSource(NewTenantSkillSource([]*types.TenantSkillEntity{
		{
			ID: "sk-1", Name: "pdf", Description: "tenant description",
			Instructions: "tenant body", Status: types.SkillStatusReady, Enabled: true,
		},
		{
			ID: "sk-2", Name: "csv", Description: "tenant only",
			Status: types.SkillStatusReady, Enabled: true,
		},
	}, nil))

	require.NoError(t, mgr.Initialize(context.Background()))

	byName := map[string]*SkillMetadata{}
	for _, meta := range mgr.GetAllMetadata() {
		byName[meta.Name] = meta
	}
	require.Len(t, byName, 2)
	require.NotContains(t, byName, "document-analyzer",
		"host skills are not in the sandbox image")
	require.Equal(t, "tenant description", byName["pdf"].Description)
	require.Equal(t, "tenant only", byName["csv"].Description)

	skill, err := mgr.LoadSkill(context.Background(), "pdf")
	require.NoError(t, err)
	require.Equal(t, "tenant body", skill.Instructions)

	_, err = mgr.LoadSkill(context.Background(), "document-analyzer")
	require.Error(t, err, "a host-only skill must not be readable once the image is the source")
}

func TestSandboxSkillDirOnlyAnswersForInstalledSkills(t *testing.T) {
	installed := NewManager(&ManagerConfig{Enabled: true}, nil)
	installed.WithTenantSource(NewTenantSkillSource([]*types.TenantSkillEntity{{
		ID: "sk-1", Name: "pdf", Status: types.SkillStatusReady, Enabled: true,
	}}, nil))
	require.NoError(t, installed.Initialize(context.Background()))

	dir, ok := installed.SandboxSkillDir("pdf")
	require.True(t, ok)
	require.Equal(t, sandbox.SkillsImageRoot+"/pdf", dir)

	host := NewManager(&ManagerConfig{
		SkillDirs: []string{hostSkillDir(t, "pdf", "host description")},
		Enabled:   true,
	}, nil)
	require.NoError(t, host.Initialize(context.Background()))

	_, ok = host.SandboxSkillDir("pdf")
	require.False(t, ok)
}

// Host skills keep uploading from the WeKnora machine and keep running in
// their staged directory; the tenant source must not change that path at all.
type recordingSandboxManager struct {
	config *sandbox.ExecuteConfig
	calls  int
}

func (m *recordingSandboxManager) Execute(
	_ context.Context, config *sandbox.ExecuteConfig,
) (*sandbox.ExecuteResult, error) {
	m.calls++
	m.config = config
	return &sandbox.ExecuteResult{ExitCode: 0}, nil
}

func (m *recordingSandboxManager) Cleanup(context.Context) error { return nil }
func (m *recordingSandboxManager) GetSandbox() sandbox.Sandbox   { return nil }
func (m *recordingSandboxManager) GetType() sandbox.SandboxType  { return sandbox.SandboxTypeCube }

// hostSkillDir writes one host skill to a temp directory and returns the
// search root it lives under.
func hostSkillDir(t *testing.T, name, description string) string {
	t.Helper()
	root := t.TempDir()
	skillDir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Join(skillDir, "scripts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, SkillFileName),
		[]byte("---\nname: "+name+"\ndescription: "+description+"\n---\nhost body\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "scripts", "run.py"),
		[]byte("print('host')\n"), 0o644))
	return root
}

func TestTenantSkillSourceCacheKeepsOneOversizeArchive(t *testing.T) {
	src := NewTenantSkillSource(nil, nil)
	src.store("small", bytes.Repeat([]byte("s"), 32))
	src.store("big", bytes.Repeat([]byte("b"), cachedBundleBytes+1))

	require.Equal(t, bytes.Repeat([]byte("b"), cachedBundleBytes+1), src.cached("big"))
	require.Nil(t, src.cached("small"),
		"a zip over the keep-around budget must not sit next to other entries")
}

// The agent reads the same archive the install accepted, so the two have to
// count it the same way. Counting raw zip entries against the skill-file cap
// rejects bundles the install took — directory entries alone can carry a real
// skill past 20k — and read_skill then fails on an install that works.
const bundleIndexSkillMD = "---\nname: pdf\ndescription: d\n---\nbody\n"

func TestSkillBundleFileIndexCountsFilesTheWayTheInstallDid(t *testing.T) {
	files := map[string]string{"repo-main/" + SkillFileName: bundleIndexSkillMD}
	for i := 0; i < maxBundleEntries-1; i++ {
		files[fmt.Sprintf("repo-main/templates/asset-%d.txt", i)] = ""
	}
	// Directory entries push the raw count past the skill-file cap without
	// adding a single file the skill is made of.
	dirs := make([]string, 0, 2000)
	for i := 0; i < 2000; i++ {
		dirs = append(dirs, fmt.Sprintf("repo-main/templates/dir-%d/", i))
	}

	index, err := skillBundleFileIndex(zipArchiveWithDirs(t, files, dirs))

	require.NoError(t, err)
	require.Len(t, index, maxBundleEntries)
	require.Contains(t, index, SkillFileName)
}

func TestSkillBundleFileIndexRejectsMoreSkillFilesThanTheCap(t *testing.T) {
	files := map[string]string{"repo-main/" + SkillFileName: bundleIndexSkillMD}
	for i := 0; i < maxBundleEntries; i++ {
		files[fmt.Sprintf("repo-main/templates/asset-%d.txt", i)] = ""
	}

	_, err := skillBundleFileIndex(zipArchiveWithDirs(t, files, nil))

	require.ErrorContains(t, err, "more than 20000 files")
}

func zipArchiveWithDirs(t *testing.T, files map[string]string, dirs []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, name := range dirs {
		_, err := writer.Create(name)
		require.NoError(t, err)
	}
	for name, content := range files {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buf.Bytes()
}

func zipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range files {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buf.Bytes()
}
