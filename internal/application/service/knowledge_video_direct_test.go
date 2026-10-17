package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// videoTestDirectModel stands in for the vendor client. The pipeline tests pin
// what it is handed (the stored bytes, the filename, the prompt) and what the
// caller does with its answer or its failure, not the upload protocol — that is
// pinned by the client's own tests.
type videoTestDirectModel struct {
	text      string
	err       error
	calls     int
	gotBytes  []byte
	gotName   string
	gotPrompt string
}

func (m *videoTestDirectModel) Predict(
	ctx context.Context, videoBytes []byte, fileName, prompt string,
) (string, error) {
	m.calls++
	m.gotBytes = videoBytes
	m.gotName = fileName
	m.gotPrompt = prompt
	if m.err != nil {
		return "", m.err
	}
	return m.text, nil
}

func (m *videoTestDirectModel) PredictURL(context.Context, string, string) (string, error) {
	return "", errors.New("PredictURL is not part of this test")
}

func (m *videoTestDirectModel) GetModelName() string { return "video-test-direct" }
func (m *videoTestDirectModel) GetModelID() string   { return "video-1" }

func newDirectVideoKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID: "kb-1",
		VideoConfig: types.VideoConfig{
			Enabled:      true,
			Mode:         types.VideoParseModeDirect,
			VideoModelID: "video-1",
			DirectPrompt: "重点关注菜单名称。",
		},
	}
}

func newDirectVideoModelService() *videoTestModelService {
	return &videoTestModelService{
		models: map[string]*types.Model{"video-1": {ID: "video-1", Type: types.ModelTypeVideo}},
	}
}

// The happy path: the whole file goes to the video model and its answer becomes
// the document text. Narration and frames are never touched, so a direct parse
// costs exactly one model call.
func TestConvertVideoDirectModeReturnsTheModelText(t *testing.T) {
	t.Parallel()

	direct := &videoTestDirectModel{text: "第一段：配置模型。\n\n第二段：上传视频。"}
	modelService := newDirectVideoModelService()
	modelService.videoModel = direct
	repo := &videoTestKnowledgeRepo{}
	extractor := &fakeVideoExtractor{audio: []byte("m4a")}
	svc := &knowledgeService{
		repo:           repo,
		fileSvc:        &videoTestFileService{content: []byte("mp4-bytes")},
		videoExtractor: extractor,
		modelService:   modelService,
	}
	knowledge := newVideoTestKnowledge()
	kb := newDirectVideoKB()

	result, err := svc.convertVideo(newVideoTestContext(), newVideoTestPayload(knowledge), kb, knowledge)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, direct.text, result.MarkdownContent)
	require.False(t, result.IsAudio, "direct mode has no ASR step")
	require.Nil(t, result.AudioData)
	require.Empty(t, result.VideoFrames)

	require.Equal(t, 1, direct.calls)
	require.Equal(t, []byte("mp4-bytes"), direct.gotBytes, "the stored video is what gets uploaded")
	require.Equal(t, "Lesson.mp4", direct.gotName)
	require.Contains(t, direct.gotPrompt, "重点关注菜单名称。",
		"the knowledge base's own requirements reach the model")
	require.Equal(t, "video-1", modelService.gotVideoModelID)
	require.Zero(t, extractor.calls, "a direct parse must not shell out to ffmpeg")
	require.Zero(t, repo.updates, "a successful direct read must not rewrite the row")
}

