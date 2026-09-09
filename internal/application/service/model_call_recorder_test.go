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
	require.NoError(t, db.AutoMigrate(&evaluationModelCallRecord{}))
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
		PromptPrefixFingerprint: "raw-prefix", Usage: types.TokenUsage{PromptTokens: 10, TotalTokens: 10},
		Success: true,
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
}

func TestModelCallRecorderDoesNotPersistUnscopedCallsOrRawFingerprint(t *testing.T) {
	t.Setenv("WEKNORA_MODEL_CALL_FINGERPRINT_KEY", "")
	db := newModelRecorderTestDB(t)
	recorder := NewModelCallRecorder(nil, db)
	recorder.Start(t.Context())
	recorder.ObserveLLMCall(types.LLMCallObservation{TenantID: 0, ModelName: "ignored", Success: true})
	recorder.ObserveLLMCall(types.LLMCallObservation{
		TenantID: 9, ModelName: "kept", PromptPrefixFingerprint: "must-not-be-stored",
		Success: false, Error: "request excerpt must-not-be-stored",
	})
	recorder.Stop()

	var records []evaluationModelCallRecord
	require.NoError(t, db.Find(&records).Error)
	require.Len(t, records, 1)
	require.Equal(t, "", records[0].PromptPrefixFingerprint)
	require.Equal(t, "", records[0].ErrMsg)
}

func TestModelCallRecorderPurgesExpiredRecords(t *testing.T) {
	db := newModelRecorderTestDB(t)
	recorder := NewModelCallRecorder(nil, db)
	old := newModelCallRecord(nil, 1, types.LLMCallObservation{ModelName: "old", Success: true}, time.Now().AddDate(0, 0, -31))
	recent := newModelCallRecord(nil, 1, types.LLMCallObservation{ModelName: "recent", Success: true}, time.Now())
	require.NoError(t, db.Create([]*evaluationModelCallRecord{old, recent}).Error)

	recorder.purgeExpired()

	var records []evaluationModelCallRecord
	require.NoError(t, db.Order("created_at").Find(&records).Error)
	require.Len(t, records, 1)
	require.Equal(t, "recent", records[0].ModelName)
}
