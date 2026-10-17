package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// fakeVideoExtractor stands in for ffmpeg. Unit tests must never shell out: the
// codec flags were validated by the PoC, what these tests pin is the pipeline
// decision around the extractor.
type fakeVideoExtractor struct {
	audio    []byte
	err      error
	calls    int
	fileName string
}

func (f *fakeVideoExtractor) Extract(
	ctx context.Context, src io.Reader, fileName string,
) ([]byte, error) {
	f.calls++
	f.fileName = fileName
	if f.err != nil {
		return nil, f.err
	}
	// Drain the reader so a regression that stops handing over the video bytes
	// is visible here rather than in production.
	if _, err := io.Copy(io.Discard, src); err != nil {
		return nil, err
	}
	return f.audio, nil
}

type videoTestFileService struct {
	interfaces.FileService
	content []byte
	err     error
}

func (s *videoTestFileService) GetFile(ctx context.Context, filePath string) (io.ReadCloser, error) {
	if s.err != nil {
		return nil, s.err
	}
	return io.NopCloser(bytes.NewReader(s.content)), nil
}

type videoTestKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	updates int
	columns map[string]interface{}
	err     error
}

func (r *videoTestKnowledgeRepo) UpdateKnowledgeColumns(
	ctx context.Context, id string, values map[string]interface{},
) error {
	r.updates++
	r.columns = values
	return r.err
}

func newVideoTestContext() context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	return context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{})
}

// The video branch must hand the extracted track to the ASR step as audio, not
// pretend it parsed text: that is what makes Step 1.5 transcribe it and the
// existing chunk/embed pipeline index the narration.
func TestConvertVideoToAudioHandsTheTrackToTheASRStep(t *testing.T) {
	t.Parallel()

	extractor := &fakeVideoExtractor{audio: []byte("m4a-audio-bytes")}
	repo := &videoTestKnowledgeRepo{}
	svc := &knowledgeService{
		repo:           repo,
		fileSvc:        &videoTestFileService{content: []byte("mp4-bytes")},
		videoExtractor: extractor,
	}
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		FileType:        "mp4",
		FileName:        "Lesson.mp4",
		FilePath:        "videos/knowledge-1.mp4",
		ParseStatus:     types.ParseStatusProcessing,
	}

	result, err := svc.convertVideoToAudio(newVideoTestContext(),
		types.DocumentProcessPayload{
			FilePath: knowledge.FilePath,
			FileName: knowledge.FileName,
			FileType: knowledge.FileType,
		},
		&types.KnowledgeBase{ID: "kb-1"},
		knowledge,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsAudio, "the result must route through the ASR step")
	require.Equal(t, []byte("m4a-audio-bytes"), result.AudioData)
	require.Equal(t, "Lesson.mp4", extractor.fileName)
	require.Zero(t, repo.updates, "a successful extraction must not rewrite the row")
}

// ffmpeg missing / codec failure / unreadable object: the import degrades to the
// archive it already was. It must not return an error (asynq would retry into the
// same wall) and must not leave the row saying "processing".
func TestConvertVideoToAudioFallsBackToArchiveWhenExtractionFails(t *testing.T) {
	t.Parallel()

	repo := &videoTestKnowledgeRepo{}
	extractor := &fakeVideoExtractor{err: errors.New("ffmpeg binary \"ffmpeg\" is not available")}
	svc := &knowledgeService{
		repo:           repo,
		fileSvc:        &videoTestFileService{content: []byte("mp4-bytes")},
		videoExtractor: extractor,
	}
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		FileType:        "mp4",
		FileName:        "Lesson.mp4",
		ParseStatus:     types.ParseStatusProcessing,
	}
	ctx := newVideoTestContext()

	_, err := svc.convertVideoToAudio(ctx, types.DocumentProcessPayload{
		FilePath: "videos/knowledge-1.mp4",
		FileName: "Lesson.mp4",
		FileType: "mp4",
	}, &types.KnowledgeBase{ID: "kb-1"}, knowledge)

	require.NoError(t, err, "degradation is not a task failure")
	require.Equal(t, 1, repo.updates)
	require.Equal(t, types.ParseStatusStored, repo.columns["parse_status"])
	require.Equal(t, "disabled", repo.columns["enable_status"])
	require.Equal(t, "", repo.columns["error_message"], "an archived row must not surface a failure")
	require.Equal(t, types.ParseStatusStored, knowledge.ParseStatus)
	require.Equal(t, "disabled", knowledge.EnableStatus)
}

