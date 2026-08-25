package service

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestListSkillFilesReadsTheStoredArchive(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedStoredSkillBundle(t)

	files, err := fx.svc.ListSkillFiles(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.Equal(t, []SkillFileEntry{
		{Path: "SKILL.md", Size: int64(len(validSkillMD))},
		{Path: "scripts/extract.py", Size: int64(len("print('hi')\n"))},
	}, files)
}

func TestListSkillFilesRefusesAnotherWorkspace(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedStoredSkillBundle(t)

	_, err := fx.svc.ListSkillFiles(context.Background(), 8, "cfg-1", "sk-1")
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	require.Equal(t, 404, appErr.HTTPCode)
}

func TestListSkillFilesReportsMissingBundle(t *testing.T) {
	fx := newInstallFixture(t)

	_, err := fx.svc.ListSkillFiles(context.Background(), 7, "cfg-1", "sk-1")
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	require.Equal(t, 404, appErr.HTTPCode)
}

func TestReadSkillFileReturnsTextContent(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedStoredSkillBundle(t)

	file, err := fx.svc.ReadSkillFile(context.Background(), 7, "cfg-1", "sk-1", "scripts/extract.py")
	require.NoError(t, err)
	require.Equal(t, "scripts/extract.py", file.Path)
	require.Equal(t, skillFileEncodingUTF8, file.Encoding)
	require.Equal(t, "print('hi')\n", file.Content)
	require.False(t, file.Binary)
}

func TestReadSkillFileRejectsPathTraversal(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedStoredSkillBundle(t)

	for _, rel := range []string{"../secret", "/etc/passwd", "scripts/../../SKILL.md", `scripts\extract.py`} {
		_, err := fx.svc.ReadSkillFile(context.Background(), 7, "cfg-1", "sk-1", rel)
		require.Error(t, err, rel)
		appErr, ok := apperrors.IsAppError(err)
		require.True(t, ok, rel)
		require.Equal(t, 400, appErr.HTTPCode, rel)
	}
}

func TestReadSkillFileReportsMissingPath(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedStoredSkillBundle(t)

	_, err := fx.svc.ReadSkillFile(context.Background(), 7, "cfg-1", "sk-1", "missing.txt")
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	require.Equal(t, 404, appErr.HTTPCode)
}

func TestReadSkillFileInlinesASmallImage(t *testing.T) {
	fx := newInstallFixture(t)
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	archive := zipBundle(t, map[string]string{
		"SKILL.md":     validSkillMD,
		"assets/a.png": string(png),
	})
	fx.storeSkillBundle(t, "sk-1", archive)

	file, err := fx.svc.ReadSkillFile(context.Background(), 7, "cfg-1", "sk-1", "assets/a.png")
	require.NoError(t, err)
	require.Equal(t, skillFileEncodingBase64, file.Encoding)
	require.Equal(t, "image/png", file.MediaType)
	decoded, err := base64.StdEncoding.DecodeString(file.Content)
	require.NoError(t, err)
	require.Equal(t, png, decoded)
}

func TestProjectSkillFileContentMarksBinaryWithoutInlining(t *testing.T) {
	got := projectSkillFileContent("scripts/tool.bin", []byte{0x00, 0x01, 0xff})
	require.True(t, got.Binary)
	require.Equal(t, skillFileEncodingBinary, got.Encoding)
	require.Empty(t, got.Content)
}

func (f *installFixture) seedStoredSkillBundle(t *testing.T) {
	t.Helper()
	archive, err := zipSkillFiles(map[string][]byte{
		"SKILL.md":           []byte(validSkillMD),
		"scripts/extract.py": []byte("print('hi')\n"),
	})
	require.NoError(t, err)
	f.storeSkillBundle(t, "sk-1", archive)
}

func (f *installFixture) storeSkillBundle(t *testing.T, skillID string, archive []byte) {
	t.Helper()
	ctx := context.Background()
	skill, err := f.skillRepo.GetSkill(ctx, 7, "cfg-1", skillID)
	require.NoError(t, err)
	require.NotNil(t, skill)
	skill.BundleRef = "file://bundle.zip"
	skill.Status = types.SkillStatusReady
	require.NoError(t, f.skillRepo.UpdateSkill(ctx, skill))
	if f.storedBundles == nil {
		f.storedBundles = map[string][]byte{}
	}
	copied := make([]byte, len(archive))
	copy(copied, archive)
	f.storedBundles["file://bundle.zip"] = copied
}
