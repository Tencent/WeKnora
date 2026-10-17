package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/models/vlm"
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

// fakeVideoFrameExtractor stands in for the ffmpeg frame pass. The JPEG bytes
// are opaque on purpose: the pipeline only forwards them to the VLM.
type fakeVideoFrameExtractor struct {
	frames []videoFrame
	err    error
	calls  int
}

func (f *fakeVideoFrameExtractor) ExtractKeyframes(
	ctx context.Context, src io.Reader, fileName string,
) ([]videoFrame, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if _, err := io.Copy(io.Discard, src); err != nil {
		return nil, err
	}
	return f.frames, nil
}

// videoTestVLM records the prompts it was called with and can fail on chosen
// frames, which is how the tests pin the per-frame degradation.
type videoTestVLM struct {
	replies []string
	err     error
	failAt  map[int]error
	prompts []string
	images  int
}

func (v *videoTestVLM) Predict(
	ctx context.Context, imgBytes [][]byte, prompt string,
) (string, error) {
	v.prompts = append(v.prompts, prompt)
	v.images += len(imgBytes)
	index := len(v.prompts)
	if err, ok := v.failAt[index]; ok {
		return "", err
	}
	if v.err != nil {
		return "", v.err
	}
	if index <= len(v.replies) {
		return v.replies[index-1], nil
	}
	return "", nil
}

func (v *videoTestVLM) GetModelName() string { return "video-test-vlm" }
func (v *videoTestVLM) GetModelID() string   { return "vlm-1" }

type videoTestASR struct {
	result   *asr.TranscriptionResult
	err      error
	calls    int
	modelID  string
	gotAudio []byte
}

func (a *videoTestASR) Transcribe(
	ctx context.Context, audioBytes []byte, fileName string,
) (*asr.TranscriptionResult, error) {
	a.calls++
	a.gotAudio = audioBytes
	if a.err != nil {
		return nil, a.err
	}
	return a.result, nil
}

func (a *videoTestASR) GetModelName() string { return "video-test-asr" }
func (a *videoTestASR) GetModelID() string   { return a.modelID }

// videoTestModelService embeds the interface so only the lookups a video parse
// performs have to be implemented.
type videoTestModelService struct {
	interfaces.ModelService
	models        map[string]*types.Model
	vlmModel      vlm.VLM
	vlmErr        error
	asrModel      asr.ASR
	asrErr        error
	gotASRModelID string
	gotVLMModelID string
}

func (s *videoTestModelService) GetModelByID(ctx context.Context, id string) (*types.Model, error) {
	return s.models[id], nil
}

func (s *videoTestModelService) GetVLMModel(ctx context.Context, id string) (vlm.VLM, error) {
	s.gotVLMModelID = id
	if s.vlmErr != nil {
		return nil, s.vlmErr
	}
	return s.vlmModel, nil
}

func (s *videoTestModelService) GetASRModel(ctx context.Context, id string) (asr.ASR, error) {
	s.gotASRModelID = id
	if s.asrErr != nil {
		return nil, s.asrErr
	}
	return s.asrModel, nil
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

func newVideoTestKnowledge() *types.Knowledge {
	return &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		FileType:        "mp4",
		FileName:        "Lesson.mp4",
		FilePath:        "videos/knowledge-1.mp4",
		ParseStatus:     types.ParseStatusProcessing,
	}
}

func newVideoTestPayload(knowledge *types.Knowledge) types.DocumentProcessPayload {
	return types.DocumentProcessPayload{
		FilePath: knowledge.FilePath,
		FileName: knowledge.FileName,
		FileType: knowledge.FileType,
	}
}

func newAudioOnlyVideoKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID: "kb-1",
		VideoConfig: types.VideoConfig{
			Enabled: true, Mode: types.VideoParseModeAudioOnly, ModelID: "asr-1",
		},
	}
}

func newKeyframeVideoKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID: "kb-1",
		VideoConfig: types.VideoConfig{
			Enabled:   true,
			Mode:      types.VideoParseModeKeyframes,
			VLMConfig: &types.VLMConfig{Enabled: true, ModelID: "vlm-1"},
		},
	}
}

func newAudioKeyframeVideoKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID: "kb-1",
		VideoConfig: types.VideoConfig{
			Enabled:   true,
			Mode:      types.VideoParseModeAudioKeyframes,
			ModelID:   "asr-1",
			VLMConfig: &types.VLMConfig{Enabled: true, ModelID: "vlm-1"},
		},
	}
}

// The video branch must hand the extracted track to the ASR step as audio, not
// pretend it parsed text: that is what makes Step 1.5 transcribe it and the
// existing chunk/embed pipeline index the narration.
func TestConvertVideoHandsTheTrackToTheASRStep(t *testing.T) {
	t.Parallel()

	extractor := &fakeVideoExtractor{audio: []byte("m4a-audio-bytes")}
	repo := &videoTestKnowledgeRepo{}
	svc := &knowledgeService{
		repo:           repo,
		fileSvc:        &videoTestFileService{content: []byte("mp4-bytes")},
		videoExtractor: extractor,
	}
	knowledge := newVideoTestKnowledge()
	kb := newAudioOnlyVideoKB()

	result, err := svc.convertVideo(newVideoTestContext(), newVideoTestPayload(knowledge),
		kb, knowledge)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsAudio, "the result must route through the ASR step")
	require.Equal(t, []byte("m4a-audio-bytes"), result.AudioData)
	require.Empty(t, result.VideoFrames, "an audio_only mode must not sample frames")
	require.Equal(t, "Lesson.mp4", extractor.fileName)
	require.Zero(t, repo.updates, "a successful extraction must not rewrite the row")
}

