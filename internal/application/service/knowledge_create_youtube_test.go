package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type youtubeRepoStub struct {
	interfaces.KnowledgeRepository
	created []*types.Knowledge
}

func (r *youtubeRepoStub) CheckKnowledgeExists(
	context.Context, uint64, string, *types.KnowledgeCheckParams,
) (bool, *types.Knowledge, error) {
	return false, nil, nil
}

func (r *youtubeRepoStub) CreateKnowledge(_ context.Context, k *types.Knowledge) error {
	r.created = append(r.created, k)
	return nil
}

func (r *youtubeRepoStub) GetKnowledgeTags(
	context.Context, []string,
) (map[string][]*types.KnowledgeTag, error) {
	return map[string][]*types.KnowledgeTag{}, nil
}

type youtubeKBServiceStub struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *youtubeKBServiceStub) GetKnowledgeBaseByID(
	context.Context, string,
) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type youtubeTaskStub struct{}

func (youtubeTaskStub) Enqueue(*asynq.Task, ...asynq.Option) (*asynq.TaskInfo, error) {
	return &asynq.TaskInfo{ID: "t"}, nil
}

// youtubeDocReaderStub answers the enumerate-engine Read call with a
// canned playlist/video/error response, keyed by URL.
type youtubeDocReaderStub struct {
	interfaces.DocumentReader
	byURL map[string]*types.ReadResult
	errAt map[string]error
}

func (d *youtubeDocReaderStub) Read(
	_ context.Context, req *types.ReadRequest,
) (*types.ReadResult, error) {
	if err, ok := d.errAt[req.URL]; ok {
		return nil, err
	}
	if result, ok := d.byURL[req.URL]; ok {
		return result, nil
	}
	return &types.ReadResult{Error: "unexpected url in test: " + req.URL}, nil
}

func newYoutubeTestService(reader interfaces.DocumentReader, repo interfaces.KnowledgeRepository) *knowledgeService {
	return &knowledgeService{
		config: &config.Config{},
		repo:   repo,
		kbService: &youtubeKBServiceStub{kb: &types.KnowledgeBase{
			ID:            "kb1",
			StorageConfig: types.StorageConfig{Provider: "local"},
		}},
		documentReader: reader,
		task:           youtubeTaskStub{},
	}
}

func TestCreateKnowledgeFromYoutubeSingleVideo(t *testing.T) {
	entries := `[{"video_id":"abc123","url":"https://www.youtube.com/watch?v=abc123","title":"My Video"}]`
	reader := &youtubeDocReaderStub{byURL: map[string]*types.ReadResult{
		"https://www.youtube.com/watch?v=abc123": {
			MarkdownContent: "ok",
			Metadata:        map[string]string{"youtube_kind": "video", "youtube_videos": entries},
		},
	}}
	repo := &youtubeRepoStub{}
	svc := newYoutubeTestService(reader, repo)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{})

	result, err := svc.CreateKnowledgeFromYoutube(
		ctx, "kb1", []string{"https://www.youtube.com/watch?v=abc123"}, nil, "",
	)
	require.NoError(t, err)
	require.Equal(t, 1, result.SuccessCount)
	require.Empty(t, result.Failed)
	require.Len(t, repo.created, 1)
	require.Equal(t, "My Video", repo.created[0].Title)
	require.Equal(t, "url", repo.created[0].Type)
}

func TestCreateKnowledgeFromYoutubePlaylistExpandsToMultipleVideos(t *testing.T) {
	playlistURL := "https://www.youtube.com/playlist?list=PL123"
	entries := `[
		{"video_id":"vid1","url":"https://www.youtube.com/watch?v=vid1","title":"First"},
		{"video_id":"vid2","url":"https://www.youtube.com/watch?v=vid2","title":"Second"}
	]`
	reader := &youtubeDocReaderStub{byURL: map[string]*types.ReadResult{
		playlistURL: {MarkdownContent: "ok", Metadata: map[string]string{
			"youtube_kind": "playlist", "youtube_videos": entries,
		}},
	}}
	repo := &youtubeRepoStub{}
	svc := newYoutubeTestService(reader, repo)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{})

	result, err := svc.CreateKnowledgeFromYoutube(ctx, "kb1", []string{playlistURL}, nil, "")
	require.NoError(t, err)
	require.Equal(t, 2, result.SuccessCount)
	require.Len(t, repo.created, 2)
}

