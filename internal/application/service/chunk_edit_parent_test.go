package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type editableChunkRepo struct {
	interfaces.ChunkRepository
	chunk *types.Chunk
}

func applyTestChunkFields(chunk *types.Chunk, values map[string]interface{}) {
	if value, ok := values["content"].(string); ok {
		chunk.Content = value
	}
	if value, ok := values["source_content"].(string); ok {
		chunk.SourceContent = value
	}
	if value, ok := values["is_enabled"].(bool); ok {
		chunk.IsEnabled = value
	}
	if value, ok := values["index_status"].(string); ok {
		chunk.IndexStatus = value
	}
	if value, ok := values["updated_at"].(time.Time); ok {
		chunk.UpdatedAt = value
	}
}

func (r *editableChunkRepo) GetChunkByID(
	_ context.Context, _ uint64, _ string,
) (*types.Chunk, error) {
	copyOfChunk := *r.chunk
	return &copyOfChunk, nil
}

func (r *editableChunkRepo) SaveChunkRevision(
	_ context.Context, chunk *types.Chunk, _ *types.ChunkRevision, _ int,
) error {
	copyOfChunk := *chunk
	r.chunk = &copyOfChunk
	return nil
}

func (r *editableChunkRepo) ListChunkByParentID(
	_ context.Context, _ uint64, _ string,
) ([]*types.Chunk, error) {
	return nil, nil
}

func (r *editableChunkRepo) UpdateChunk(_ context.Context, chunk *types.Chunk) error {
	copyOfChunk := *chunk
	r.chunk = &copyOfChunk
	return nil
}

func (r *editableChunkRepo) UpdateChunkFieldsIfCurrent(
	_ context.Context, _ uint64, chunkID, _, _ string, expectedRevision int,
	values map[string]interface{},
) (bool, error) {
	if r.chunk == nil || r.chunk.ID != chunkID || r.chunk.ContentRevision != expectedRevision {
		return false, nil
	}
	applyTestChunkFields(r.chunk, values)
	return true, nil
}

type editableChunkKBRepo struct {
	interfaces.KnowledgeBaseRepository
}

func (editableChunkKBRepo) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{}, nil
}

type editableChunkKnowledgeRepo struct {
	interfaces.KnowledgeRepository
}

func (editableChunkKnowledgeRepo) GetKnowledgeByID(context.Context, uint64, string) (*types.Knowledge, error) {
	return &types.Knowledge{
		ID: "knowledge", KnowledgeBaseID: "kb", ParseStatus: types.ParseStatusCompleted,
	}, nil
}

type imageSyncChunkRepo struct {
	interfaces.ChunkRepository
	children []*types.Chunk
}

func (r *imageSyncChunkRepo) ListChunkByParentID(
	_ context.Context, _ uint64, _ string,
) ([]*types.Chunk, error) {
	return r.children, nil
}

func (r *imageSyncChunkRepo) UpdateChunk(_ context.Context, chunk *types.Chunk) error {
	for i := range r.children {
		if r.children[i].ID == chunk.ID {
			copyOfChunk := *chunk
			r.children[i] = &copyOfChunk
			return nil
		}
	}
	return nil
}

func (r *imageSyncChunkRepo) UpdateChunkFieldsIfCurrent(
	_ context.Context, _ uint64, chunkID, _, _ string, expectedRevision int,
	values map[string]interface{},
) (bool, error) {
	for _, child := range r.children {
		if child.ID == chunkID && child.ContentRevision == expectedRevision {
			applyTestChunkFields(child, values)
			return true, nil
		}
	}
	return false, nil
}

type parentRebuildChunkRepo struct {
	interfaces.ChunkRepository
	parent   *types.Chunk
	children []*types.Chunk
	updated  *types.Chunk
}

func TestValidateEditedChunkImages(t *testing.T) {
	source := "before\n![one](resource://one)\n![two](resource://two)\nafter"
	if err := validateEditedChunkImages(source, "before\n![two](resource://two)\nafter"); err != nil {
		t.Fatalf("deleting an original image should be allowed: %v", err)
	}
	if err := validateEditedChunkImages(source, source+"\n![new](resource://new)"); err == nil {
		t.Fatal("adding a new image should be rejected")
	}
}

func TestImageChildMatchesEditedContent(t *testing.T) {
	imageInfo, err := json.Marshal([]types.ImageInfo{{URL: "resource://one", OriginalURL: "original://one"}})
	if err != nil {
		t.Fatal(err)
	}
	child := &types.Chunk{ImageInfo: string(imageInfo)}
	if !imageChildMatchesContent(child, map[string]bool{"resource://one": true}) {
		t.Fatal("current image URL should match")
	}
	if !imageChildMatchesContent(child, map[string]bool{"original://one": true}) {
		t.Fatal("original image URL should match")
	}
	if imageChildMatchesContent(child, map[string]bool{"resource://other": true}) {
		t.Fatal("unrelated image URL should not match")
	}
}