// ffmpeg missing / codec failure / unreadable object: the import degrades to the
// archive it already was. It must not return an error (asynq would retry into the
// same wall) and must not leave the row saying "processing".
func TestConvertVideoFallsBackToArchiveWhenExtractionFails(t *testing.T) {
	t.Parallel()

	repo := &videoTestKnowledgeRepo{}
	extractor := &fakeVideoExtractor{err: errors.New("ffmpeg binary \"ffmpeg\" is not available")}
	svc := &knowledgeService{
		repo:           repo,
		fileSvc:        &videoTestFileService{content: []byte("mp4-bytes")},
		videoExtractor: extractor,
	}
	knowledge := newVideoTestKnowledge()
	kb := newAudioOnlyVideoKB()

	_, err := svc.convertVideo(newVideoTestContext(), newVideoTestPayload(knowledge),
		kb, knowledge)

	require.NoError(t, err, "degradation is not a task failure")
	require.Equal(t, 1, repo.updates)
	require.Equal(t, types.ParseStatusStored, repo.columns["parse_status"])
	require.Equal(t, "disabled", repo.columns["enable_status"])
	require.Equal(t, "", repo.columns["error_message"], "an archived row must not surface a failure")
	require.Equal(t, types.ParseStatusStored, knowledge.ParseStatus)
	require.Equal(t, "disabled", knowledge.EnableStatus)
}

func TestConvertVideoFallsBackWhenTheStoredFileIsUnreadable(t *testing.T) {
	t.Parallel()

	repo := &videoTestKnowledgeRepo{}
	extractor := &fakeVideoExtractor{audio: []byte("never-used")}
	svc := &knowledgeService{
		repo:           repo,
		fileSvc:        &videoTestFileService{err: errors.New("object not found")},
		videoExtractor: extractor,
	}
	knowledge := newVideoTestKnowledge()
	kb := newAudioOnlyVideoKB()

	_, err := svc.convertVideo(newVideoTestContext(), newVideoTestPayload(knowledge),
		kb, knowledge)

	require.NoError(t, err)
	require.Zero(t, extractor.calls, "no bytes means no ffmpeg run")
	require.Equal(t, types.ParseStatusStored, repo.columns["parse_status"])
}

// If the archive state cannot be persisted, the row is still "processing" and
// acking the task would strand it there. Surface the error so asynq retries.
func TestConvertVideoRetriesWhenArchivingCannotBePersisted(t *testing.T) {
	t.Parallel()

	repo := &videoTestKnowledgeRepo{err: errors.New("db unavailable")}
	svc := &knowledgeService{
		repo:           repo,
		fileSvc:        &videoTestFileService{content: []byte("mp4-bytes")},
		videoExtractor: &fakeVideoExtractor{err: errors.New("no audio stream")},
	}
	knowledge := newVideoTestKnowledge()
	kb := newAudioOnlyVideoKB()

	_, err := svc.convertVideo(newVideoTestContext(), newVideoTestPayload(knowledge),
		kb, knowledge)

	require.Error(t, err)
}

// keyframes mode: frames go to the VLM one by one, and the returned captions
// carry their capture times so the final text can be time-ordered.
func TestConvertVideoDescribesSampledFrames(t *testing.T) {
	t.Parallel()

	frames := &fakeVideoFrameExtractor{frames: []videoFrame{
		{StartMs: 0, Data: []byte("jpeg-1")},
		{StartMs: 15_000, Data: []byte("jpeg-2")},
	}}
	model := &videoTestVLM{replies: []string{"  终端里执行 go test  ", "PPT：\n视频处理架构"}}
	modelSvc := &videoTestModelService{vlmModel: model}
	svc := &knowledgeService{
		repo:                &videoTestKnowledgeRepo{},
		fileSvc:             &videoTestFileService{content: []byte("mp4-bytes")},
		videoFrameExtractor: frames,
		modelService:        modelSvc,
	}
	knowledge := newVideoTestKnowledge()
	kb := newKeyframeVideoKB()

	result, err := svc.convertVideo(newVideoTestContext(), newVideoTestPayload(knowledge),
		kb, knowledge)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsAudio, "keyframes mode must not transcribe the narration")
	require.Empty(t, result.AudioData)
	require.Len(t, result.VideoFrames, 2)
	require.Equal(t, int64(0), result.VideoFrames[0].StartMs)
	require.Equal(t, int64(15_000), result.VideoFrames[1].StartMs)
	require.Equal(t, "终端里执行 go test", result.VideoFrames[0].Text,
		"the caption is flattened to one line before it is chunked")
	require.Equal(t, "PPT： 视频处理架构", result.VideoFrames[1].Text)
	require.Equal(t, "vlm-1", modelSvc.gotVLMModelID)
	require.Equal(t, 2, model.images)
	require.Contains(t, model.prompts[0], "training video")
	require.Contains(t, model.prompts[0], "unreadable frame")
	require.Contains(t, result.MarkdownContent, "[00:00:00] 画面：终端里执行 go test")
	require.Contains(t, result.MarkdownContent, "[00:00:15] 画面：PPT： 视频处理架构")
	require.Len(t, result.SourceBlocks, 2)
	require.Equal(t, int64(15_000), result.SourceBlocks[1].Locator.StartMs)
	// The captions must survive the real chunker: this is the text the index
	// will actually hold, not just an intermediate string.
	chunks := chunker.SplitText(result.MarkdownContent,
		chunker.SplitterConfig{ChunkSize: 512, ChunkOverlap: 0})
	require.NotEmpty(t, chunks)
	var chunked strings.Builder
	for _, chunk := range chunks {
		chunked.WriteString(chunk.Content)
	}
	require.Contains(t, chunked.String(), "[00:00:15] 画面：PPT： 视频处理架构")
}