func TestCreateKnowledgeFromYoutubeContinuesAfterOneFailure(t *testing.T) {
	goodURL := "https://www.youtube.com/watch?v=good"
	badURL := "https://www.youtube.com/watch?v=bad"
	goodEntries := `[{"video_id":"good","url":"https://www.youtube.com/watch?v=good","title":"Good"}]`
	reader := &youtubeDocReaderStub{
		byURL: map[string]*types.ReadResult{
			goodURL: {MarkdownContent: "ok", Metadata: map[string]string{
				"youtube_kind": "video", "youtube_videos": goodEntries,
			}},
		},
		errAt: map[string]error{badURL: assertErr("expansion failed")},
	}
	repo := &youtubeRepoStub{}
	svc := newYoutubeTestService(reader, repo)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{})

	result, err := svc.CreateKnowledgeFromYoutube(ctx, "kb1", []string{badURL, goodURL}, nil, "")
	require.NoError(t, err)
	require.Equal(t, 1, result.SuccessCount)
	require.Len(t, result.Failed, 1)
	require.Equal(t, badURL, result.Failed[0].URL)
	require.Len(t, repo.created, 1)
}

func TestCreateKnowledgeFromYoutubeRejectsUnsafeURL(t *testing.T) {
	svc := newYoutubeTestService(&youtubeDocReaderStub{byURL: map[string]*types.ReadResult{}}, &youtubeRepoStub{})

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	result, err := svc.CreateKnowledgeFromYoutube(
		ctx, "kb1", []string{"http://127.0.0.1/watch?v=abc"}, nil, "",
	)
	require.NoError(t, err)
	require.Empty(t, result.Knowledge)
	require.Len(t, result.Failed, 1)
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

// youtubeRepoWithExistingStub reports one URL as already existing in the
// knowledge base (as CheckKnowledgeExists would for a video ingested by an
// earlier run), so CreateKnowledgeFromURL returns a DuplicateKnowledgeError
// for it.
type youtubeRepoWithExistingStub struct {
	youtubeRepoStub
	existingURL       string
	existingKnowledge *types.Knowledge
}

func (r *youtubeRepoWithExistingStub) CheckKnowledgeExists(
	_ context.Context, _ uint64, _ string, params *types.KnowledgeCheckParams,
) (bool, *types.Knowledge, error) {
	if params.URL == r.existingURL {
		return true, r.existingKnowledge, nil
	}
	return false, nil, nil
}

func (r *youtubeRepoWithExistingStub) UpdateKnowledge(context.Context, *types.Knowledge) error {
	return nil
}

func TestCreateKnowledgeFromYoutubeTreatsExistingURLAsSuccess(t *testing.T) {
	existingURL := "https://www.youtube.com/watch?v=existing"
	playlistURL := "https://www.youtube.com/playlist?list=PL999"
	entries := `[
		{"video_id":"existing","url":"https://www.youtube.com/watch?v=existing","title":"Existing"},
		{"video_id":"newvid","url":"https://www.youtube.com/watch?v=newvid","title":"New"}
	]`
	reader := &youtubeDocReaderStub{byURL: map[string]*types.ReadResult{
		playlistURL: {MarkdownContent: "ok", Metadata: map[string]string{
			"youtube_kind": "playlist", "youtube_videos": entries,
		}},
	}}
	existingKnowledge := &types.Knowledge{ID: "existing-kid", Title: "Existing", Source: existingURL}
	repo := &youtubeRepoWithExistingStub{existingURL: existingURL, existingKnowledge: existingKnowledge}
	svc := newYoutubeTestService(reader, repo)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{})

	result, err := svc.CreateKnowledgeFromYoutube(ctx, "kb1", []string{playlistURL}, nil, "")
	require.NoError(t, err)

	// Both the duplicate and the new video count as successes; the
	// duplicate must NOT be recorded as a failure.
	require.Equal(t, 2, result.SuccessCount)
	require.Empty(t, result.Failed)
	require.Len(t, result.Knowledge, 2)

	// Only the genuinely new video should have gone through CreateKnowledge.
	require.Len(t, repo.created, 1)
	require.Equal(t, "New", repo.created[0].Title)

	// The duplicate's existing knowledge record must be the one reported back.
	found := false
	for _, k := range result.Knowledge {
		if k.ID == "existing-kid" {
			found = true
		}
	}
	require.True(t, found, "expected existing knowledge to be included in result.Knowledge")
}