func TestConvertVideoToAudioFallsBackWhenTheStoredFileIsUnreadable(t *testing.T) {
	t.Parallel()

	repo := &videoTestKnowledgeRepo{}
	extractor := &fakeVideoExtractor{audio: []byte("never-used")}
	svc := &knowledgeService{
		repo:           repo,
		fileSvc:        &videoTestFileService{err: errors.New("object not found")},
		videoExtractor: extractor,
	}
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		FileType:        "mp4",
		FileName:        "Lesson.mp4",
		ParseStatus:     types.ParseStatusProcessing,
	}

	_, err := svc.convertVideoToAudio(newVideoTestContext(), types.DocumentProcessPayload{
		FilePath: "videos/knowledge-1.mp4",
		FileName: "Lesson.mp4",
		FileType: "mp4",
	}, &types.KnowledgeBase{ID: "kb-1"}, knowledge)

	require.NoError(t, err)
	require.Zero(t, extractor.calls, "no bytes means no ffmpeg run")
	require.Equal(t, types.ParseStatusStored, repo.columns["parse_status"])
}

// If the archive state cannot be persisted, the row is still "processing" and
// acking the task would strand it there. Surface the error so asynq retries.
func TestConvertVideoToAudioRetriesWhenArchivingCannotBePersisted(t *testing.T) {
	t.Parallel()

	repo := &videoTestKnowledgeRepo{err: errors.New("db unavailable")}
	svc := &knowledgeService{
		repo:           repo,
		fileSvc:        &videoTestFileService{content: []byte("mp4-bytes")},
		videoExtractor: &fakeVideoExtractor{err: errors.New("no audio stream")},
	}
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		FileType:        "mp4",
		ParseStatus:     types.ParseStatusProcessing,
	}

	_, err := svc.convertVideoToAudio(newVideoTestContext(), types.DocumentProcessPayload{
		FilePath: "videos/knowledge-1.mp4",
		FileName: "Lesson.mp4",
		FileType: "mp4",
	}, &types.KnowledgeBase{ID: "kb-1"}, knowledge)

	require.Error(t, err)
}

// The connector asks to store unindexed. With the KB opted in, the same bytes
// must instead be dispatched for parsing: that is the whole point of the switch.
func TestCreateStoredVideoParsesWhenVideoConfigEnabled(t *testing.T) {
	t.Parallel()

	repo := &createKnowledgeFileRepoStub{}
	fileSvc := &createKnowledgeFileServiceStub{}
	task := &createKnowledgeTaskEnqueuerStub{}
	svc := &knowledgeService{
		repo: repo,
		kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{
			ID:          "kb-1",
			VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeAudioOnly},
			ASRConfig:   types.ASRConfig{Enabled: true, ModelID: "asr-1"},
		}},
		fileSvc: fileSvc,
		task:    task,
	}

	knowledge, err := svc.CreateStoredKnowledgeFromFile(
		newCreateKnowledgeFileContext(),
		"kb-1",
		newMultipartFileHeader(t, "Lesson.mp4", videoBytes),
		map[string]string{"channel": "dingtalk", "external_id": "node-1"},
		"",
		nil,
		types.ChannelDingtalk,
	)

	require.NoError(t, err)
	require.NotNil(t, knowledge)
	require.Equal(t, types.ParseStatusPending, knowledge.ParseStatus,
		"an opted-in video must be queued for parsing, not archived")
	require.Equal(t, 1, repo.createCalls)
	require.Equal(t, 1, task.calls, "the parse task is what turns the video into chunks")
}