// Frame descriptions use the video section's own vision model. The image
// section's vlm_config is a different setting: retuning image descriptions must
// never silently change what a video's frames say.
func TestConvertVideoDescribesFramesWithTheVideoSectionsVLM(t *testing.T) {
	t.Parallel()

	model := &videoTestVLM{replies: []string{"界面：登录页"}}
	modelSvc := &videoTestModelService{vlmModel: model}
	svc := &knowledgeService{
		repo:    &videoTestKnowledgeRepo{},
		fileSvc: &videoTestFileService{content: []byte("mp4-bytes")},
		videoFrameExtractor: &fakeVideoFrameExtractor{
			frames: []videoFrame{{StartMs: 0, Data: []byte("jpeg")}},
		},
		modelService: modelSvc,
	}
	knowledge := newVideoTestKnowledge()
	kb := &types.KnowledgeBase{
		ID: "kb-1",
		VideoConfig: types.VideoConfig{
			Enabled:   true,
			Mode:      types.VideoParseModeKeyframes,
			VLMConfig: &types.VLMConfig{Enabled: true, ModelID: "vlm-video"},
		},
		// A different, perfectly usable VLM in the image section: it must not be
		// the model the video asks for.
		VLMConfig: types.VLMConfig{Enabled: true, ModelID: "vlm-image"},
	}

	result, err := svc.convertVideo(newVideoTestContext(), newVideoTestPayload(knowledge),
		kb, knowledge)

	require.NoError(t, err)
	require.Len(t, result.VideoFrames, 1)
	require.Equal(t, "vlm-video", modelSvc.gotVLMModelID,
		"the video's frames must be described by video_config.vlm_config")
	require.Len(t, model.prompts, 1)
}

// The system-owned prompt is byte-for-byte what it was before the configurable
// instructions existed: a knowledge base that never customized anything must
// send exactly the same text to the model.
func TestBuildVideoFramePromptIsUnchangedWithoutInstructions(t *testing.T) {
	t.Parallel()

	const want = `You describe frames sampled from a training video so a text-only reader can follow it.
Look at the frame and report, in this order of priority:
1. Software interfaces: which screen, menu, dialog or button is visible, and the readable labels.
2. Terminals: the command being run and the visible output.
3. Slides, whiteboards or diagrams: the title and the key points.
Only state what is clearly visible. If the frame is blank, blurred or unreadable,
say "unreadable frame" instead of guessing.
Answer in Chinese with at most three short sentences. No preamble, no markdown headings, no bullet points.`

	for _, instructions := range []string{"", "   ", "\n\t"} {
		got := buildVideoFramePrompt(context.Background(), types.VLMConfig{
			DescriptionLanguage: "Chinese",
			CustomInstructions:  instructions,
		})
		require.Equal(t, want, got, "instructions %q must not change the prompt", instructions)
	}
}

// Custom guidance is appended after the fixed block, so the unreadable-frame
// rule and the output shape stay in force even when the operator writes
// guidance that tries to replace them.
func TestBuildVideoFramePromptAppendsInstructionsAfterTheFixedRules(t *testing.T) {
	t.Parallel()

	base := buildVideoFramePrompt(context.Background(), types.VLMConfig{DescriptionLanguage: "Chinese"})
	guidance := "重点识别报错弹窗和按钮文案；忽略右下角水印。"
	got := buildVideoFramePrompt(context.Background(), types.VLMConfig{
		DescriptionLanguage: "Chinese",
		CustomInstructions:  "  " + guidance + "  ",
	})

	require.True(t, strings.HasPrefix(got, base),
		"the fixed prompt must come first, unchanged")
	require.Contains(t, got, guidance)
	require.Contains(t, got, "unreadable frame", "the anti-hallucination rule must survive")
	require.Contains(t, got, "at most three short sentences", "the output contract must survive")
	require.Contains(t, got, "video_frame_business_instructions")
	require.Contains(t, got, "do not conflict with the system-owned output format")
	require.Less(t, strings.Index(got, "unreadable frame"), strings.Index(got, guidance),
		"the fixed rules must precede the custom guidance")
}

// Without a VLM in the video section the frame pass is skipped with a warning,
// and the narration is still transcribed: the two sources degrade
// independently.
func TestConvertVideoKeepsNarrationWhenTheVideoVLMIsNotConfigured(t *testing.T) {
	// Not parallel: it captures the process-wide logger output.
	var logs bytes.Buffer
	logger.SetOutput(&logs)
	defer logger.SetOutput(os.Stdout)

	frames := &fakeVideoFrameExtractor{
		frames: []videoFrame{{StartMs: 0, Data: []byte("jpeg")}},
	}
	modelSvc := &videoTestModelService{vlmModel: &videoTestVLM{replies: []string{"never used"}}}
	svc := &knowledgeService{
		repo:                &videoTestKnowledgeRepo{},
		fileSvc:             &videoTestFileService{content: []byte("mp4-bytes")},
		videoExtractor:      &fakeVideoExtractor{audio: []byte("m4a-bytes")},
		videoFrameExtractor: frames,
		modelService:        modelSvc,
	}
	knowledge := newVideoTestKnowledge()
	kb := &types.KnowledgeBase{
		ID: "kb-1",
		VideoConfig: types.VideoConfig{
			Enabled: true, Mode: types.VideoParseModeAudioKeyframes, ModelID: "asr-1",
		},
		// The image section's VLM is configured and must not be borrowed.
		VLMConfig: types.VLMConfig{Enabled: true, ModelID: "vlm-image"},
	}

	result, err := svc.convertVideo(newVideoTestContext(), newVideoTestPayload(knowledge),
		kb, knowledge)

	require.NoError(t, err)
	require.True(t, result.IsAudio, "the narration must still be transcribed")
	require.Equal(t, []byte("m4a-bytes"), result.AudioData)
	require.Empty(t, result.VideoFrames)
	require.Zero(t, frames.calls, "no video VLM means the frame pass never runs")
	require.Empty(t, modelSvc.gotVLMModelID, "the image section's VLM must not be resolved")
	require.Contains(t, logs.String(), "the video section has no VLM model configured")
}

