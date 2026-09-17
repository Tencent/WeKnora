package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/infrastructure/youtube"
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

// allowYouTubeHostsForTest whitelists the canonical YouTube host so URL
// import tests do not depend on DNS resolution. It mutates process-global
// SSRF state, so callers must not run in parallel.
func allowYouTubeHostsForTest(t *testing.T) {
	t.Helper()
	secutils.SetSSRFWhitelistFromRaw("www.youtube.com")
	t.Cleanup(func() { secutils.SetSSRFWhitelistFromRaw("") })
}

type fakeYouTubeSource struct {
	playlists     map[string]*youtube.Playlist
	playlistErrs  map[string]error
	maxVideos     int
	transcript    *youtube.Transcript
	transcriptErr error

	playlistCalls  []string
	gotTranscriber youtube.Transcriber
}

func (f *fakeYouTubeSource) Playlist(_ context.Context, playlistID string, _ int) (*youtube.Playlist, error) {
	f.playlistCalls = append(f.playlistCalls, playlistID)
	if err := f.playlistErrs[playlistID]; err != nil {
		return nil, err
	}
	if playlist, ok := f.playlists[playlistID]; ok {
		return playlist, nil
	}
	return &youtube.Playlist{ID: playlistID}, nil
}

func (f *fakeYouTubeSource) FetchTranscript(
	_ context.Context, _ string, transcribe youtube.Transcriber,
) (*youtube.Transcript, error) {
	f.gotTranscriber = transcribe
	return f.transcript, f.transcriptErr
}

func (f *fakeYouTubeSource) MaxVideosPerImport() int {
	if f.maxVideos > 0 {
		return f.maxVideos
	}
	return 200
}

// youTubeRepoStub records created knowledge and reports existing URLs as duplicates.
type youTubeRepoStub struct {
	createKnowledgeFileRepoStub

	existingURLs map[string]*types.Knowledge
	created      []*types.Knowledge
	updated      []*types.Knowledge
}

func (r *youTubeRepoStub) CheckKnowledgeExists(
	_ context.Context, _ uint64, _ string, params *types.KnowledgeCheckParams,
) (bool, *types.Knowledge, error) {
	if existing, ok := r.existingURLs[params.URL]; ok {
		return true, existing, nil
	}
	return false, nil, nil
}

func (r *youTubeRepoStub) CreateKnowledge(_ context.Context, knowledge *types.Knowledge) error {
	copied := *knowledge
	r.created = append(r.created, &copied)
	return nil
}

func (r *youTubeRepoStub) UpdateKnowledge(_ context.Context, knowledge *types.Knowledge) error {
	copied := *knowledge
	r.updated = append(r.updated, &copied)
	return nil
}

func requireAppErrorStatus(t *testing.T, err error, status int) {
	t.Helper()
	appErr, ok := werrors.IsAppError(err)
	require.True(t, ok, "expected AppError, got %T: %v", err, err)
	require.Equal(t, status, appErr.HTTPCode)
}

func TestNormalizeYouTubeImportURL(t *testing.T) {
	t.Parallel()

	got, isYouTube, err := normalizeYouTubeImportURL("https://youtu.be/jNQXAC9IVRw?si=share")
	require.NoError(t, err)
	require.True(t, isYouTube)
	require.Equal(t, "https://www.youtube.com/watch?v=jNQXAC9IVRw", got)

	got, isYouTube, err = normalizeYouTubeImportURL("https://example.com/page")
	require.NoError(t, err)
	require.False(t, isYouTube)
	require.Equal(t, "https://example.com/page", got)

	_, _, err = normalizeYouTubeImportURL("https://www.youtube.com/playlist?list=PL123456")
	requireAppErrorStatus(t, err, http.StatusBadRequest)
}

func TestCreateKnowledgeFromURLStoresCanonicalYouTubeURL(t *testing.T) {
	allowYouTubeHostsForTest(t)

	repo := &youTubeRepoStub{}
	task := &createKnowledgeTaskEnqueuerStub{}
	svc := &knowledgeService{
		repo:      repo,
		kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1"}},
		fileSvc:   &createKnowledgeFileServiceStub{},
		task:      task,
	}

	// A file_type hint must not turn a YouTube link into a file download.
	knowledge, err := svc.CreateKnowledgeFromURL(newCreateKnowledgeFileContext(), "kb-1",
		"https://youtu.be/jNQXAC9IVRw?si=share", "", "mp4", nil, "", nil, "", nil)

	require.NoError(t, err)
	require.NotNil(t, knowledge)
	require.Len(t, repo.created, 1)
	require.Equal(t, "url", repo.created[0].Type)
	require.Equal(t, "https://www.youtube.com/watch?v=jNQXAC9IVRw", repo.created[0].Source)
	require.Equal(t, 1, task.calls)
}