// A failed direct read falls back to the narration the video section still has
// configured, and the row goes through the normal ASR step.
func TestConvertVideoDirectModeFallsBackToNarration(t *testing.T) {
	t.Parallel()

	modelService := newDirectVideoModelService()
	modelService.videoModel = &videoTestDirectModel{err: errors.New("upstream 400")}
	repo := &videoTestKnowledgeRepo{}
	extractor := &fakeVideoExtractor{audio: []byte("m4a-audio")}
	svc := &knowledgeService{
		repo:           repo,
		fileSvc:        &videoTestFileService{content: []byte("mp4-bytes")},
		videoExtractor: extractor,
		modelService:   modelService,
	}
	knowledge := newVideoTestKnowledge()
	kb := newDirectVideoKB()
	kb.VideoConfig.ModelID = "asr-video"
	modelService.models["asr-video"] = &types.Model{ID: "asr-video", Type: types.ModelTypeASR}

	result, err := svc.convertVideo(newVideoTestContext(), newVideoTestPayload(knowledge), kb, knowledge)

	require.NoError(t, err, "degradation is not a task failure")
	require.NotNil(t, result)
	require.True(t, result.IsAudio, "the fallback hands the track to the ASR step")
	require.Equal(t, []byte("m4a-audio"), result.AudioData)
	require.Equal(t, 1, extractor.calls)
	require.Zero(t, repo.updates, "the fallback produced text, so the row keeps processing")
}

// A failed direct read falls back to the frame descriptions when the video
// section has a VLM but no ASR model.
func TestConvertVideoDirectModeFallsBackToFrames(t *testing.T) {
	t.Parallel()

	modelService := newDirectVideoModelService()
	modelService.videoModel = &videoTestDirectModel{err: errors.New("timed out")}
	modelService.vlmModel = &videoTestVLM{replies: []string{"打开设置页面"}}
	repo := &videoTestKnowledgeRepo{}
	svc := &knowledgeService{
		repo:    repo,
		fileSvc: &videoTestFileService{content: []byte("mp4-bytes")},
		videoFrameExtractor: &fakeVideoFrameExtractor{
			frames: []videoFrame{{StartMs: 0, Data: []byte("jpeg")}},
		},
		modelService: modelService,
	}
	knowledge := newVideoTestKnowledge()
	kb := newDirectVideoKB()
	kb.VideoConfig.VLMConfig = &types.VLMConfig{Enabled: true, ModelID: "vlm-1"}

	result, err := svc.convertVideo(newVideoTestContext(), newVideoTestPayload(knowledge), kb, knowledge)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsAudio)
	require.Len(t, result.VideoFrames, 1)
	require.Contains(t, result.MarkdownContent, "打开设置页面")
	require.Zero(t, repo.updates)
}

// A pure video_direct video whose model fails and that has no other model
// configured settles back into the archive instead of failing the import.
func TestConvertVideoDirectModeArchivesWhenNothingElseIsConfigured(t *testing.T) {
	t.Parallel()

	modelService := newDirectVideoModelService()
	modelService.videoModel = &videoTestDirectModel{err: errors.New("model does not support video")}
	repo := &videoTestKnowledgeRepo{}
	svc := &knowledgeService{
		repo:         repo,
		fileSvc:      &videoTestFileService{content: []byte("mp4-bytes")},
		modelService: modelService,
	}
	knowledge := newVideoTestKnowledge()

	_, err := svc.convertVideo(newVideoTestContext(), newVideoTestPayload(knowledge),
		newDirectVideoKB(), knowledge)

	require.NoError(t, err, "degradation is not a task failure")
	require.Equal(t, 1, repo.updates)
	require.Equal(t, types.ParseStatusStored, repo.columns["parse_status"])
	require.Equal(t, types.ParseStatusStored, knowledge.ParseStatus)
}

// The direct mode needs a video model configured; without one the video is
// archived and no file is even read.
func TestConvertVideoDirectModeWithoutModelArchives(t *testing.T) {
	t.Parallel()

	repo := &videoTestKnowledgeRepo{}
	fileSvc := &videoTestFileService{content: []byte("mp4-bytes")}
	svc := &knowledgeService{repo: repo, fileSvc: fileSvc, modelService: newDirectVideoModelService()}
	knowledge := newVideoTestKnowledge()
	kb := newDirectVideoKB()
	kb.VideoConfig.VideoModelID = ""

	_, err := svc.convertVideo(newVideoTestContext(), newVideoTestPayload(knowledge), kb, knowledge)

	require.NoError(t, err)
	require.Equal(t, types.ParseStatusStored, repo.columns["parse_status"])
}