// A failed frame pass must not cost the narration: the audio result is still
// handed to the ASR step, and only the picture source degrades.
func TestConvertVideoKeepsAudioWhenFrameDescriptionFails(t *testing.T) {
	t.Parallel()

	svc := &knowledgeService{
		repo:           &videoTestKnowledgeRepo{},
		fileSvc:        &videoTestFileService{content: []byte("mp4-bytes")},
		videoExtractor: &fakeVideoExtractor{audio: []byte("m4a-bytes")},
		videoFrameExtractor: &fakeVideoFrameExtractor{
			frames: []videoFrame{{StartMs: 0, Data: []byte("jpeg")}},
		},
		modelService: &videoTestModelService{vlmErr: errors.New("vlm down")},
	}
	knowledge := newVideoTestKnowledge()
	kb := newAudioKeyframeVideoKB()

	result, err := svc.convertVideo(newVideoTestContext(), newVideoTestPayload(knowledge),
		kb, knowledge)

	require.NoError(t, err)
	require.True(t, result.IsAudio)
	require.Equal(t, []byte("m4a-bytes"), result.AudioData)
	require.Empty(t, result.VideoFrames)
}

// A keyframes-only video with no VLM has no text source at all: it settles as an
// archive, never as a failed import.
func TestConvertVideoArchivesKeyframesWithoutVLM(t *testing.T) {
	t.Parallel()

	repo := &videoTestKnowledgeRepo{}
	svc := &knowledgeService{
		repo:    repo,
		fileSvc: &videoTestFileService{content: []byte("mp4-bytes")},
		videoFrameExtractor: &fakeVideoFrameExtractor{
			frames: []videoFrame{{StartMs: 0, Data: []byte("jpeg")}},
		},
	}
	knowledge := newVideoTestKnowledge()
	kb := &types.KnowledgeBase{
		ID:          "kb-1",
		VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeKeyframes},
	}

	_, err := svc.convertVideo(newVideoTestContext(), newVideoTestPayload(knowledge),
		kb, knowledge)

	require.NoError(t, err, "degradation is not a task failure")
	require.Equal(t, 1, repo.updates)
	require.Equal(t, types.ParseStatusStored, repo.columns["parse_status"])
	require.Equal(t, types.ParseStatusStored, knowledge.ParseStatus)
}

// A keyframes-only video whose VLM exists but cannot describe anything settles
// the same way instead of failing.
func TestConvertVideoArchivesWhenVLMDescribesNothing(t *testing.T) {
	t.Parallel()

	repo := &videoTestKnowledgeRepo{}
	svc := &knowledgeService{
		repo:    repo,
		fileSvc: &videoTestFileService{content: []byte("mp4-bytes")},
		videoFrameExtractor: &fakeVideoFrameExtractor{
			frames: []videoFrame{{StartMs: 0, Data: []byte("jpeg")}},
		},
		modelService: &videoTestModelService{vlmModel: &videoTestVLM{err: errors.New("boom")}},
	}
	knowledge := newVideoTestKnowledge()
	kb := newKeyframeVideoKB()

	_, err := svc.convertVideo(newVideoTestContext(), newVideoTestPayload(knowledge),
		kb, knowledge)

	require.NoError(t, err)
	require.Equal(t, types.ParseStatusStored, repo.columns["parse_status"])
}

// The audio source exists only when the video section names a model; the audio
// section's ASR is a different upload type and must not make this capability
// true. The frame source is the same story with the image section's VLM.
func TestVideoParseCapabilitiesReadTheVideoSectionOnly(t *testing.T) {
	t.Parallel()

	audioOnly := &types.KnowledgeBase{
		VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeAudioOnly},
		ASRConfig:   types.ASRConfig{Enabled: true, ModelID: "asr-audio"},
	}
	audio, frames := videoParseCapabilities(audioOnly)
	require.False(t, audio, "an audio-section model must not count as the video's model")
	require.False(t, frames)

	withVideoModel := &types.KnowledgeBase{
		VideoConfig: types.VideoConfig{
			Enabled: true, Mode: types.VideoParseModeAudioOnly, ModelID: "asr-video",
		},
	}
	audio, frames = videoParseCapabilities(withVideoModel)
	require.True(t, audio)
	require.False(t, frames)

	// The image section's VLM is a different setting: a keyframes video whose
	// only VLM lives at the knowledge-base level has no frame source.
	imageVLMOnly := &types.KnowledgeBase{
		VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeKeyframes},
		VLMConfig:   types.VLMConfig{Enabled: true, ModelID: "vlm-image"},
	}
	audio, frames = videoParseCapabilities(imageVLMOnly)
	require.False(t, audio)
	require.False(t, frames, "the image section's VLM must not describe video frames")

	withVideoVLM := &types.KnowledgeBase{
		VideoConfig: types.VideoConfig{
			Enabled:   true,
			Mode:      types.VideoParseModeKeyframes,
			VLMConfig: &types.VLMConfig{Enabled: true, ModelID: "vlm-video"},
		},
	}
	audio, frames = videoParseCapabilities(withVideoVLM)
	require.False(t, audio)
	require.True(t, frames)
}

