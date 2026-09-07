package service

import (
	"archive/zip"
	"bytes"
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type zipAttachmentStorage struct {
	interfaces.FileService
	archive []byte
}

func (s *zipAttachmentStorage) SaveBytes(_ context.Context, data []byte, _ uint64, _ string, _ bool) (string, error) {
	s.archive = append([]byte(nil), data...)
	return "provider://attachment/skill.zip", nil
}

func TestZIPAttachmentPreservesBundleWithoutParsing(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.TemporaryDocument{}))
	repo := repository.NewTemporaryDocumentRepository(db)
	storage := &zipAttachmentStorage{}
	// No parser or queue: a ZIP must be usable without either one.
	svc := &temporaryDocumentService{repo: repo, fileService: storage}
	var archive bytes.Buffer
	w := zip.NewWriter(&archive)
	file, err := w.Create("example/SKILL.md")
	require.NoError(t, err)
	_, err = file.Write([]byte("---\nname: example\ndescription: Example skill\n---\nUse this skill."))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	doc, err := svc.Create(ctx, 7, "session", "example.ZIP", "application/zip",
		int64(archive.Len()), bytes.NewReader(archive.Bytes()), types.TemporaryDocumentCreateOptions{})
	require.NoError(t, err)
	require.Equal(t, types.TemporaryDocumentStatusReady, doc.Status)
	require.NotNil(t, doc.ReadyAt)
	require.Equal(t, archive.Bytes(), storage.archive)
	stored, err := repo.GetScoped(ctx, 7, "session", doc.ID)
	require.NoError(t, err)
	require.Equal(t, types.TemporaryDocumentStatusReady, stored.Status)
	require.Empty(t, stored.Content)
	bundle, err := ParseSkillBundle(storage.archive)
	require.NoError(t, err)
	require.Equal(t, "example", bundle.Name)
}