func TestCreateKnowledgeFromURLRejectsYouTubePlaylist(t *testing.T) {
	t.Parallel()

	repo := &youTubeRepoStub{}
	svc := &knowledgeService{repo: repo}
	_, err := svc.CreateKnowledgeFromURL(newCreateKnowledgeFileContext(), "kb-1",
		"https://www.youtube.com/playlist?list=PL123456", "", "", nil, "", nil, "", nil)

	requireAppErrorStatus(t, err, http.StatusBadRequest)
	require.Empty(t, repo.created)
}

func newYouTubeImportService(repo *youTubeRepoStub, task *createKnowledgeTaskEnqueuerStub,
	source *fakeYouTubeSource,
) *knowledgeService {
	return &knowledgeService{
		repo:          repo,
		kbService:     &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1"}},
		fileSvc:       &createKnowledgeFileServiceStub{},
		task:          task,
		youtubeSource: source,
	}
}

func TestCreateKnowledgeFromYouTubeBatchMixesVideosAndPlaylists(t *testing.T) {
	allowYouTubeHostsForTest(t)

	existing := &types.Knowledge{ID: "existing-c", Source: "https://www.youtube.com/watch?v=ccccccccccc"}
	repo := &youTubeRepoStub{existingURLs: map[string]*types.Knowledge{existing.Source: existing}}
	task := &createKnowledgeTaskEnqueuerStub{}
	source := &fakeYouTubeSource{
		playlists: map[string]*youtube.Playlist{
			"PL111111": {ID: "PL111111", Title: "Course", Entries: []youtube.PlaylistEntry{
				{VideoID: "aaaaaaaaaaa", Title: "Lesson A"},
				{VideoID: "bbbbbbbbbbb", Title: "Lesson B"},
				{VideoID: "ccccccccccc", Title: "Lesson C"},
			}},
		},
		playlistErrs: map[string]error{"PL222222": errors.New("ERROR: The playlist does not exist")},
	}
	svc := newYouTubeImportService(repo, task, source)

	result, err := svc.CreateKnowledgeFromYouTube(newCreateKnowledgeFileContext(), "kb-1", []string{
		"https://youtu.be/aaaaaaaaaaa",
		"https://www.youtube.com/playlist?list=PL111111",
		"https://www.youtube.com/watch?v=bbbbbbbbbbb&t=10s",
		"  ",
		"https://example.com/not-youtube",
		"https://www.youtube.com/playlist?list=PL222222",
	}, nil, nil, "", nil, "")

	require.NoError(t, err)
	require.Equal(t, 3, result.TotalVideos, "a, b and c once each")
	require.False(t, result.Truncated)
	require.Equal(t, []string{"PL111111", "PL222222"}, source.playlistCalls)
	require.Equal(t, []types.YouTubeImportPlaylist{{
		URL: "https://www.youtube.com/playlist?list=PL111111", PlaylistID: "PL111111", Title: "Course",
		FolderPath: "Course", Videos: 2,
	}}, result.Playlists)
	require.Empty(t, repo.created[0].FolderPath, "single videos stay in the requested folder (root)")
	require.Equal(t, "Course", repo.created[1].FolderPath, "playlist videos go into the playlist folder")

	require.Len(t, result.Created, 2)
	require.Len(t, repo.created, 2)
	require.Equal(t, "https://www.youtube.com/watch?v=aaaaaaaaaaa", repo.created[0].Source)
	require.Empty(t, repo.created[0].Title, "single links get their title from the video during processing")
	require.Equal(t, "https://www.youtube.com/watch?v=bbbbbbbbbbb", repo.created[1].Source)
	require.Equal(t, "Lesson B", repo.created[1].Title)
	require.Equal(t, 2, task.calls)

	require.Equal(t, []types.YouTubeImportItem{{
		URL: existing.Source, VideoID: "ccccccccccc", Title: "Lesson C", KnowledgeID: "existing-c",
	}}, result.Duplicates)
	require.Len(t, result.Failed, 2)
	require.Equal(t, "https://example.com/not-youtube", result.Failed[0].URL)
	require.Contains(t, result.Failed[0].Error, "not a YouTube")
	require.Equal(t, "https://www.youtube.com/playlist?list=PL222222", result.Failed[1].URL)
	require.Contains(t, result.Failed[1].Error, "playlist does not exist")
}