// audio_only without a video ASR model has no text source at all: the video is
// archived, and ffmpeg is never asked for a track nobody can transcribe.
func TestConvertVideoArchivesAudioOnlyWithoutTheVideoASRModel(t *testing.T) {
	t.Parallel()

	extractor := &fakeVideoExtractor{audio: []byte("m4a-bytes")}
	repo := &videoTestKnowledgeRepo{}
	svc := &knowledgeService{
		repo:           repo,
		fileSvc:        &videoTestFileService{content: []byte("mp4-bytes")},
		videoExtractor: extractor,
	}
	knowledge := newVideoTestKnowledge()
	kb := &types.KnowledgeBase{
		ID:          "kb-1",
		VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeAudioOnly},
		ASRConfig:   types.ASRConfig{Enabled: true, ModelID: "asr-audio"},
	}

	_, err := svc.convertVideo(newVideoTestContext(), newVideoTestPayload(knowledge),
		kb, knowledge)

	require.NoError(t, err, "degradation is not a task failure")
	require.Zero(t, extractor.calls, "no video ASR means no narration pass")
	require.Equal(t, 1, repo.updates)
	require.Equal(t, types.ParseStatusStored, repo.columns["parse_status"])
	require.Equal(t, types.ParseStatusStored, knowledge.ParseStatus)
}

// audio_and_keyframes degrades per source: with a VLM but no video ASR model
// the narration is skipped, the frame descriptions stay, and the row is not
// failed.
func TestVideoAudioAndKeyframesWithoutVideoASRKeepsFrameDescriptions(t *testing.T) {
	t.Parallel()

	extractor := &fakeVideoExtractor{audio: []byte("m4a-bytes")}
	repo := &videoTestKnowledgeRepo{}
	svc := &knowledgeService{
		repo:           repo,
		fileSvc:        &videoTestFileService{content: []byte("mp4-bytes")},
		videoExtractor: extractor,
		videoFrameExtractor: &fakeVideoFrameExtractor{
			frames: []videoFrame{{StartMs: 4_000, Data: []byte("jpeg")}},
		},
		modelService: &videoTestModelService{
			vlmModel: &videoTestVLM{replies: []string{"界面：登录页"}},
		},
	}
	knowledge := newVideoTestKnowledge()
	kb := &types.KnowledgeBase{
		ID: "kb-1",
		VideoConfig: types.VideoConfig{
			Enabled:   true,
			Mode:      types.VideoParseModeAudioKeyframes,
			VLMConfig: &types.VLMConfig{Enabled: true, ModelID: "vlm-1"},
		},
		ASRConfig: types.ASRConfig{Enabled: true, ModelID: "asr-audio"},
	}

	result, err := svc.convertVideo(newVideoTestContext(), newVideoTestPayload(knowledge),
		kb, knowledge)

	require.NoError(t, err)
	require.Zero(t, extractor.calls, "the narration source is skipped without a video ASR")
	require.Empty(t, result.AudioData)
	require.Len(t, result.VideoFrames, 1)

	settled, err := svc.transcribeMedia(newVideoTestContext(), kb, knowledge,
		ResolveProcessConfig(kb, nil), result, true)

	require.NoError(t, err)
	require.False(t, settled, "the frame descriptions are still indexable")
	require.Zero(t, repo.updates, "a source-level skip must not fail the row")
	require.Equal(t, types.ParseStatusProcessing, knowledge.ParseStatus)
	require.Equal(t, "[00:00:04] 画面：界面：登录页", result.MarkdownContent)
	require.False(t, result.IsAudio)
	require.Nil(t, result.VideoFrames)
}

// The model and the language hint both come from the video section alone: a
// fully configured audio section must not leak into a video parse.
func TestResolveVideoASRConfigUsesTheVideoSectionOnly(t *testing.T) {
	t.Parallel()

	modelSvc := &videoTestModelService{models: map[string]*types.Model{
		"asr-video": {ID: "asr-video", Type: types.ModelTypeASR},
		"vlm-1":     {ID: "vlm-1", Type: types.ModelTypeVLLM},
	}}
	svc := &knowledgeService{modelService: modelSvc}

	t.Run("video model wins over the audio section", func(t *testing.T) {
		t.Parallel()
		kb := &types.KnowledgeBase{
			VideoConfig: types.VideoConfig{
				Enabled: true, Mode: types.VideoParseModeAudioOnly,
				ModelID: "asr-video", Language: "ja",
			},
			ASRConfig: types.ASRConfig{Enabled: true, ModelID: "asr-audio", Language: "zh"},
		}
		cfg, err := svc.resolveVideoASRConfig(newVideoTestContext(), kb)
		require.NoError(t, err)
		require.Equal(t, "asr-video", cfg.ModelID)
		require.True(t, cfg.Enabled)
		require.Equal(t, "ja", cfg.Language, "the language hint is the video section's own")
	})

	t.Run("empty video model does not borrow the audio section", func(t *testing.T) {
		t.Parallel()
		kb := &types.KnowledgeBase{
			VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeAudioOnly},
			ASRConfig:   types.ASRConfig{Enabled: true, ModelID: "asr-audio", Language: "zh"},
		}
		_, err := svc.resolveVideoASRConfig(newVideoTestContext(), kb)
		require.ErrorIs(t, err, errVideoASRNotConfigured,
			"an audio-section model must not satisfy a video parse")
	})

	t.Run("empty video model keeps auto-detection", func(t *testing.T) {
		t.Parallel()
		kb := &types.KnowledgeBase{
			VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeAudioOnly},
		}
		_, err := svc.resolveVideoASRConfig(newVideoTestContext(), kb)
		require.ErrorIs(t, err, errVideoASRNotConfigured)
	})

	t.Run("no model anywhere", func(t *testing.T) {
		t.Parallel()
		kb := &types.KnowledgeBase{
			VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeAudioOnly},
		}
		_, err := svc.resolveVideoASRConfig(newVideoTestContext(), kb)
		require.ErrorIs(t, err, errVideoASRNotConfigured)
	})

	t.Run("video model must be an ASR model", func(t *testing.T) {
		t.Parallel()
		kb := &types.KnowledgeBase{
			VideoConfig: types.VideoConfig{
				Enabled: true, Mode: types.VideoParseModeAudioOnly, ModelID: "vlm-1",
			},
			// A valid fallback must not be used: a configured-but-wrong model is
			// a configuration error, not a reason to silently borrow another.
			ASRConfig: types.ASRConfig{Enabled: true, ModelID: "asr-audio"},
		}
		_, err := svc.resolveVideoASRConfig(newVideoTestContext(), kb)
		require.Error(t, err)
		require.Contains(t, err.Error(), "not an ASR model")
	})

	t.Run("missing video model is an error", func(t *testing.T) {
		t.Parallel()
		kb := &types.KnowledgeBase{
			VideoConfig: types.VideoConfig{
				Enabled: true, Mode: types.VideoParseModeAudioOnly, ModelID: "ghost",
			},
		}
		_, err := svc.resolveVideoASRConfig(newVideoTestContext(), kb)
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not exist")
	})
}

