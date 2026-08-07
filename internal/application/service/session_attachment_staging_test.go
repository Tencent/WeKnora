package service

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stagingFileService struct {
	files    map[string][]byte
	getCalls map[string]int
}

func (f *stagingFileService) CheckConnectivity(context.Context) error { return nil }
func (f *stagingFileService) SaveFile(context.Context, *multipart.FileHeader, uint64, string) (string, error) {
	panic("unexpected SaveFile")
}
func (f *stagingFileService) SaveBytes(context.Context, []byte, uint64, string, bool) (string, error) {
	panic("unexpected SaveBytes")
}
func (f *stagingFileService) GetFile(_ context.Context, filePath string) (io.ReadCloser, error) {
	if f.getCalls == nil {
		f.getCalls = make(map[string]int)
	}
	f.getCalls[filePath]++
	return io.NopCloser(bytes.NewReader(f.files[filePath])), nil
}
func (f *stagingFileService) GetFileURL(context.Context, string) (string, error) {
	panic("unexpected GetFileURL")
}
func (f *stagingFileService) DeleteFile(context.Context, string) error { return nil }
func (f *stagingFileService) CopyFile(context.Context, string, uint64, string) (string, error) {
	panic("unexpected CopyFile")
}

// stagingSandboxManager is a test double that satisfies both the Manager
// contract and the SessionCapabilityProvider capability accessor. It stores
// in-memory files so a staging test can round-trip attachments without a
// real sandbox.
type stagingSandboxManager struct {
	sandboxType sandbox.SandboxType
	files       map[string][]byte
	writes      []string
	removes     []string

	// disableFiles lets a test simulate a manager that advertises no
	// session filesystem capability (e.g. Local fallback engaged).
	disableFiles bool
}

func (m *stagingSandboxManager) Execute(context.Context, *sandbox.ExecuteConfig) (*sandbox.ExecuteResult, error) {
	panic("unexpected Execute")
}
func (m *stagingSandboxManager) Cleanup(context.Context) error { return nil }
func (m *stagingSandboxManager) GetSandbox() sandbox.Sandbox   { return nil }
func (m *stagingSandboxManager) GetType() sandbox.SandboxType  { return m.sandboxType }

func (m *stagingSandboxManager) SessionShellExecutor() sandbox.SessionShellExecutor { return nil }
func (m *stagingSandboxManager) SessionFileStore() sandbox.SessionFileStore {
	if m.disableFiles {
		return nil
	}
	return m
}

func (m *stagingSandboxManager) EnsureSessionDir(context.Context, string, string) error {
	return nil
}
func (m *stagingSandboxManager) StatSessionFile(context.Context, string, string) (*sandbox.RemoteStatEntry, error) {
	panic("unexpected StatSessionFile")
}
func (m *stagingSandboxManager) ReadSessionFile(context.Context, string, string) ([]byte, error) {
	panic("unexpected ReadSessionFile")
}
func (m *stagingSandboxManager) ListSessionFiles(context.Context, string, string) ([]sandbox.RemoteDirEntry, error) {
	entries := make([]sandbox.RemoteDirEntry, 0, len(m.files))
	for filePath, content := range m.files {
		entries = append(entries, sandbox.RemoteDirEntry{Path: filePath, Size: int64(len(content)), Type: sandbox.RemoteEntryFile})
	}
	return entries, nil
}
func (m *stagingSandboxManager) WriteSessionInputFile(_ context.Context, _ string, filePath string, content []byte) error {
	if m.files == nil {
		m.files = make(map[string][]byte)
	}
	m.files[filePath] = append([]byte(nil), content...)
	m.writes = append(m.writes, filePath)
	return nil
}
func (m *stagingSandboxManager) RemoveSessionInputPath(_ context.Context, _ string, targetPath string) error {
	for filePath := range m.files {
		if filePath == targetPath || strings.HasPrefix(filePath, targetPath+"/") {
			delete(m.files, filePath)
		}
	}
	m.removes = append(m.removes, targetPath)
	return nil
}

func TestStageSessionAttachmentsReconcilesAndSkipsExisting(t *testing.T) {
	t.Setenv("WEKNORA_SANDBOX_MODE", "cube")

	attachment := types.MessageAttachment{
		URL:      "local://tenant/attachment-1",
		FileName: "report.pdf",
		FileType: ".pdf",
		FileSize: 7,
	}
	remotePath, err := sandboxAttachmentPath(attachment)
	require.NoError(t, err)
	stalePath := sandbox.SessionInputRoot + "/stale/old.txt"
	manager := &stagingSandboxManager{
		sandboxType: sandbox.SandboxTypeCube,
		files:       map[string][]byte{stalePath: []byte("old")},
	}
	fileService := &stagingFileService{
		files: map[string][]byte{attachment.URL: []byte("content")},
	}
	service := &agentService{sandboxMgr: manager, fileService: fileService}

	staged, err := service.stageSessionAttachments(
		context.Background(),
		"session-1",
		"",
		types.MessageAttachments{attachment, attachment},
	)

	require.NoError(t, err)
	require.Len(t, staged, 1)
	assert.Equal(t, remotePath, staged[0].Path)
	assert.Equal(t, []string{remotePath}, manager.writes)
	assert.Equal(t, 1, fileService.getCalls[attachment.URL])
	assert.NotEmpty(t, manager.removes)
	assert.NotContains(t, manager.files, stalePath)

	// The second reconciliation sees the same path and size and avoids storage IO.
	_, err = service.stageSessionAttachments(context.Background(), "session-1", "", types.MessageAttachments{attachment})
	require.NoError(t, err)
	assert.Equal(t, 1, fileService.getCalls[attachment.URL])
}

func TestStageSessionAttachmentsSkipsWhenNoFilesystemCapability(t *testing.T) {
	t.Setenv("WEKNORA_SANDBOX_MODE", "local")

	// disableFiles=true simulates any manager without a session filesystem
	// capability (Local/Docker/Disabled DefaultManager, or a
	// SessionBoundManager whose remote provider fell back to Local).
	manager := &stagingSandboxManager{
		sandboxType:  sandbox.SandboxTypeLocal,
		disableFiles: true,
	}
	service := &agentService{sandboxMgr: manager, fileService: &stagingFileService{}}

	staged, err := service.stageSessionAttachments(context.Background(), "session-1", "", types.MessageAttachments{{
		URL: "local://tenant/file", FileName: "file.txt",
	}})

	require.NoError(t, err)
	assert.Empty(t, staged)
	assert.Empty(t, manager.writes)
}

func TestBuildSandboxAttachmentsPromptEscapesMetadata(t *testing.T) {
	prompt := buildSandboxAttachmentsPrompt([]stagedSessionAttachment{{
		Name: "a<&>.txt", FileType: ".txt", Size: 3, Path: "/workspace/input/hash/a.txt",
	}})

	assert.Contains(t, prompt, `name="a&lt;&amp;&gt;.txt"`)
	assert.Contains(t, prompt, `path="/workspace/input/hash/a.txt"`)
	assert.Contains(t, prompt, "read-only inputs")
}
