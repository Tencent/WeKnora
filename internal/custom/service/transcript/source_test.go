package transcript

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

type sourceGateway struct {
	created   []weknora.ManualKnowledgeInput
	kbIDs     []string
	items     map[string]weknora.ManualKnowledgeResult
	findErr   error
	createErr error
	getErr    error
}

func (g *sourceGateway) FindManualKnowledgeByTitle(_ context.Context, kbID string, title string) (*weknora.ManualKnowledgeResult, error) {
	g.kbIDs = append(g.kbIDs, kbID)
	if g.findErr != nil {
		return nil, g.findErr
	}
	for _, item := range g.items {
		if item.Title == title {
			value := item
			return &value, nil
		}
	}
	return nil, nil
}

func (g *sourceGateway) CreateManualKnowledge(_ context.Context, kbID string, input weknora.ManualKnowledgeInput) (weknora.ManualKnowledgeResult, error) {
	if g.createErr != nil {
		return weknora.ManualKnowledgeResult{}, g.createErr
	}
	g.created = append(g.created, input)
	g.kbIDs = append(g.kbIDs, kbID)
	value := weknora.ManualKnowledgeResult{ID: uuid.NewString(), KnowledgeBaseID: kbID, Title: input.Title, ParseStatus: "completed"}
	if g.items == nil {
		g.items = make(map[string]weknora.ManualKnowledgeResult)
	}
	g.items[value.ID] = value
	return value, nil
}

func (g *sourceGateway) GetKnowledge(_ context.Context, id string) (weknora.ManualKnowledgeResult, error) {
	if g.getErr != nil {
		return weknora.ManualKnowledgeResult{}, g.getErr
	}
	value, ok := g.items[id]
	if !ok {
		return weknora.ManualKnowledgeResult{ID: id, ParseStatus: "completed"}, nil
	}
	return value, nil
}

func openSourceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.VideoTranscriptSource{}))
	return db
}

func sourceTestDocument(t *testing.T, generation, text string) FullVideoDocument {
	t.Helper()
	doc, err := Build(Input{
		VideoID: "video-1", TranscriptGeneration: generation, Title: "测试视频", DurationSeconds: 30,
		Chapters: []InputChapter{{Index: 0, Title: "测试视频", Paragraphs: []InputParagraph{{
			Index: 0, SpeakerID: "speaker-1", Sentences: []InputSentence{{
				SourceSentenceID: "sentence-1", EvidenceSentenceID: "evidence-" + generation,
				Text: text, SpeakerID: "speaker-1", StartMs: 100, EndMs: 1000,
			}},
		}}}},
	})
	require.NoError(t, err)
	return doc
}

func TestSourceWriterReusesSameGeneration(t *testing.T) {
	db := openSourceTestDB(t)
	gateway := &sourceGateway{items: make(map[string]weknora.ManualKnowledgeResult)}
	writer := &SourceWriter{DB: db, Gateway: gateway, KBID: "kb-1"}
	doc := sourceTestDocument(t, "generation-1", "第一句")

	first, err := writer.Ensure(context.Background(), SourceInput{Document: doc})
	require.NoError(t, err)
	require.Equal(t, "created", first.Action)
	require.Len(t, gateway.created, 1)
	second, err := writer.Ensure(context.Background(), SourceInput{Document: doc})
	require.NoError(t, err)
	require.Equal(t, "reused", second.Action)
	require.Equal(t, first.KnowledgeID, second.KnowledgeID)
	require.Len(t, gateway.created, 1)

	var bindings []model.VideoTranscriptSource
	require.NoError(t, db.Find(&bindings).Error)
	require.Len(t, bindings, 1)
	require.Equal(t, SourceStatusCreated, bindings[0].Status)
	require.Equal(t, "kb-1", bindings[0].KnowledgeBaseID)
	require.NotEmpty(t, gateway.kbIDs)
	for _, kbID := range gateway.kbIDs {
		require.Equal(t, "kb-1", kbID)
	}
}

func TestSourceWriterDoesNotReuseLegacyBindingWithoutKnowledgeBase(t *testing.T) {
	db := openSourceTestDB(t)
	doc := sourceTestDocument(t, "generation-1", "第一句")
	documentJSON, err := doc.JSON()
	require.NoError(t, err)
	legacyHash := fmt.Sprintf("%x", sha256.Sum256([]byte(documentJSON)))
	require.NoError(t, db.Create(&model.VideoTranscriptSource{
		ID: "legacy-binding", VideoID: doc.VideoID, TranscriptGeneration: doc.TranscriptGeneration,
		KnowledgeID: "legacy-knowledge", ContentHash: legacyHash, Status: SourceStatusCreated,
	}).Error)

	gateway := &sourceGateway{items: make(map[string]weknora.ManualKnowledgeResult)}
	writer := &SourceWriter{DB: db, Gateway: gateway, KBID: "knowledge-kb"}
	result, err := writer.Ensure(context.Background(), SourceInput{Document: doc})
	require.NoError(t, err)
	require.Equal(t, "created", result.Action)
	require.NotEqual(t, "legacy-knowledge", result.KnowledgeID)

	var bindings []model.VideoTranscriptSource
	require.NoError(t, db.Find(&bindings).Error)
	require.Len(t, bindings, 2)
	var legacyCount, knowledgeCount int
	for _, binding := range bindings {
		if binding.KnowledgeBaseID == "" {
			legacyCount++
		}
		if binding.KnowledgeBaseID == "knowledge-kb" {
			knowledgeCount++
		}
	}
	require.Equal(t, 1, legacyCount)
	require.Equal(t, 1, knowledgeCount)
}