// A video whose configured model is not an ASR model degrades instead of
// failing: with no frames it archives, with frames it keeps the captions.
func TestTranscribeMediaDegradesANonASRVideoModel(t *testing.T) {
	t.Parallel()

	modelSvc := &videoTestModelService{models: map[string]*types.Model{
		"vlm-1": {ID: "vlm-1", Type: types.ModelTypeVLLM},
	}}

	t.Run("audio_only archives the video", func(t *testing.T) {
		t.Parallel()
		repo := &videoTestKnowledgeRepo{}
		svc := &knowledgeService{repo: repo, modelService: modelSvc}
		knowledge := newVideoTestKnowledge()
		kb := &types.KnowledgeBase{
			ID: "kb-1",
			VideoConfig: types.VideoConfig{
				Enabled: true, Mode: types.VideoParseModeAudioOnly, ModelID: "vlm-1",
			},
		}
		result := &types.ReadResult{IsAudio: true, AudioData: []byte("m4a")}

		settled, err := svc.transcribeMedia(newVideoTestContext(), kb, knowledge,
			ResolveProcessConfig(kb, nil), result, true)

		require.NoError(t, err, "a model type mismatch must not fail the import")
		require.True(t, settled)
		require.Equal(t, types.ParseStatusStored, repo.columns["parse_status"])
		require.Equal(t, types.ParseStatusStored, knowledge.ParseStatus)
	})

	t.Run("audio_and_keyframes keeps the captions", func(t *testing.T) {
		t.Parallel()
		repo := &videoTestKnowledgeRepo{}
		svc := &knowledgeService{repo: repo, modelService: modelSvc}
		knowledge := newVideoTestKnowledge()
		kb := &types.KnowledgeBase{
			ID: "kb-1",
			VideoConfig: types.VideoConfig{
				Enabled: true, Mode: types.VideoParseModeAudioKeyframes, ModelID: "vlm-1",
			},
			VLMConfig: types.VLMConfig{Enabled: true, ModelID: "vlm-1"},
		}
		result := &types.ReadResult{
			IsAudio:   true,
			AudioData: []byte("m4a"),
			VideoFrames: []types.VideoFrameDescription{
				{StartMs: 3_000, Text: "界面：登录页"},
			},
		}

		settled, err := svc.transcribeMedia(newVideoTestContext(), kb, knowledge,
			ResolveProcessConfig(kb, nil), result, true)

		require.NoError(t, err)
		require.False(t, settled, "the frame captions are still indexable")
		require.Zero(t, repo.updates)
		require.False(t, result.IsAudio)
		require.Nil(t, result.AudioData)
		require.Contains(t, result.MarkdownContent, "[00:00:03] 画面：界面：登录页")
	})
}

