package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newModelRecorderTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&evaluationModelCallRecord{}, &embeddingCacheUsageRecord{}))
	return db
}

func TestModelCallRecorderFlushesTenantMetadataOnStop(t *testing.T) {
	t.Setenv("WEKNORA_MODEL_CALL_FINGERPRINT_KEY", "test-secret")
	db := newModelRecorderTestDB(t)
	enabled := true
	recorder := NewModelCallRecorder(&config.Config{ModelObservability: &config.ModelObservabilityConfig{
		Enabled: &enabled, RetentionDays: 30, QueueSize: 8, BatchSize: 8,
	}}, db)
	recorder.Start(t.Context())
	recorder.ObserveLLMCall(types.LLMCallObservation{
		TenantID: 7, ModelID: "m-1", ModelName: "test-model", Purpose: "wiki_page_modify",
		PromptPrefixFingerprint: "raw-prefix", RequestFingerprint: "raw-request",
		Usage:   types.TokenUsage{PromptTokens: 10, TotalTokens: 10},
		Success: true,
	})
	recorder.ObserveEmbeddingCache(types.EmbeddingCacheObservation{
		TenantID: 7, ModelID: "embedding-1", ModelName: "test-embedding",
		LookupCount: 4, HitCount: 2, MissCount: 1, DeduplicatedCount: 1,
		AvoidedComputations: 3,
	})
	recorder.Stop()

	var records []evaluationModelCallRecord
	require.NoError(t, db.Find(&records).Error)
	require.Len(t, records, 1)
	require.Nil(t, records[0].TaskID)
	require.Equal(t, uint64(7), records[0].TenantID)
	require.Equal(t, "wiki_page_modify", records[0].Purpose)
	require.NotEqual(t, "raw-prefix", records[0].PromptPrefixFingerprint)
	mac := hmac.New(sha256.New, []byte("test-secret"))
	_, _ = mac.Write([]byte("raw-prefix"))
	require.Equal(t, hex.EncodeToString(mac.Sum(nil)), records[0].PromptPrefixFingerprint)
	mac.Reset()
	_, _ = mac.Write([]byte("raw-request"))
	require.Equal(t, hex.EncodeToString(mac.Sum(nil)), records[0].RequestFingerprint)

	var cacheRecords []embeddingCacheUsageRecord
	require.NoError(t, db.Find(&cacheRecords).Error)
	require.Len(t, cacheRecords, 1)
	require.Equal(t, uint64(7), cacheRecords[0].TenantID)
	require.Equal(t, "embedding-1", cacheRecords[0].ModelID)
	require.Equal(t, 4, cacheRecords[0].LookupCount)
	require.Equal(t, 2, cacheRecords[0].HitCount)
	require.Equal(t, 1, cacheRecords[0].MissCount)
	require.Equal(t, 1, cacheRecords[0].DeduplicatedCount)
	require.Equal(t, 3, cacheRecords[0].AvoidedComputations)
}

func TestModelCallRecorderDoesNotPersistUnscopedCallsOrRawFingerprint(t *testing.T) {
	t.Setenv("WEKNORA_MODEL_CALL_FINGERPRINT_KEY", "")
	db := newModelRecorderTestDB(t)
	recorder := NewModelCallRecorder(nil, db)
	recorder.Start(t.Context())
	recorder.ObserveLLMCall(types.LLMCallObservation{TenantID: 0, ModelName: "ignored", Success: true})
	recorder.ObserveLLMCall(types.LLMCallObservation{
		TenantID: 9, ModelName: "kept", PromptPrefixFingerprint: "must-not-be-stored",
		RequestFingerprint: "request-must-not-be-stored",
		Success:            false, Error: "request excerpt must-not-be-stored",
	})
	recorder.Stop()

	var records []evaluationModelCallRecord
	require.NoError(t, db.Find(&records).Error)
	require.Len(t, records, 1)
	require.Equal(t, "", records[0].PromptPrefixFingerprint)
	require.Equal(t, "", records[0].RequestFingerprint)
	require.Equal(t, "", records[0].ErrMsg)
}

func TestModelCallRecorderPurgesExpiredRecords(t *testing.T) {
	db := newModelRecorderTestDB(t)
	recorder := NewModelCallRecorder(nil, db)
	old := newModelCallRecord(nil, 1, types.LLMCallObservation{ModelName: "old", Success: true}, time.Now().AddDate(0, 0, -31))
	recent := newModelCallRecord(nil, 1, types.LLMCallObservation{ModelName: "recent", Success: true}, time.Now())
	require.NoError(t, db.Create([]*evaluationModelCallRecord{old, recent}).Error)
	require.NoError(t, db.Create([]*embeddingCacheUsageRecord{
		{ID: "old-cache", TenantID: 1, ModelName: "old-cache", LookupCount: 1, CreatedAt: time.Now().AddDate(0, 0, -31)},
		{ID: "recent-cache", TenantID: 1, ModelName: "recent-cache", LookupCount: 1, CreatedAt: time.Now()},
	}).Error)

	recorder.purgeExpired()

	var records []evaluationModelCallRecord
	require.NoError(t, db.Order("created_at").Find(&records).Error)
	require.Len(t, records, 1)
	require.Equal(t, "recent", records[0].ModelName)

	var cacheRecords []embeddingCacheUsageRecord
	require.NoError(t, db.Order("created_at").Find(&cacheRecords).Error)
	require.Len(t, cacheRecords, 1)
	require.Equal(t, "recent-cache", cacheRecords[0].ModelName)
}
