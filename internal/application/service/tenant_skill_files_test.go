package service

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
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

func TestListSkillFilesUsesCatalogWhenInstallHasNoRef(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	archive, err := zipSkillFiles(map[string][]byte{
		"SKILL.md":           []byte(validSkillMD),
		"scripts/extract.py": []byte("print('hi')\n"),
	})
	require.NoError(t, err)
	fx.storedBundles = map[string][]byte{"file://catalog.zip": archive}
	require.NoError(t, fx.skillRepo.CreateCatalog(ctx, &types.TenantSkillCatalogEntity{
		ID: "cat-1", TenantID: 7, Name: "pdf-tools",
		BundleRef: "file://catalog.zip", BundleSHA256: skillArchiveSHA256(archive),
	}))
	skill, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	skill.CatalogID = "cat-1"
	skill.BundleRef = ""
	skill.Status = types.SkillStatusReady
	require.NoError(t, fx.skillRepo.UpdateSkill(ctx, skill))

	files, err := fx.svc.ListSkillFiles(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.NotEmpty(t, files)
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

func TestSkillBundleArchiveCacheKeepsOversizeAsSoleOccupant(t *testing.T) {
	c := &skillBundleArchiveCache{slots: 4, maxBytes: 8}
	c.put("small", []byte("abcd"))
	c.put("big", []byte("0123456789"))

	require.Equal(t, []byte("0123456789"), c.get("big"))
	require.Nil(t, c.get("small"),
		"a zip over the keep-around budget must not sit next to other entries")
}

func TestSkillBundleArchiveCacheEvictsToStayUnderBudget(t *testing.T) {
	c := &skillBundleArchiveCache{slots: 4, maxBytes: 8}
	c.put("a", []byte("aaaa"))
	c.put("b", []byte("bbbb"))
	c.put("c", []byte("cccc"))

	require.Equal(t, []byte("cccc"), c.get("c"))
	require.Equal(t, []byte("bbbb"), c.get("b"))
	require.Nil(t, c.get("a"))
}

func TestSkillBundleArchiveCacheRespectsSlotCount(t *testing.T) {
	c := &skillBundleArchiveCache{slots: 2, maxBytes: 100}
	c.put("a", []byte("a"))
	c.put("b", []byte("b"))
	c.put("c", []byte("c"))

	require.Nil(t, c.get("a"))
	require.Equal(t, []byte("c"), c.get("c"))
	require.Equal(t, []byte("b"), c.get("b"))
}

func TestListAndReadSkillFilesDownloadTheBundleOnce(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedStoredSkillBundle(t)

	files, err := fx.svc.ListSkillFiles(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	_, err = fx.svc.ReadSkillFile(context.Background(), 7, "cfg-1", "sk-1", "SKILL.md")
	require.NoError(t, err)
	_, err = fx.svc.ReadSkillFile(context.Background(), 7, "cfg-1", "sk-1", "scripts/extract.py")
	require.NoError(t, err)

	require.Equal(t, int32(1), fx.getFileCalls.Load())
}

func TestListAndReadSkillFilesCoalesceConcurrentDownloads(t *testing.T) {
	fx := newInstallFixture(t)
	fx.seedStoredSkillBundle(t)

	var wg sync.WaitGroup
	errCh := make(chan error, 3)
	wg.Add(3)
	go func() {
		defer wg.Done()
		_, err := fx.svc.ListSkillFiles(context.Background(), 7, "cfg-1", "sk-1")
		errCh <- err
	}()
	go func() {
		defer wg.Done()
		_, err := fx.svc.ReadSkillFile(context.Background(), 7, "cfg-1", "sk-1", "SKILL.md")
		errCh <- err
	}()
	go func() {
		defer wg.Done()
		_, err := fx.svc.ReadSkillFile(context.Background(), 7, "cfg-1", "sk-1", "scripts/extract.py")
		errCh <- err
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), fx.getFileCalls.Load())
}

func TestListSkillFilesStripsAWrapDirectory(t *testing.T) {
	fx := newInstallFixture(t)
	fx.storeSkillBundle(t, "sk-1", zipBundle(t, map[string]string{
		"pdf-tools/SKILL.md":           validSkillMD,
		"pdf-tools/scripts/extract.py": "print('hi')\n",
	}))

	files, err := fx.svc.ListSkillFiles(context.Background(), 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	require.Equal(t, []SkillFileEntry{
		{Path: "SKILL.md", Size: int64(len(validSkillMD))},
		{Path: "scripts/extract.py", Size: int64(len("print('hi')\n"))},
	}, files)

	file, err := fx.svc.ReadSkillFile(context.Background(), 7, "cfg-1", "sk-1", "scripts/extract.py")
	require.NoError(t, err)
	require.Equal(t, "print('hi')\n", file.Content)
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

func TestListSkillFilesDoesNotFallbackToADifferentCatalogBundle(t *testing.T) {
	fx := newInstallFixture(t)
	ctx := context.Background()
	other := zipBundle(t, map[string]string{
		"SKILL.md":           validSkillMD,
		"scripts/extract.py": "print('other')\n",
	})
	fx.storedBundles = map[string][]byte{"file://catalog.zip": other}
	require.NoError(t, fx.skillRepo.CreateCatalog(ctx, &types.TenantSkillCatalogEntity{
		ID: "cat-1", TenantID: 7, Name: "pdf-tools",
		BundleRef: "file://catalog.zip", BundleSHA256: skillArchiveSHA256(other),
	}))
	skill, err := fx.skillRepo.GetSkill(ctx, 7, "cfg-1", "sk-1")
	require.NoError(t, err)
	skill.CatalogID = "cat-1"
	skill.BundleRef = "file://missing.zip"
	skill.BundleSHA256 = strings.Repeat("a", 64)
	require.NoError(t, fx.skillRepo.UpdateSkill(ctx, skill))

	_, err = fx.svc.ListSkillFiles(ctx, 7, "cfg-1", "sk-1")
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	require.Equal(t, 404, appErr.HTTPCode)
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