// audio_and_keyframes must interleave both sources by time and keep a source
// block for every line.
func TestTranscribeMediaMergesNarrationAndFrames(t *testing.T) {
	t.Parallel()

	asrModel := &videoTestASR{result: &asr.TranscriptionResult{Segments: []asr.Segment{
		{Start: 0, End: 2, Text: "开场介绍"},
		{Start: 10, End: 12, Text: "演示操作"},
		{Start: 25, End: 26, Text: "总结"},
	}}}
	modelSvc := &videoTestModelService{
		models:   map[string]*types.Model{"asr-1": {ID: "asr-1", Type: types.ModelTypeASR}},
		asrModel: asrModel,
	}
	svc := &knowledgeService{repo: &videoTestKnowledgeRepo{}, modelService: modelSvc}
	knowledge := newVideoTestKnowledge()
	kb := newAudioKeyframeVideoKB()
	result := &types.ReadResult{
		IsAudio:   true,
		AudioData: []byte("m4a"),
		VideoFrames: []types.VideoFrameDescription{
			{StartMs: 5_000, Text: "界面：登录页"},
			{StartMs: 20_000, Text: "终端：go test"},
		},
	}

	settled, err := svc.transcribeMedia(newVideoTestContext(), kb, knowledge,
		ResolveProcessConfig(kb, nil), result, true)

	require.NoError(t, err)
	require.False(t, settled)
	require.Equal(t, "asr-1", modelSvc.gotASRModelID,
		"the video section's model is the one transcription uses")
	require.Equal(t, []byte("m4a"), asrModel.gotAudio)
	lines := strings.Split(result.MarkdownContent, "\n")
	require.Equal(t, []string{
		"开场介绍",
		"[00:00:05] 画面：界面：登录页",
		"演示操作",
		"[00:00:20] 画面：终端：go test",
		"总结",
	}, lines)
	require.Len(t, result.SourceBlocks, 5)
	require.Equal(t, int64(5_000), result.SourceBlocks[1].Locator.StartMs)
	require.Nil(t, result.VideoFrames, "captions are consumed by the merge")
	chunks := chunker.SplitText(result.MarkdownContent,
		chunker.SplitterConfig{ChunkSize: 512, ChunkOverlap: 0})
	require.NotEmpty(t, chunks)
	var chunked strings.Builder
	for _, chunk := range chunks {
		chunked.WriteString(chunk.Content)
	}
	require.Contains(t, chunked.String(), "开场介绍")
	require.Contains(t, chunked.String(), "[00:00:20] 画面：终端：go test")
}

// Vendors without segment timings leave no way to interleave; the captions are
// appended after the transcript and keep exact offsets.
func TestMergeVideoContentWithoutSegmentsAppendsCaptions(t *testing.T) {
	t.Parallel()

	text, blocks := mergeVideoContent("没有时间轴的旁白", nil,
		[]types.VideoFrameDescription{{StartMs: 5_000, Text: "界面：登录页"}})

	require.Equal(t, "没有时间轴的旁白\n\n[00:00:05] 画面：界面：登录页", text)
	require.Len(t, blocks, 1)
	require.Equal(t, len([]rune("没有时间轴的旁白"))+2, blocks[0].Start)
	require.Equal(t, int64(5_000), blocks[0].Locator.StartMs)
}

// A transcript failure on the last retry keeps the captions instead of throwing
// away a partially readable video.
func TestTranscribeMediaKeepsFramesWhenTranscriptionFails(t *testing.T) {
	t.Parallel()

	decisions := 0
	svc := &knowledgeService{
		repo: &videoTestKnowledgeRepo{},
		modelService: &videoTestModelService{
			models:   map[string]*types.Model{"asr-1": {ID: "asr-1", Type: types.ModelTypeASR}},
			asrModel: &videoTestASR{err: errors.New("vendor 500")},
		},
	}
	knowledge := newVideoTestKnowledge()
	kb := newAudioKeyframeVideoKB()
	result := &types.ReadResult{
		IsAudio:     true,
		AudioData:   []byte("m4a"),
		VideoFrames: []types.VideoFrameDescription{{StartMs: 7_000, Text: "终端：构建失败"}},
	}

	settled, err := svc.transcribeMedia(newVideoTestContext(), kb, knowledge,
		ResolveProcessConfig(kb, nil), result, true)
	decisions++

	require.NoError(t, err)
	require.False(t, settled)
	require.Equal(t, 1, decisions)
	require.Contains(t, result.MarkdownContent, "[00:00:07] 画面：终端：构建失败")
	require.Nil(t, result.VideoFrames)
}

// Readiness only checks presence, but it checks the video section: an audio
// override, an audio-section model, or an image-section VLM cannot make a video
// parseable.
func TestVideoParseReadyUsesTheVideoModelOnly(t *testing.T) {
	t.Parallel()

	audioOnly := &types.KnowledgeBase{
		VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeAudioOnly},
	}
	require.False(t, videoParseReady(audioOnly))

	asrEnabled := true
	audioOverride := &types.KnowledgeProcessOverrides{
		ASRConfig: &types.ASRConfig{Enabled: asrEnabled, ModelID: "asr-override"},
	}
	require.False(t, videoParseReady(audioOnly),
		"an ASR override belongs to audio uploads, not to the video section")
	require.True(t, ResolveProcessConfig(audioOnly, audioOverride).ASRConfig.IsASREnabled(),
		"the override does merge into the effective config; only the video gate ignores it")

	withVideoModel := &types.KnowledgeBase{
		VideoConfig: types.VideoConfig{
			Enabled: true, Mode: types.VideoParseModeAudioOnly, ModelID: "asr-video",
		},
	}
	require.True(t, videoParseReady(withVideoModel))

	keyframes := &types.KnowledgeBase{
		VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeKeyframes},
	}
	require.False(t, videoParseReady(keyframes))
	vlmOverride := &types.KnowledgeProcessOverrides{
		VLMConfig: &types.VLMConfig{Enabled: true, ModelID: "vlm-override"},
	}
	require.True(t, ResolveProcessConfig(keyframes, vlmOverride).VLMConfig.IsEnabled(),
		"the image override merges into the effective config; only the video gate ignores it")
	require.False(t, videoParseReady(keyframes),
		"a VLM override belongs to image parsing; the video keeps its own model")

	keyframesWithImageVLM := &types.KnowledgeBase{
		VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeKeyframes},
		VLMConfig:   types.VLMConfig{Enabled: true, ModelID: "vlm-image"},
	}
	require.False(t, videoParseReady(keyframesWithImageVLM),
		"the image section's VLM must not make a keyframes video parseable")

	keyframesWithVideoVLM := &types.KnowledgeBase{
		VideoConfig: types.VideoConfig{
			Enabled:   true,
			Mode:      types.VideoParseModeKeyframes,
			VLMConfig: &types.VLMConfig{Enabled: true, ModelID: "vlm-video"},
		},
	}
	require.True(t, videoParseReady(keyframesWithVideoVLM),
		"the video section's own VLM makes a keyframes video parseable")

	disabled := &types.KnowledgeBase{
		VideoConfig: types.VideoConfig{Enabled: false, Mode: types.VideoParseModeAudioKeyframes},
		ASRConfig:   types.ASRConfig{Enabled: true, ModelID: "asr-1"},
	}
	require.False(t, videoParseReady(disabled))
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
			ID: "kb-1",
			VideoConfig: types.VideoConfig{
				Enabled: true, Mode: types.VideoParseModeAudioOnly, ModelID: "asr-1",
			},
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