func TestCreateKnowledgeFromYouTubePlacesPlaylistsInFolders(t *testing.T) {
	allowYouTubeHostsForTest(t)

	repo := &youTubeRepoStub{}
	source := &fakeYouTubeSource{
		playlists: map[string]*youtube.Playlist{
			"PL111111": {ID: "PL111111", Title: "易經 / Part 1", Entries: []youtube.PlaylistEntry{
				{VideoID: "bbbbbbbbbbb", Title: "第一集"},
			}},
			"PL222222": {ID: "PL222222", Entries: []youtube.PlaylistEntry{{VideoID: "ccccccccccc"}}},
		},
	}
	svc := newYouTubeImportService(repo, &createKnowledgeTaskEnqueuerStub{}, source)

	result, err := svc.CreateKnowledgeFromYouTube(newCreateKnowledgeFileContext(), "kb-1", []string{
		"https://youtu.be/aaaaaaaaaaa",
		"https://www.youtube.com/playlist?list=PL111111",
		"https://www.youtube.com/playlist?list=PL222222",
	}, nil, nil, "", nil, " Lectures/ ")

	require.NoError(t, err)
	require.Len(t, repo.created, 3)
	require.Equal(t, "Lectures", repo.created[0].FolderPath)
	require.Equal(t, "Lectures/易經 - Part 1", repo.created[1].FolderPath, "a slash in the title must not nest folders")
	require.Equal(t, "Lectures/PL222222", repo.created[2].FolderPath, "untitled playlists fall back to their ID")
	require.Equal(t, "Lectures/易經 - Part 1", result.Playlists[0].FolderPath)
}

func TestCreateKnowledgeFromYouTubeCapsVideosPerImport(t *testing.T) {
	allowYouTubeHostsForTest(t)

	repo := &youTubeRepoStub{}
	task := &createKnowledgeTaskEnqueuerStub{}
	source := &fakeYouTubeSource{
		maxVideos: 2,
		playlists: map[string]*youtube.Playlist{
			"PL111111": {ID: "PL111111", Entries: []youtube.PlaylistEntry{
				{VideoID: "bbbbbbbbbbb"}, {VideoID: "ccccccccccc"},
			}},
		},
	}
	svc := newYouTubeImportService(repo, task, source)

	result, err := svc.CreateKnowledgeFromYouTube(newCreateKnowledgeFileContext(), "kb-1", []string{
		"https://youtu.be/aaaaaaaaaaa",
		"https://www.youtube.com/playlist?list=PL111111",
		"https://youtu.be/ddddddddddd",
	}, nil, nil, "", nil, "")

	require.NoError(t, err)
	require.True(t, result.Truncated)
	require.Equal(t, 2, result.TotalVideos)
	require.Len(t, repo.created, 2)
	require.Equal(t, "https://www.youtube.com/watch?v=bbbbbbbbbbb", repo.created[1].Source)
}

func TestCreateKnowledgeFromYouTubeErrors(t *testing.T) {
	t.Parallel()

	t.Run("no YouTube links", func(t *testing.T) {
		source := &fakeYouTubeSource{}
		_, err := newYouTubeImportService(&youTubeRepoStub{}, nil, source).CreateKnowledgeFromYouTube(
			newCreateKnowledgeFileContext(), "kb-1", []string{"https://example.com/a"}, nil, nil, "", nil, "")
		requireAppErrorStatus(t, err, http.StatusBadRequest)
		require.Contains(t, err.Error(), "not a YouTube")
		require.Empty(t, source.playlistCalls)
	})

	t.Run("only an empty playlist", func(t *testing.T) {
		source := &fakeYouTubeSource{}
		_, err := newYouTubeImportService(&youTubeRepoStub{}, nil, source).CreateKnowledgeFromYouTube(
			newCreateKnowledgeFileContext(), "kb-1", []string{"https://www.youtube.com/playlist?list=PL111111"},
			nil, nil, "", nil, "")
		requireAppErrorStatus(t, err, http.StatusBadRequest)
		require.Contains(t, err.Error(), "no importable videos")
	})

	t.Run("yt-dlp missing is a server error", func(t *testing.T) {
		source := &fakeYouTubeSource{playlistErrs: map[string]error{"PL111111": youtube.ErrToolMissing}}
		_, err := newYouTubeImportService(&youTubeRepoStub{}, nil, source).CreateKnowledgeFromYouTube(
			newCreateKnowledgeFileContext(), "kb-1", []string{"https://www.youtube.com/playlist?list=PL111111"},
			nil, nil, "", nil, "")
		requireAppErrorStatus(t, err, http.StatusInternalServerError)
	})
}

