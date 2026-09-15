package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// memoryExportStore holds more accounts than one export page, so the walk is
// exercised rather than only the first read.
type memoryExportStore struct {
	interfaces.MemoryService
	episodes []*types.MemoryEpisode
	notes    []*types.MemoryNote
	profile  *types.MemoryDigest
}

func (s *memoryExportStore) Profile(context.Context) (*types.MemoryDigest, error) {
	return s.profile, nil
}

func (s *memoryExportStore) ListEpisodes(
	_ context.Context, limit, offset int,
) ([]*types.MemoryEpisode, int64, error) {
	total := int64(len(s.episodes))
	if offset >= len(s.episodes) {
		return nil, total, nil
	}
	end := offset + limit
	if end > len(s.episodes) {
		end = len(s.episodes)
	}
	return s.episodes[offset:end], total, nil
}

func (s *memoryExportStore) ListNotes(context.Context, int) ([]*types.MemoryNote, error) {
	return s.notes, nil
}

// An export is what a person takes with them, so it has to contain everything
// the product remembers about them rather than the one store the screen they
// were looking at happened to show.
func TestAnExportCarriesAllThreeStores(t *testing.T) {
	store := &memoryExportStore{
		profile: &types.MemoryDigest{Body: "## 用户画像\n- 在做医学影像的后端\n", Revision: 3},
		notes:   []*types.MemoryNote{{ID: "n1", Content: "回答请用中文"}},
	}
	for i := 0; i < memoryExportPageSize+7; i++ {
		store.episodes = append(store.episodes, &types.MemoryEpisode{
			ID:      fmt.Sprintf("e%d", i),
			Title:   fmt.Sprintf("第 %d 段对话", i),
			Summary: fmt.Sprintf("第 %d 段对话的记述。", i),
			ToAt:    time.Now(),
		})
	}

	router := gin.New()
	router.GET("/memory/export", NewMemoryHandler(store).Export)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/memory/export", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t,
		`attachment; filename="weknora-memories.json"`,
		recorder.Header().Get("Content-Disposition"))

	var payload struct {
		Success   bool  `json:"success"`
		Total     int64 `json:"total"`
		Truncated bool  `json:"truncated"`
		Data      struct {
			Profile  *types.MemoryDigest    `json:"profile"`
			Episodes []*types.MemoryEpisode `json:"episodes"`
			Notes    []*types.MemoryNote    `json:"notes"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	require.False(t, payload.Truncated)
	require.Len(t, payload.Data.Episodes, len(store.episodes),
		"an export that stopped at the first page would look complete")
	require.Len(t, payload.Data.Notes, 1)
	require.NotNil(t, payload.Data.Profile)
	require.Contains(t, payload.Data.Profile.Body, "医学影像")
	require.Equal(t, int64(len(store.episodes)+2), payload.Total,
		"the total covers every stored record, profile included")
}

func TestAnExportWithNoProfileStillReportsTheOtherStores(t *testing.T) {
	store := &memoryExportStore{
		notes: []*types.MemoryNote{{ID: "n1", Content: "回答请用中文"}},
	}
	router := gin.New()
	router.GET("/memory/export", NewMemoryHandler(store).Export)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/memory/export", nil))
	require.Equal(t, http.StatusOK, recorder.Code)

	var payload struct {
		Total int64 `json:"total"`
		Data  struct {
			Profile *types.MemoryDigest `json:"profile"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Nil(t, payload.Data.Profile, "a subject with no profile yet exports a null one")
	require.Equal(t, int64(1), payload.Total)
}