func TestSourceWriterRejectsRemoteOwnershipMismatch(t *testing.T) {
	db := openSourceTestDB(t)
	gateway := &sourceGateway{items: map[string]weknora.ManualKnowledgeResult{
		"wrong": {ID: "wrong", KnowledgeBaseID: "evidence-kb", Title: SourceTitle("video-1", "generation-1"), ParseStatus: "completed"},
	}}
	writer := &SourceWriter{DB: db, Gateway: gateway, KBID: "knowledge-kb"}
	_, err := writer.Ensure(context.Background(), SourceInput{Document: sourceTestDocument(t, "generation-1", "第一句")})
	require.Error(t, err)
	require.Contains(t, err.Error(), SourceOwnershipMismatch)
}

func TestSourceWriterSeparatesGenerationsAndRejectsHashChange(t *testing.T) {
	db := openSourceTestDB(t)
	gateway := &sourceGateway{items: make(map[string]weknora.ManualKnowledgeResult)}
	writer := &SourceWriter{DB: db, Gateway: gateway, KBID: "kb-1"}
	first, err := writer.Ensure(context.Background(), SourceInput{Document: sourceTestDocument(t, "generation-1", "第一句")})
	require.NoError(t, err)
	second, err := writer.Ensure(context.Background(), SourceInput{Document: sourceTestDocument(t, "generation-2", "第二句")})
	require.NoError(t, err)
	require.NotEqual(t, first.KnowledgeID, second.KnowledgeID)
	require.Len(t, gateway.created, 2)

	_, err = writer.Ensure(context.Background(), SourceInput{Document: sourceTestDocument(t, "generation-1", "被改写")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "content hash mismatch")
	require.Len(t, gateway.created, 2)
}

func TestSourceWriterRecordsFailureWithoutBindingSuccess(t *testing.T) {
	db := openSourceTestDB(t)
	gateway := &sourceGateway{items: make(map[string]weknora.ManualKnowledgeResult), createErr: gorm.ErrInvalidData}
	writer := &SourceWriter{DB: db, Gateway: gateway, KBID: "kb-1"}

	_, err := writer.Ensure(context.Background(), SourceInput{Document: sourceTestDocument(t, "generation-1", "第一句")})
	require.Error(t, err)
	var binding model.VideoTranscriptSource
	require.NoError(t, db.First(&binding).Error)
	require.Equal(t, SourceStatusFailed, binding.Status)
	require.Empty(t, binding.KnowledgeID)
	require.NotEmpty(t, binding.ErrorMessage)
}

func TestSourceWriterRetryRecoversSameBindingWithoutDuplicate(t *testing.T) {
	db := openSourceTestDB(t)
	gateway := &sourceGateway{items: make(map[string]weknora.ManualKnowledgeResult), createErr: gorm.ErrInvalidData}
	writer := &SourceWriter{DB: db, Gateway: gateway, KBID: "kb-1"}
	doc := sourceTestDocument(t, "generation-1", "第一句")

	_, err := writer.Ensure(context.Background(), SourceInput{Document: doc})
	require.Error(t, err)

	var failed model.VideoTranscriptSource
	require.NoError(t, db.First(&failed).Error)
	require.Equal(t, SourceStatusFailed, failed.Status)
	require.Empty(t, failed.KnowledgeID)
	require.Empty(t, gateway.created)

	gateway.createErr = nil
	result, err := writer.Ensure(context.Background(), SourceInput{Document: doc})
	require.NoError(t, err)
	require.Equal(t, "created", result.Action)
	require.NotEmpty(t, result.KnowledgeID)
	require.Len(t, gateway.created, 1)

	var bindings []model.VideoTranscriptSource
	require.NoError(t, db.Find(&bindings).Error)
	require.Len(t, bindings, 1)
	require.Equal(t, failed.ID, bindings[0].ID)
	require.Equal(t, SourceStatusCreated, bindings[0].Status)
	require.Equal(t, result.KnowledgeID, bindings[0].KnowledgeID)
	require.Empty(t, bindings[0].ErrorMessage)
}