func sampleYouTubeTranscript() *youtube.Transcript {
	return &youtube.Transcript{
		Video:    youtube.VideoInfo{ID: "jNQXAC9IVRw", Title: "Me at the zoo", Channel: "jawed", Duration: 19},
		Source:   youtube.SourceCaptions,
		Language: "en",
		Segments: []youtube.Segment{{Start: 1.2, Text: "elephants have long trunks"}},
	}
}

func TestConvertYouTubeRendersTranscriptDocument(t *testing.T) {
	t.Parallel()

	source := &fakeYouTubeSource{transcript: sampleYouTubeTranscript()}
	svc := &knowledgeService{repo: &youTubeRepoStub{}, youtubeSource: source}
	knowledge := &types.Knowledge{ID: "k-1"}
	eff := types.EffectiveProcessConfig{}

	result, err := svc.convertYouTube(newCreateKnowledgeFileContext(),
		types.DocumentProcessPayload{URL: "https://www.youtube.com/watch?v=jNQXAC9IVRw"},
		&types.KnowledgeBase{ID: "kb-1"}, knowledge, eff, true, "jNQXAC9IVRw")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, source.gotTranscriber, "no ASR model configured, so no audio fallback")
	require.Contains(t, result.MarkdownContent, "# Me at the zoo")
	require.Contains(t, result.MarkdownContent, "[0:01] elephants have long trunks")
	require.NotContains(t, result.MarkdownContent, "## Documentation")
	require.Equal(t, "Me at the zoo", result.Metadata["title"])
	require.Equal(t, "captions", result.Metadata["transcript_source"])
}

func TestConvertYouTubeWithoutTranscriptFailsWithoutRetry(t *testing.T) {
	t.Parallel()

	repo := &youTubeRepoStub{}
	source := &fakeYouTubeSource{transcriptErr: youtube.ErrNoTranscript}
	svc := &knowledgeService{repo: repo, youtubeSource: source}
	knowledge := &types.Knowledge{ID: "k-1"}

	result, err := svc.convertYouTube(newCreateKnowledgeFileContext(),
		types.DocumentProcessPayload{URL: "https://www.youtube.com/watch?v=jNQXAC9IVRw"},
		&types.KnowledgeBase{ID: "kb-1"}, knowledge, types.EffectiveProcessConfig{}, false, "jNQXAC9IVRw")

	require.NoError(t, err)
	require.Nil(t, result)
	require.Equal(t, types.ParseStatusFailed, knowledge.ParseStatus)
	require.Contains(t, knowledge.ErrorMessage, "ASR")
	require.Len(t, repo.updated, 1)
}

func TestConvertYouTubeTransientFailureRetries(t *testing.T) {
	t.Parallel()

	repo := &youTubeRepoStub{}
	source := &fakeYouTubeSource{transcriptErr: errors.New("yt-dlp failed: HTTP Error 503")}
	svc := &knowledgeService{repo: repo, youtubeSource: source}
	knowledge := &types.Knowledge{ID: "k-1", ParseStatus: types.ParseStatusProcessing}

	result, err := svc.convertYouTube(newCreateKnowledgeFileContext(),
		types.DocumentProcessPayload{URL: "https://www.youtube.com/watch?v=jNQXAC9IVRw"},
		&types.KnowledgeBase{ID: "kb-1"}, knowledge, types.EffectiveProcessConfig{}, false, "jNQXAC9IVRw")

	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, types.ParseStatusProcessing, knowledge.ParseStatus, "not the last retry, so not failed yet")
}

type youTubeModelServiceStub struct {
	interfaces.ModelService

	chat chat.Chat
	asr  asr.ASR
}

func (m *youTubeModelServiceStub) GetChatModel(context.Context, string) (chat.Chat, error) {
	return m.chat, nil
}

func (m *youTubeModelServiceStub) GetASRModel(context.Context, string) (asr.ASR, error) {
	return m.asr, nil
}