// The switch alone cannot produce text: without an ASR model the connector's
// request keeps its old meaning and the video stays an archive.
func TestCreateStoredVideoStaysArchivedWithoutASRModel(t *testing.T) {
	t.Parallel()

	repo := &createKnowledgeFileRepoStub{}
	task := &createKnowledgeTaskEnqueuerStub{}
	svc := &knowledgeService{
		repo: repo,
		kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{
			ID:          "kb-1",
			VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeAudioOnly},
		}},
		fileSvc: &createKnowledgeFileServiceStub{},
		task:    task,
	}

	knowledge, err := svc.CreateStoredKnowledgeFromFile(
		newCreateKnowledgeFileContext(), "kb-1",
		newMultipartFileHeader(t, "Lesson.mp4", videoBytes),
		nil, "", nil, types.ChannelDingtalk,
	)

	require.NoError(t, err)
	require.Equal(t, types.ParseStatusStored, knowledge.ParseStatus)
	require.Zero(t, task.calls)
}

// Default behaviour is unchanged: an existing deployment that never touched the
// switch still archives videos.
func TestCreateStoredVideoStaysArchivedWhenVideoConfigDisabled(t *testing.T) {
	t.Parallel()

	repo := &createKnowledgeFileRepoStub{}
	task := &createKnowledgeTaskEnqueuerStub{}
	svc := &knowledgeService{
		repo: repo,
		kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{
			ID:        "kb-1",
			ASRConfig: types.ASRConfig{Enabled: true, ModelID: "asr-1"},
		}},
		fileSvc: &createKnowledgeFileServiceStub{},
		task:    task,
	}

	knowledge, err := svc.CreateStoredKnowledgeFromFile(
		newCreateKnowledgeFileContext(), "kb-1",
		newMultipartFileHeader(t, "Lesson.mp4", videoBytes),
		nil, "", nil, types.ChannelDingtalk,
	)

	require.NoError(t, err)
	require.Equal(t, types.ParseStatusStored, knowledge.ParseStatus)
	require.Zero(t, task.calls)
}

// Direct uploads follow the same switch: rejected when the KB has not opted in,
// queued for parsing when it has.
func TestCreateVideoUploadUsesTheVideoSwitch(t *testing.T) {
	t.Parallel()

	t.Run("rejected without the switch", func(t *testing.T) {
		t.Parallel()
		repo := &createKnowledgeFileRepoStub{}
		task := &createKnowledgeTaskEnqueuerStub{}
		svc := &knowledgeService{
			repo:      repo,
			kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1"}},
			fileSvc:   &createKnowledgeFileServiceStub{},
			task:      task,
		}

		_, err := svc.CreateKnowledgeFromFile(
			newCreateKnowledgeFileContext(), "kb-1",
			newMultipartFileHeader(t, "Lesson.mp4", videoBytes),
			nil, nil, "", nil, "", nil,
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "暂不支持上传视频文件")
		require.Zero(t, repo.createCalls)
		require.Zero(t, task.calls)
	})

	t.Run("rejected when ASR is missing", func(t *testing.T) {
		t.Parallel()
		svc := &knowledgeService{
			repo: &createKnowledgeFileRepoStub{},
			kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{
				ID:          "kb-1",
				VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeAudioOnly},
			}},
			fileSvc: &createKnowledgeFileServiceStub{},
			task:    &createKnowledgeTaskEnqueuerStub{},
		}

		_, err := svc.CreateKnowledgeFromFile(
			newCreateKnowledgeFileContext(), "kb-1",
			newMultipartFileHeader(t, "Lesson.mp4", videoBytes),
			nil, nil, "", nil, "", nil,
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "ASR")
	})

	t.Run("parsed when the switch and ASR are configured", func(t *testing.T) {
		t.Parallel()
		repo := &createKnowledgeFileRepoStub{}
		task := &createKnowledgeTaskEnqueuerStub{}
		svc := &knowledgeService{
			repo: repo,
			kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{
				ID:          "kb-1",
				VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeAudioOnly},
				ASRConfig:   types.ASRConfig{Enabled: true, ModelID: "asr-1"},
			}},
			fileSvc: &createKnowledgeFileServiceStub{},
			task:    task,
		}

		knowledge, err := svc.CreateKnowledgeFromFile(
			newCreateKnowledgeFileContext(), "kb-1",
			newMultipartFileHeader(t, "Lesson.mp4", videoBytes),
			nil, nil, "", nil, "", nil,
		)
		require.NoError(t, err)
		require.Equal(t, types.ParseStatusPending, knowledge.ParseStatus)
		require.Equal(t, 1, repo.createCalls)
		require.Equal(t, 1, task.calls)
	})
}