// A stale id that points at a non-video row degrades the same way: the model
// type is part of the contract, and a chat model cannot be handed a video.
func TestConvertVideoDirectModeRejectsAWrongModelType(t *testing.T) {
	t.Parallel()

	modelService := newDirectVideoModelService()
	modelService.models["video-1"] = &types.Model{ID: "video-1", Type: types.ModelTypeKnowledgeQA}
	repo := &videoTestKnowledgeRepo{}
	svc := &knowledgeService{
		repo:         repo,
		fileSvc:      &videoTestFileService{content: []byte("mp4-bytes")},
		modelService: modelService,
	}
	knowledge := newVideoTestKnowledge()

	_, err := svc.convertVideo(newVideoTestContext(), newVideoTestPayload(knowledge),
		newDirectVideoKB(), knowledge)

	require.NoError(t, err)
	require.Equal(t, 1, repo.updates)
	require.Equal(t, types.ParseStatusStored, repo.columns["parse_status"])
}

// The direct source counts for the import gate only when the mode selects it
// AND the video section names its own video model; other sections' models are
// not a substitute.
func TestVideoParseCapabilitiesDirectMode(t *testing.T) {
	t.Parallel()

	direct := &types.KnowledgeBase{
		VideoConfig: types.VideoConfig{Enabled: true, Mode: types.VideoParseModeDirect},
	}
	audio, frames, hasDirect := videoParseCapabilities(direct)
	require.False(t, audio)
	require.False(t, frames)
	require.False(t, hasDirect, "no video model means no direct source")
	require.False(t, videoParseReady(direct))

	withModel := &types.KnowledgeBase{
		VideoConfig: types.VideoConfig{
			Enabled: true, Mode: types.VideoParseModeDirect, VideoModelID: "video-1",
		},
	}
	audio, frames, hasDirect = videoParseCapabilities(withModel)
	require.False(t, audio)
	require.False(t, frames)
	require.True(t, hasDirect)
	require.True(t, videoParseReady(withModel))

	// Switching to video_direct stops the other two sources from being selected,
	// even when their models are still configured: the direct read is the whole
	// parse, and the others exist only as its fallback.
	switched := &types.KnowledgeBase{
		VideoConfig: types.VideoConfig{
			Enabled:      true,
			Mode:         types.VideoParseModeDirect,
			VideoModelID: "video-1",
			ModelID:      "asr-video",
			VLMConfig:    &types.VLMConfig{Enabled: true, ModelID: "vlm-video"},
		},
	}
	audio, frames, hasDirect = videoParseCapabilities(switched)
	require.False(t, audio, "the mode no longer reads narration directly")
	require.False(t, frames, "the mode no longer samples frames directly")
	require.True(t, hasDirect)

	fallbackAudio, fallbackFrames := videoDirectFallbackSources(switched)
	require.True(t, fallbackAudio, "a failed direct read may use the configured ASR model")
	require.True(t, fallbackFrames, "a failed direct read may use the configured VLM")

	// A non-direct mode has no fallback: it already selects what it reads.
	fallbackAudio, fallbackFrames = videoDirectFallbackSources(withModel)
	require.False(t, fallbackAudio)
	require.False(t, fallbackFrames)
}

// The built-in prompt keeps its rules and the knowledge base's requirements are
// appended, without letting them replace the output contract.
func TestBuildVideoDirectPromptAppendsTheKnowledgeBaseRequirements(t *testing.T) {
	t.Parallel()

	base := buildVideoDirectPrompt(context.Background(), types.VideoConfig{})
	require.Contains(t, base, "complete training video")
	require.NotContains(t, base, "video_direct_business_instructions")

	withInstructions := buildVideoDirectPrompt(context.Background(), types.VideoConfig{
		DirectPrompt: "只看第三章之后的画面。",
	})
	require.Contains(t, withInstructions, "<video_direct_business_instructions>")
	require.Contains(t, withInstructions, "只看第三章之后的画面。")
	require.Contains(t, withInstructions, "system-owned output format")
	require.True(t, strings.HasPrefix(withInstructions, "You are given"), "系统的输出格式仍然在前")
}