type youTubeChatStub struct {
	userMessages []string
	err          error
}

func (c *youTubeChatStub) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not implemented")
}

func (c *youTubeChatStub) GetModelName() string { return "stub-chat" }

func (c *youTubeChatStub) GetModelID() string { return "chat-1" }

func (c *youTubeChatStub) Chat(
	_ context.Context, messages []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	c.userMessages = append(c.userMessages, messages[len(messages)-1].Content)
	if c.err != nil {
		return nil, c.err
	}
	return &types.ChatResponse{Content: "### Elephants\nThey have long trunks (from 0:01)."}, nil
}

type youTubeASRStub struct {
	asr.ASR

	result *asr.TranscriptionResult
}

func (a *youTubeASRStub) Transcribe(context.Context, []byte, string) (*asr.TranscriptionResult, error) {
	return a.result, nil
}

func TestConvertYouTubeGeneratesDocumentationWithSummaryModel(t *testing.T) {
	t.Parallel()

	chatModel := &youTubeChatStub{}
	source := &fakeYouTubeSource{transcript: sampleYouTubeTranscript()}
	svc := &knowledgeService{
		repo:          &youTubeRepoStub{},
		youtubeSource: source,
		modelService:  &youTubeModelServiceStub{chat: chatModel},
	}
	eff := types.EffectiveProcessConfig{ASRConfig: types.ASRConfig{Enabled: true, ModelID: "asr-1"}}

	result, err := svc.convertYouTube(newCreateKnowledgeFileContext(),
		types.DocumentProcessPayload{URL: "https://www.youtube.com/watch?v=jNQXAC9IVRw"},
		&types.KnowledgeBase{ID: "kb-1", SummaryModelID: "chat-1"}, &types.Knowledge{ID: "k-1"},
		eff, true, "jNQXAC9IVRw")

	require.NoError(t, err)
	require.Contains(t, result.MarkdownContent, "## Documentation\n\n### Elephants\nThey have long trunks (from 0:01).")
	require.Contains(t, result.MarkdownContent, "## Transcript")
	require.Len(t, chatModel.userMessages, 1)
	require.Contains(t, chatModel.userMessages[0], "Video title: Me at the zoo")
	require.Contains(t, chatModel.userMessages[0], "[0:01] elephants have long trunks")
	require.NotNil(t, source.gotTranscriber, "ASR is configured, so the audio fallback must be available")
}

func TestWriteYouTubeDocumentationSplitsLongTranscriptsAndFallsBackOnError(t *testing.T) {
	t.Parallel()

	transcript := sampleYouTubeTranscript()
	transcript.Segments = nil
	for i := 0; i < 400; i++ {
		transcript.Segments = append(transcript.Segments, youtube.Segment{
			Start: float64(i * 60), Text: strings.Repeat("word ", 20),
		})
	}

	chatModel := &youTubeChatStub{}
	doc := writeYouTubeDocumentation(context.Background(), chatModel, transcript)
	require.Greater(t, len(chatModel.userMessages), 1)
	require.Contains(t, chatModel.userMessages[0], "Transcript part 1 of")
	require.Equal(t, len(chatModel.userMessages), strings.Count(doc, "### Elephants"))

	failing := &youTubeChatStub{err: errors.New("model unavailable")}
	require.Empty(t, writeYouTubeDocumentation(context.Background(), failing, transcript))
}

func TestYouTubeTranscriberAdaptsASRResults(t *testing.T) {
	t.Parallel()

	asrModel := &youTubeASRStub{result: &asr.TranscriptionResult{
		Text:     "hello world",
		Segments: []asr.Segment{{Start: 2, Text: " hello "}, {Start: 4, Text: "  "}, {Start: 5, Text: "world"}},
	}}
	svc := &knowledgeService{modelService: &youTubeModelServiceStub{asr: asrModel}}
	transcribe := svc.youTubeTranscriber("asr-1")

	segments, err := transcribe(context.Background(), []byte("audio"), "piece.mp3")
	require.NoError(t, err)
	require.Equal(t, []youtube.Segment{{Start: 2, Text: "hello"}, {Start: 5, Text: "world"}}, segments)

	asrModel.result = &asr.TranscriptionResult{Text: " only text "}
	segments, err = transcribe(context.Background(), []byte("audio"), "piece.mp3")
	require.NoError(t, err)
	require.Equal(t, []youtube.Segment{{Text: "only text"}}, segments)
}
