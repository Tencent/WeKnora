package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestExtractMarkdownLocalImageRefs(t *testing.T) {
	markdown := strings.Join([]string{
		`![relative](images/pic.png)`,
		`![dot](./images/space%20name.JPG)`,
		`![sibling](../shared/diagram.svg)`,
		`![angle](<images/flow chart.webp>)`,
		`![remote](https://example.com/remote.png)`,
		`![data](data:image/png;base64,AAA)`,
		`![provider](local://images/already.png)`,
		`![absolute](/var/tmp/secret.png)`,
		`![escape](../../../secret.png)`,
		`![not-image](notes/readme.txt)`,
	}, "\n")

	refs := extractMarkdownLocalImageRefs(markdown, "docs/chapter/readme.md")

	expected := map[string]string{
		"images/pic.png":            "docs/chapter/images/pic.png",
		"./images/space%20name.JPG": "docs/chapter/images/space name.JPG",
		"../shared/diagram.svg":     "docs/shared/diagram.svg",
		"<images/flow chart.webp>":  "docs/chapter/images/flow chart.webp",
	}
	if len(refs) != len(expected) {
		t.Fatalf("expected %d refs but got %d: %+v", len(expected), len(refs), refs)
	}

	for _, ref := range refs {
		if expected[ref.originalRef] != ref.fileName {
			t.Fatalf("unexpected ref resolution for %q: got %q want %q", ref.originalRef, ref.fileName, expected[ref.originalRef])
		}
	}
}

func TestNormalizeMarkdownLocalImagePathSkipsUnsafeRefs(t *testing.T) {
	tests := []struct {
		name      string
		ref       string
		want      string
		wantValid bool
	}{
		{name: "same directory", ref: "pic.png", want: "docs/pic.png", wantValid: true},
		{name: "sub directory", ref: "images/pic.png", want: "docs/images/pic.png", wantValid: true},
		{name: "sibling directory", ref: "../assets/pic.png", want: "assets/pic.png", wantValid: true},
		{name: "encoded spaces", ref: "images/pic%201.png", want: "docs/images/pic 1.png", wantValid: true},
		{name: "absolute path", ref: "/tmp/pic.png", wantValid: false},
		{name: "network scheme", ref: "https://example.com/pic.png", wantValid: false},
		{name: "provider scheme", ref: "local://images/pic.png", wantValid: false},
		{name: "data uri", ref: "data:image/png;base64,AAA", wantValid: false},
		{name: "windows absolute path", ref: "C:\\temp\\pic.png", wantValid: false},
		{name: "path traversal", ref: "../../pic.png", wantValid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, valid := normalizeMarkdownLocalImagePath(tt.ref, "docs/readme.md")
			if valid != tt.wantValid {
				t.Fatalf("valid = %v, want %v", valid, tt.wantValid)
			}
			if got != tt.want {
				t.Fatalf("path = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMimeTypeForImageFileUsesExtension(t *testing.T) {
	if got := mimeTypeForImageFile("images/diagram.svg", []byte("<svg></svg>")); got != "image/svg+xml" {
		t.Fatalf("got %q, want image/svg+xml", got)
	}
	if got := mimeTypeForImageFile("images/photo.JPG", nil); got != "image/jpeg" {
		t.Fatalf("got %q, want image/jpeg", got)
	}
}

func TestAttachMarkdownLocalImagesUsesUploadedImageKnowledge(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&types.Knowledge{}); err != nil {
		t.Fatal(err)
	}

	imageData := []byte("uploaded image bytes")
	imageFilePath := "local://images/pic.png"
	if err := db.Create(&types.Knowledge{
		ID:              "image-knowledge",
		TenantID:        1,
		KnowledgeBaseID: "kb-a",
		Type:            "file",
		FileName:        "docs/images/pic.png",
		FileType:        "png",
		FilePath:        imageFilePath,
	}).Error; err != nil {
		t.Fatal(err)
	}

	svc := &knowledgeService{
		repo: repository.NewKnowledgeRepository(db),
		fileSvc: &memoryFileService{
			files: map[string][]byte{imageFilePath: imageData},
		},
	}
	result := &types.ReadResult{
		MarkdownContent: "![pic](images/pic.png)",
	}

	svc.attachMarkdownLocalImages(context.Background(), &types.KnowledgeBase{ID: "kb-a"}, types.DocumentProcessPayload{
		TenantID:        1,
		KnowledgeID:     "markdown-knowledge",
		FileName:        "docs/readme.md",
		FileType:        "md",
		KnowledgeBaseID: "kb-a",
	}, result)

	if len(result.ImageRefs) != 1 {
		t.Fatalf("expected 1 image ref but got %d", len(result.ImageRefs))
	}
	ref := result.ImageRefs[0]
	if ref.OriginalRef != "images/pic.png" {
		t.Fatalf("got original ref %q", ref.OriginalRef)
	}
	if !bytes.Equal(ref.ImageData, imageData) {
		t.Fatal("image data mismatch")
	}
	if !ref.IsOriginal {
		t.Fatal("local markdown image should skip icon filtering")
	}
}

type memoryFileService struct {
	files map[string][]byte
}

func (m *memoryFileService) CheckConnectivity(context.Context) error {
	return nil
}

func (m *memoryFileService) SaveFile(context.Context, *multipart.FileHeader, uint64, string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (m *memoryFileService) SaveBytes(context.Context, []byte, uint64, string, bool) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (m *memoryFileService) GetFile(_ context.Context, filePath string) (io.ReadCloser, error) {
	data, ok := m.files[filePath]
	if !ok {
		return nil, fmt.Errorf("file not found: %s", filePath)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *memoryFileService) GetFileURL(context.Context, string) (string, error) {
	return "", nil
}

func (m *memoryFileService) DeleteFile(context.Context, string) error {
	return nil
}