func TestSyncEditedChunkImagesDisablesAndRestoresImageChildren(t *testing.T) {
	imageInfo, err := json.Marshal([]types.ImageInfo{{URL: "resource://one"}})
	if err != nil {
		t.Fatal(err)
	}
	repo := &imageSyncChunkRepo{children: []*types.Chunk{{
		ID: "image", TenantID: 1, KnowledgeID: "knowledge", KnowledgeBaseID: "kb", ParentChunkID: "text",
		ChunkType: types.ChunkTypeImageOCR, ImageInfo: string(imageInfo),
		IsEnabled: true, IndexStatus: "ready",
	}}}
	service := &chunkService{
		chunkRepository: repo, kbRepository: editableChunkKBRepo{}, knowledgeRepo: editableChunkKnowledgeRepo{},
	}
	parent := &types.Chunk{
		ID: "text", TenantID: 1, KnowledgeID: "knowledge", KnowledgeBaseID: "kb",
		IsEnabled: true, Content: "image removed",
	}

	if err := service.syncEditedChunkImages(context.Background(), parent); err != nil {
		t.Fatalf("disable removed image child: %v", err)
	}
	if repo.children[0].IsEnabled || repo.children[0].IndexStatus != "ready" {
		t.Fatalf("removed image child was not disabled cleanly: %+v", repo.children[0])
	}

	parent.Content = "image restored\n![one](resource://one)"
	if err := service.syncEditedChunkImages(context.Background(), parent); err != nil {
		t.Fatalf("restore image child: %v", err)
	}
	if !repo.children[0].IsEnabled || repo.children[0].IndexStatus != "ready" {
		t.Fatalf("restored image child was not re-enabled: %+v", repo.children[0])
	}
}

func TestUpdateDocumentChunkPreservesGeneratedQuestionsAcrossRevision(t *testing.T) {
	metadata := &types.DocumentChunkMetadata{
		GeneratedQuestions:         []types.GeneratedQuestion{{ID: "q1", Question: "old question"}},
		GeneratedQuestionsRevision: 0,
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	repo := &editableChunkRepo{chunk: &types.Chunk{
		ID: "chunk", TenantID: 1, KnowledgeID: "knowledge", KnowledgeBaseID: "kb",
		Content: "old body", SourceContent: "old body", ContentRevision: 0,
		ChunkType: types.ChunkTypeText, IsEnabled: true, IndexStatus: "ready", Metadata: metadataJSON,
	}}
	service := &chunkService{
		chunkRepository: repo,
		knowledgeRepo:   editableChunkKnowledgeRepo{},
		kbRepository:    editableChunkKBRepo{},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	newContent := "new body"

	updated, err := service.UpdateDocumentChunk(ctx, "chunk", &newContent, nil, nil)
	if err != nil {
		t.Fatalf("update chunk: %v", err)
	}
	updatedMetadata, err := updated.DocumentMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if len(updatedMetadata.GeneratedQuestions) != 1 || updatedMetadata.GeneratedQuestions[0].Question != "old question" {
		t.Fatalf("generated questions were cleared: %+v", updatedMetadata.GeneratedQuestions)
	}
	if updatedMetadata.IsQuestionCurrent(updatedMetadata.GeneratedQuestions[0], updated.ContentRevision) {
		t.Fatal("question should remain identifiable as based on the previous content revision")
	}
}

func (r *parentRebuildChunkRepo) GetChunkByID(
	_ context.Context, _ uint64, _ string,
) (*types.Chunk, error) {
	copyOfParent := *r.parent
	return &copyOfParent, nil
}

func (r *parentRebuildChunkRepo) ListChunkByParentID(
	_ context.Context, _ uint64, _ string,
) ([]*types.Chunk, error) {
	return r.children, nil
}

func (r *parentRebuildChunkRepo) UpdateChunk(_ context.Context, chunk *types.Chunk) error {
	copyOfChunk := *chunk
	r.updated = &copyOfChunk
	return nil
}

func (r *parentRebuildChunkRepo) UpdateChunkFieldsIfCurrent(
	_ context.Context, _ uint64, chunkID, _, _ string, expectedRevision int,
	values map[string]interface{},
) (bool, error) {
	if r.parent == nil || r.parent.ID != chunkID || r.parent.ContentRevision != expectedRevision {
		return false, nil
	}
	applyTestChunkFields(r.parent, values)
	copyOfChunk := *r.parent
	r.updated = &copyOfChunk
	return true, nil
}

func TestRebuildParentContentPreservesConflictingEdits(t *testing.T) {
	now := time.Now()
	repo := &parentRebuildChunkRepo{
		parent: &types.Chunk{
			ID: "parent", TenantID: 1, ChunkType: types.ChunkTypeParentText,
			SourceContent: "abcdefghij", Content: "abcdefghij", StartAt: 0, EndAt: 10,
		},
		children: []*types.Chunk{
			{
				ID: "older", ParentChunkID: "parent", ContentRevision: 1,
				Content: "OLDER EDIT BODY", StartAt: 0, EndAt: 6, UpdatedAt: now.Add(-time.Minute),
			},
			{
				ID: "newer", ParentChunkID: "parent", ContentRevision: 1,
				Content: "NEWER EDIT BODY", StartAt: 4, EndAt: 10, UpdatedAt: now,
			},
		},
	}
	service := &chunkService{chunkRepository: repo}
	edited := &types.Chunk{TenantID: 1, ParentChunkID: "parent"}

	if err := service.rebuildParentContent(context.Background(), edited); err != nil {
		t.Fatalf("rebuild parent: %v", err)
	}
	if repo.updated == nil {
		t.Fatal("parent was not updated")
	}
	for _, want := range []string{"OLDER EDIT BODY", "NEWER EDIT BODY"} {
		if !strings.Contains(repo.updated.Content, want) {
			t.Fatalf("rebuilt parent lost %q: %q", want, repo.updated.Content)
		}
	}
}
