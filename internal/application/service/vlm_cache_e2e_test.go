package service_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/stretchr/testify/assert"
)

type MockVLMModel struct {
	modelID      string
	predictTimes atomic.Int32
}

func (m *MockVLMModel) ID() string {
	return m.modelID
}

func (m *MockVLMModel) GetCallCount() int32 {
	return m.predictTimes.Load()
}

// Fix 1: Match the actual method signature with parameters
func (m *MockVLMModel) Predict(ctx context.Context, images [][]byte, prompt string) (string, error) {
	m.predictTimes.Add(1)
	time.Sleep(10 * time.Millisecond) // Use smaller sleep for faster tests
	return "mock model result", nil
}

func TestVLMCache_E2E_Reparse_Saving_Money(t *testing.T) {
	// 1. Initialization of memory database
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	assert.NoError(t, err)

	// Create table manually with SQLite-compatible schema
	err = db.Exec(`CREATE TABLE IF NOT EXISTS vlm_cache (
		image_hash     TEXT NOT NULL,
		vlm_model_id   TEXT NOT NULL,
		prompt_version TEXT NOT NULL,
		result_text    TEXT NOT NULL,
		created_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_accessed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (image_hash, vlm_model_id, prompt_version)
	)`).Error
	assert.NoError(t, err)

	cacheRepo := repository.NewVLMCacheRepo(db)
	ctx := context.Background()
	mockImageBytes := []byte("fake_image_content_12345")
	imgHash := "fake_hash_abc123"

	// ====================================================
	// Scenario 1: First Ingest
	// ====================================================
	modelV1 := &MockVLMModel{modelID: "gpt-4-vision-v1"}
	_, hit, err := cacheRepo.GetOrCompute(ctx, interfaces.VLMCacheKey{
		ImageHash:     imgHash,
		ModelID:       modelV1.ID(),
		PromptVersion: "ocr.default.v1",
	}, func() (string, error) {
		// Fix 3: Pass required arguments
		return modelV1.Predict(ctx, [][]byte{mockImageBytes}, "prompt")
	})

	assert.NoError(t, err)
	assert.False(t, hit, "first execution must be a cache miss")
	assert.Equal(t, int32(1), modelV1.GetCallCount(), "model must be called exactly once")

	// ====================================================
	// Scenario 2: Second Ingest (Reparse with unchanged content)
	// ====================================================
	_, hit2, err2 := cacheRepo.GetOrCompute(ctx, interfaces.VLMCacheKey{
		ImageHash:     imgHash,
		ModelID:       modelV1.ID(),
		PromptVersion: "ocr.default.v1",
	}, func() (string, error) {
		return modelV1.Predict(ctx, [][]byte{mockImageBytes}, "prompt")
	})

	assert.NoError(t, err2)
	assert.True(t, hit2, "second execution must perfectly hit the cache")
	assert.Equal(t, int32(1), modelV1.GetCallCount(), "core assertion: model call count must stay at 1, no extra cost!")

	// ====================================================
	// Scenario 3: Model Change Reparse
	// ====================================================
	modelV2 := &MockVLMModel{modelID: "qwen-vl-plus-v2"}
	_, hit3, err3 := cacheRepo.GetOrCompute(ctx, interfaces.VLMCacheKey{
		ImageHash:     imgHash,
		ModelID:       modelV2.ID(),
		PromptVersion: "ocr.default.v1",
	}, func() (string, error) {
		// Fix 4: Pass required arguments consistently
		return modelV2.Predict(ctx, [][]byte{mockImageBytes}, "prompt")
	})

	assert.NoError(t, err3)
	assert.False(t, hit3, "must trigger a cache miss due to model change")
	assert.Equal(t, int32(1), modelV2.GetCallCount(), "new model V2 must be called exactly once")
	assert.Equal(t, int32(1), modelV1.GetCallCount(), "old model V1 call count must remain 1")
}