// An opted-in KB that names its own video ASR model needs no audio-section
// model: the video section is authoritative.
func TestCreateStoredVideoParsesWithTheVideoModelOnly(t *testing.T) {
	t.Parallel()

	repo := &createKnowledgeFileRepoStub{}
	task := &createKnowledgeTaskEnqueuerStub{}
	svc := &knowledgeService{
		repo: repo,
		kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{
			ID: "kb-1",
			VideoConfig: types.VideoConfig{
				Enabled: true, Mode: types.VideoParseModeAudioOnly, ModelID: "asr-video",
			},
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
	require.Equal(t, types.ParseStatusPending, knowledge.ParseStatus)
	require.Equal(t, 1, task.calls)
}

// The switch alone cannot produce text: without the video section's model, the
// connector's request keeps its old meaning and the video stays an archive —
// even when the audio section has a perfectly good ASR model.
func TestCreateStoredVideoStaysArchivedWithoutASRModel(t *testing.T) {
	t.Parallel()

	repo := &createKnowledgeFileRepoStub{}
	task := &createKnowledgeTaskEnqueuerStub{}
	svc := &knowledgeService{
		repo: repo,
		kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{
			ID:          "kb-1",
			VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeAudioOnly},
			ASRConfig:   types.ASRConfig{Enabled: true, ModelID: "asr-audio"},
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

	t.Run("rejected when only the audio section has an ASR model", func(t *testing.T) {
		t.Parallel()
		svc := &knowledgeService{
			repo: &createKnowledgeFileRepoStub{},
			kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{
				ID:          "kb-1",
				VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeAudioOnly},
				ASRConfig:   types.ASRConfig{Enabled: true, ModelID: "asr-audio"},
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

	t.Run("parsed when the switch and the video ASR are configured", func(t *testing.T) {
		t.Parallel()
		repo := &createKnowledgeFileRepoStub{}
		task := &createKnowledgeTaskEnqueuerStub{}
		svc := &knowledgeService{
			repo: repo,
			kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{
				ID: "kb-1",
				VideoConfig: types.VideoConfig{
					Enabled: true, Mode: types.VideoParseModeAudioOnly, ModelID: "asr-1",
				},
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

	enabledKB := newAudioOnlyVideoKB()
	keyframesKB := newKeyframeVideoKB()
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
		{name: "video with switch and video ASR", knowledge: video("mp4", "Lesson.mp4"), kb: enabledKB, want: true},
		{name: "video with switch and vlm", knowledge: video("mp4", "Lesson.mp4"), kb: keyframesKB, want: true},
		{name: "video file type empty, name carries it", knowledge: video("", "Lesson.mp4"), kb: enabledKB, want: true},
		{name: "video without the switch", knowledge: video("mp4", "Lesson.mp4"), kb: asrOnlyKB, want: false},
		{name: "video without the video ASR", knowledge: video("mp4", "Lesson.mp4"), kb: &types.KnowledgeBase{
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
			require.Equal(t, tc.want, canUpgradeStoredVideo(tc.knowledge, tc.kb))
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

func TestFFmpegFrameExtractorReportsAMissingBinary(t *testing.T) {
	t.Parallel()

	extractor := &ffmpegFrameExtractor{binary: "weknora-test-ffmpeg-does-not-exist"}
	_, err := extractor.ExtractKeyframes(newVideoTestContext(), bytes.NewReader(nil), "Lesson.mp4")

	require.Error(t, err)
	require.Contains(t, err.Error(), "is not available")
}

// The sampling policy is unit-testable without ffmpeg: a short clip samples
// every second, a long one spreads its cap over the duration, and an unknown
// duration falls back to the conservative default.
func TestVideoKeyframeInterval(t *testing.T) {
	t.Parallel()

	require.Equal(t, 3*time.Second, videoKeyframeInterval(30*time.Second))
	require.Equal(t, time.Second, videoKeyframeInterval(5*time.Second))
	require.Equal(t, 50*time.Second, videoKeyframeInterval(10*time.Minute))
	require.Equal(t, 300*time.Second, videoKeyframeInterval(time.Hour))
	require.Equal(t, videoKeyframeDefaultInterval, videoKeyframeInterval(0))
}

func TestParseFFmpegDuration(t *testing.T) {
	t.Parallel()

	require.Equal(t, 90*time.Second, parseFFmpegDuration(
		"  Duration: 00:01:30.00, start: 0.000000, bitrate: 1234 kb/s"))
	require.Equal(t, 2*time.Hour+3*time.Minute+4500*time.Millisecond, parseFFmpegDuration(
		"Duration: 02:03:04.50, start: 0.000000, bitrate: 1 kb/s"))
	require.Zero(t, parseFFmpegDuration("Duration: N/A, start: 0.000000, bitrate: N/A"))
	require.Zero(t, parseFFmpegDuration("ffmpeg version 6.0"))
	require.Zero(t, parseFFmpegDuration(""))
}