// Option A for existing archives: only a video whose KB has since opted in may
// be re-parsed. Everything else keeps the refusal that protects a settled
// archive from being flipped to failed.
func TestCanUpgradeStoredVideo(t *testing.T) {
	t.Parallel()

	enabledKB := &types.KnowledgeBase{
		ID:          "kb-1",
		VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeAudioOnly},
		ASRConfig:   types.ASRConfig{Enabled: true, ModelID: "asr-1"},
	}
	asrOnlyKB := &types.KnowledgeBase{
		ID:        "kb-1",
		ASRConfig: types.ASRConfig{Enabled: true, ModelID: "asr-1"},
	}
	video := func(fileType, fileName string) *types.Knowledge {
		return &types.Knowledge{FileType: fileType, FileName: fileName}
	}

	cases := []struct {
		name      string
		knowledge *types.Knowledge
		kb        *types.KnowledgeBase
		want      bool
	}{
		{name: "video with switch and asr", knowledge: video("mp4", "Lesson.mp4"), kb: enabledKB, want: true},
		{name: "video file type empty, name carries it", knowledge: video("", "Lesson.mp4"), kb: enabledKB, want: true},
		{name: "video without the switch", knowledge: video("mp4", "Lesson.mp4"), kb: asrOnlyKB, want: false},
		{name: "video without asr", knowledge: video("mp4", "Lesson.mp4"), kb: &types.KnowledgeBase{
			VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeAudioOnly},
		}, want: false},
		{name: "non-video archive", knowledge: video("pdf", "report.pdf"), kb: enabledKB, want: false},
		{name: "nil knowledge", knowledge: nil, kb: enabledKB, want: false},
		{name: "nil kb", knowledge: video("mp4", "Lesson.mp4"), kb: nil, want: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, canUpgradeStoredVideo(tc.knowledge, tc.kb, nil))
		})
	}
}

func TestVideoInputExtensionKeepsOnlyVideoSuffixes(t *testing.T) {
	t.Parallel()

	require.Equal(t, ".mov", videoInputExtension("Lesson.MOV"))
	require.Equal(t, ".mkv", videoInputExtension("folder/Lesson.mkv"))
	require.Equal(t, ".mp4", videoInputExtension("Lesson.txt"))
	require.Equal(t, ".mp4", videoInputExtension("no-extension"))
}

// The production extractor resolves ffmpeg on the PATH. When the binary is
// missing it must fail fast with an actionable reason — that string becomes the
// degradation log line, and the video is archived instead of the sync failing.
func TestFFmpegAudioExtractorReportsAMissingBinary(t *testing.T) {
	t.Parallel()

	extractor := &ffmpegAudioExtractor{binary: "weknora-test-ffmpeg-does-not-exist"}
	_, err := extractor.Extract(newVideoTestContext(), bytes.NewReader(nil), "Lesson.mp4")

	require.Error(t, err)
	require.Contains(t, err.Error(), "is not available")
}
