package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
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
	}, func(string) ([]byte, error) { return nil, errors.New("must not be called") })

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
	}}, func(string) ([]byte, error) { return nil, errors.New("must not be called") })

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
	}}, func(ref string) ([]byte, error) {
		require.Equal(t, "local://sk-1.zip", ref)
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
	}}, func(string) ([]byte, error) {
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
	}}, func(string) ([]byte, error) {
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

func TestManagerIgnoresPreloadedSkillsWhenTenantSourceIsAttached(t *testing.T) {
	dir := preloadedSkillDir(t, "document-analyzer", "preloaded description")
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
		"host preloaded skills are not in the sandbox image")
	require.Equal(t, "tenant description", byName["pdf"].Description)
	require.Equal(t, "tenant only", byName["csv"].Description)

	skill, err := mgr.LoadSkill(context.Background(), "pdf")
	require.NoError(t, err)
	require.Equal(t, "tenant body", skill.Instructions)

	_, err = mgr.LoadSkill(context.Background(), "document-analyzer")
	require.Error(t, err, "a host-only skill must not be readable once the image is the source")
}

func TestManagerRunsATenantSkillFromTheImageWithoutUploading(t *testing.T) {
	sandboxMgr := &recordingSandboxManager{}
	mgr := NewManager(&ManagerConfig{SkillDirs: nil, Enabled: true}, sandboxMgr)
	mgr.WithTenantSource(NewTenantSkillSource([]*types.TenantSkillEntity{{
		ID: "sk-1", Name: "pdf", Status: types.SkillStatusReady, Enabled: true,
	}}, nil))
	require.NoError(t, mgr.Initialize(context.Background()))

	_, err := mgr.ExecuteScript(
		types.WithSessionID(context.Background(), "sess-1"),
		"pdf", "scripts/extract.py", []string{"--flag"}, "",
	)

	require.NoError(t, err)
	require.NotNil(t, sandboxMgr.config)
	require.Equal(t, "/opt/weknora/tenant/skills/pdf/scripts/extract.py",
		sandboxMgr.config.RemoteScriptPath)
	require.Empty(t, sandboxMgr.config.Script,
		"there is no host-side copy of an installed skill to upload")
	require.Equal(t, []string{"--flag"}, sandboxMgr.config.Args)
	require.Equal(t, "sess-1", sandboxMgr.config.SessionID)
	require.Equal(t, sandbox.SkillsImageRoot+"/pdf",
		sandboxMgr.config.Env["WEKNORA_SKILL_DIR"])
	require.Equal(t, "/workspace/output", sandboxMgr.config.Env[artifactOutputEnvVar])
}

// Preloaded skills keep uploading from the host and keep running in their own
// directory; the tenant source must not change that path at all.
func TestManagerKeepsPreloadedSkillExecutionWhenNoTenantSource(t *testing.T) {
	dir := preloadedSkillDir(t, "pdf", "preloaded description")
	sandboxMgr := &recordingSandboxManager{}
	mgr := NewManager(&ManagerConfig{SkillDirs: []string{dir}, Enabled: true}, sandboxMgr)
	require.NoError(t, mgr.Initialize(context.Background()))

	_, err := mgr.ExecuteScript(context.Background(), "pdf", "scripts/run.py", nil, "")

	require.NoError(t, err)
	require.NotNil(t, sandboxMgr.config)
	require.Empty(t, sandboxMgr.config.RemoteScriptPath)
	require.Equal(t, filepath.Join(dir, "pdf", "scripts", "run.py"), sandboxMgr.config.Script)
	require.Equal(t, dir+"/pdf", sandboxMgr.config.WorkDir)
}

type recordingSandboxManager struct {
	config *sandbox.ExecuteConfig
}

func (m *recordingSandboxManager) Execute(
	_ context.Context, config *sandbox.ExecuteConfig,
) (*sandbox.ExecuteResult, error) {
	m.config = config
	return &sandbox.ExecuteResult{ExitCode: 0}, nil
}

func (m *recordingSandboxManager) Cleanup(context.Context) error { return nil }
func (m *recordingSandboxManager) GetSandbox() sandbox.Sandbox   { return nil }
func (m *recordingSandboxManager) GetType() sandbox.SandboxType  { return sandbox.SandboxTypeCube }

// preloadedSkillDir writes one deployment-preloaded skill to a temp directory
// and returns the search root it lives under.
func preloadedSkillDir(t *testing.T, name, description string) string {
	t.Helper()
	root := t.TempDir()
	skillDir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Join(skillDir, "scripts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, SkillFileName),
		[]byte("---\nname: "+name+"\ndescription: "+description+"\n---\npreloaded body\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "scripts", "run.py"),
		[]byte("print('preloaded')\n"), 0o644))
	return root
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
