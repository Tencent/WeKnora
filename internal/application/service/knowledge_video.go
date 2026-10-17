package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/models/video"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// maxVideoAudioBytes caps the audio kept in memory for one ASR call. At
	// the 64 kbit/s mono profile below this is over eight hours of narration;
	// the ceiling exists so an unexpected ffmpeg output degrades the video to
	// an archive instead of exhausting the worker's memory.
	maxVideoAudioBytes = 256 << 20
	// videoAudioStderrBytes is how much ffmpeg diagnostics we keep for the
	// degradation reason. ffmpeg can emit a lot on a broken container.
	videoAudioStderrBytes = 4 << 10
	// videoKeyframeMaxEdge bounds each frame's long edge before it is sent to
	// the VLM. The model does not need more to read a dialog title or a
	// terminal line, and a smaller input is both cheaper and faster.
	videoKeyframeMaxEdge = 1024
	// videoKeyframeJPEGQuality is ffmpeg's -q:v value for the sampled frames:
	// 2 is visually lossless for screenshots at roughly a tenth of PNG size.
	videoKeyframeJPEGQuality = 2
	// videoKeyframeDefaultInterval is the sampling step used when the
	// container does not report a duration. The frame cap still applies, so an
	// unknown duration can only cost fewer samples than the cap allows.
	videoKeyframeDefaultInterval = 10 * time.Second
	// videoProbeStderrBytes bounds the stderr kept from the duration probe.
	// The Duration line sits at the head of the output.
	videoProbeStderrBytes = 8 << 10
	// maxVideoDirectBytes caps the video the direct mode reads into memory. It
	// matches the vendor's per-file ceiling; a larger video degrades rather
	// than tying up the worker's memory for one import.
	maxVideoDirectBytes = 1 << 30
)

// videoAudioExtractor turns the bytes of a stored video into an audio track the
// ASR step can transcribe. It is an interface so the parse pipeline is testable
// without ffmpeg: the behaviour worth pinning is the decision around the
// extractor (which path runs, what happens when it fails), not the codec flags.
type videoAudioExtractor interface {
	Extract(ctx context.Context, src io.Reader, fileName string) ([]byte, error)
}

// videoFrame is one sampled still from a video. StartMs is the capture time
// derived from the sampling interval; it is what gives the caption a
// `[00:01:30]` marker in the merged text.
type videoFrame struct {
	StartMs int64
	Data    []byte
}

// videoFrameSampling is the per-knowledge-base frame sampling policy: the cap
// on how many frames one video contributes and the step between them. Interval
// zero means "derive the step from the duration and MaxFrames", which keeps
// short clips dense and long ones sparse.
type videoFrameSampling struct {
	MaxFrames int
	Interval  time.Duration
}

// videoFrameExtractor samples still frames from a stored video. Like the audio
// extractor it is an interface, so unit tests never shell out to ffmpeg: the
// tests pin the wiring from frames to captions to chunks, not the filter graph.
type videoFrameExtractor interface {
	ExtractKeyframes(
		ctx context.Context, src io.Reader, fileName string, sampling videoFrameSampling,
	) ([]videoFrame, error)
}

// ffmpegAudioExtractor shells out to the ffmpeg binary in the application
// container. The output profile is the one validated by the PoC: mono 16 kHz
// AAC at 64 kbit/s, which every configured ASR model accepts and which keeps a
// one-hour video around 29 MB.
type ffmpegAudioExtractor struct {
	binary  string
	timeout time.Duration
}

// Extract stages the video in a temp file, runs ffmpeg, and returns the audio
// bytes. The temp directory is removed on every path: the source video stays in
// the knowledge store, and the extracted track is only an intermediate.
func (e *ffmpegAudioExtractor) Extract(ctx context.Context, src io.Reader, fileName string) ([]byte, error) {
	binary := strings.TrimSpace(e.binary)
	if binary == "" {
		binary = "ffmpeg"
	}
	if _, err := exec.LookPath(binary); err != nil {
		return nil, fmt.Errorf("ffmpeg binary %q is not available: %w", binary, err)
	}

	workDir, err := os.MkdirTemp("", "weknora-video-*")
	if err != nil {
		return nil, fmt.Errorf("create video work dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	inputPath, err := stageVideoInput(workDir, src, fileName)
	if err != nil {
		return nil, err
	}

	outputPath := filepath.Join(workDir, "audio.m4a")
	runCtx := ctx
	if e.timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}
	stderr := &cappedBuffer{max: videoAudioStderrBytes}
	cmd := exec.CommandContext(runCtx, binary,
		"-y", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", inputPath,
		"-vn", "-ac", "1", "-ar", "16000", "-c:a", "aac", "-b:a", "64k",
		outputPath,
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		// A killed process and a failing process both surface as *ExitError;
		// check the deadlines first so an operator sees "timed out" rather than
		// the opaque "signal: killed".
		if runCtx.Err() != nil {
			if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("ffmpeg audio extraction interrupted: %w", ctx.Err())
			}
			return nil, fmt.Errorf("ffmpeg audio extraction timed out after %s", e.timeout)
		}
		return nil, fmt.Errorf("ffmpeg audio extraction failed: %w (stderr: %s)", err, stderr.String())
	}

	info, err := os.Stat(outputPath)
	if err != nil {
		return nil, fmt.Errorf("stat extracted audio: %w", err)
	}
	if info.Size() == 0 {
		return nil, errors.New("ffmpeg produced an empty audio track")
	}
	if info.Size() > maxVideoAudioBytes {
		return nil, fmt.Errorf("extracted audio is %d bytes, over the %d byte ASR budget",
			info.Size(), int64(maxVideoAudioBytes))
	}
	audio, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("read extracted audio: %w", err)
	}
	return audio, nil
}

// stageVideoInput writes the reader to a temp file with a suffix ffmpeg can
// probe. Both extractors need a seekable local file, and both remove the work
// directory they created.
func stageVideoInput(workDir string, src io.Reader, fileName string) (string, error) {
	inputPath := filepath.Join(workDir, "input"+videoInputExtension(fileName))
	input, err := os.Create(inputPath)
	if err != nil {
		return "", fmt.Errorf("create video staging file: %w", err)
	}
	if _, err := io.Copy(input, src); err != nil {
		_ = input.Close()
		return "", fmt.Errorf("stage video for extraction: %w", err)
	}
	if err := input.Close(); err != nil {
		return "", fmt.Errorf("close staged video: %w", err)
	}
	return inputPath, nil
}

// ffmpegFrameExtractor samples uniformly spaced stills from a video with one
// ffmpeg invocation. Uniform sampling is deliberate: scene detection misses a
// slide that stays on screen for minutes (the common training-video case),
// while a fixed step always covers the whole timeline. The interval is derived
// from the duration so the frame cap lands evenly instead of clustering at the
// head of a long video.
type ffmpegFrameExtractor struct {
	binary  string
	timeout time.Duration
}

// ExtractKeyframes probes the duration, samples at most sampling.MaxFrames
// frames, and returns them with their capture times. The temp directory is
// removed on every path; only the bounded JPEG bytes are kept in memory.
func (e *ffmpegFrameExtractor) ExtractKeyframes(
	ctx context.Context, src io.Reader, fileName string, sampling videoFrameSampling,
) ([]videoFrame, error) {
	sampling = normalizeVideoFrameSampling(sampling)
	binary := strings.TrimSpace(e.binary)
	if binary == "" {
		binary = "ffmpeg"
	}
	if _, err := exec.LookPath(binary); err != nil {
		return nil, fmt.Errorf("ffmpeg binary %q is not available: %w", binary, err)
	}

	workDir, err := os.MkdirTemp("", "weknora-video-frames-*")
	if err != nil {
		return nil, fmt.Errorf("create video work dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	inputPath, err := stageVideoInput(workDir, src, fileName)
	if err != nil {
		return nil, err
	}

	interval := videoKeyframeInterval(e.probeDuration(ctx, binary, inputPath), sampling)
	framesDir := filepath.Join(workDir, "frames")
	if err := os.MkdirAll(framesDir, 0o755); err != nil {
		return nil, fmt.Errorf("create frame directory: %w", err)
	}

	runCtx := ctx
	if e.timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}
	stderr := &cappedBuffer{max: videoAudioStderrBytes}
	// scale=min(edge,iw):min(edge,ih) with force_original_aspect_ratio keeps the
	// aspect ratio, never upscales a small frame, and never lets either edge
	// exceed the cap (including portrait video, where width alone would not).
	filter := fmt.Sprintf(
		"fps=1/%d,scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease",
		int(interval/time.Second), videoKeyframeMaxEdge, videoKeyframeMaxEdge,
	)
	cmd := exec.CommandContext(runCtx, binary,
		"-y", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", inputPath,
		"-vf", filter,
		"-frames:v", strconv.Itoa(sampling.MaxFrames),
		"-q:v", strconv.Itoa(videoKeyframeJPEGQuality),
		filepath.Join(framesDir, "%04d.jpg"),
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if runCtx.Err() != nil {
			if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("ffmpeg keyframe extraction interrupted: %w", ctx.Err())
			}
			return nil, fmt.Errorf("ffmpeg keyframe extraction timed out after %s", e.timeout)
		}
		return nil, fmt.Errorf("ffmpeg keyframe extraction failed: %w (stderr: %s)", err, stderr.String())
	}

	names, err := filepath.Glob(filepath.Join(framesDir, "*.jpg"))
	if err != nil {
		return nil, fmt.Errorf("list sampled frames: %w", err)
	}
	if len(names) == 0 {
		return nil, errors.New("ffmpeg produced no frames")
	}
	sort.Strings(names)

	frames := make([]videoFrame, 0, len(names))
	for i, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read sampled frame %d: %w", i+1, err)
		}
		if len(data) == 0 {
			continue
		}
		frames = append(frames, videoFrame{
			StartMs: int64(i) * int64(interval/time.Millisecond),
			Data:    data,
		})
	}
	if len(frames) == 0 {
		return nil, errors.New("ffmpeg produced no readable frames")
	}
	return frames, nil
}

// probeDuration asks ffmpeg for the container metadata and parses the Duration
// line. The command has no output file, so it exits non-zero by design; the
// stderr it prints anyway is the point. 0 means "unknown", and the caller then
// samples with a conservative default step.
func (e *ffmpegFrameExtractor) probeDuration(
	ctx context.Context, binary, inputPath string,
) time.Duration {
	probeCtx := ctx
	if e.timeout > 0 {
		var cancel context.CancelFunc
		probeCtx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}
	stderr := &cappedBuffer{max: videoProbeStderrBytes}
	cmd := exec.CommandContext(probeCtx, binary, "-nostdin", "-hide_banner", "-i", inputPath)
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr
	_ = cmd.Run()
	return parseFFmpegDuration(stderr.String())
}

// parseFFmpegDuration reads the `Duration: HH:MM:SS.ss` line ffmpeg prints for
// an input. Kept separate from the command so it is exercised without ffmpeg.
func parseFFmpegDuration(stderr string) time.Duration {
	const prefix = "Duration: "
	at := strings.Index(stderr, prefix)
	if at < 0 {
		return 0
	}
	rest := stderr[at+len(prefix):]
	if lineEnd := strings.IndexAny(rest, ",\n"); lineEnd >= 0 {
		rest = rest[:lineEnd]
	}
	value := strings.TrimSpace(rest)
	if value == "" || value == "N/A" {
		return 0
	}
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0
	}
	hours, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return 0
	}
	minutes, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return 0
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	if err != nil {
		return 0
	}
	total := hours*3600 + minutes*60 + seconds
	if total <= 0 {
		return 0
	}
	return time.Duration(total * float64(time.Second))
}

// videoSamplingForConfig reads the knowledge base's frame policy into the shape
// the extractor consumes. Both fields are clamped here again because rows can
// be older than the settings and because a hand-edited JSONB value must never
// reach ffmpeg verbatim.
func videoSamplingForConfig(config types.VideoConfig) videoFrameSampling {
	return normalizeVideoFrameSampling(videoFrameSampling{
		MaxFrames: config.EffectiveMaxFrames(),
		Interval:  time.Duration(config.EffectiveFrameIntervalSeconds()) * time.Second,
	})
}

// normalizeVideoFrameSampling clamps a sampling policy to the range the
// pipeline supports: at least one frame, at most MaxAllowedVideoFrames, and a
// step between one second and MaxVideoFrameIntervalSeconds. Interval zero means
// "auto" and is preserved.
func normalizeVideoFrameSampling(sampling videoFrameSampling) videoFrameSampling {
	switch {
	case sampling.MaxFrames <= 0:
		sampling.MaxFrames = types.DefaultVideoMaxFrames
	case sampling.MaxFrames > types.MaxAllowedVideoFrames:
		sampling.MaxFrames = types.MaxAllowedVideoFrames
	}
	maxInterval := time.Duration(types.MaxVideoFrameIntervalSeconds) * time.Second
	switch {
	case sampling.Interval < 0:
		sampling.Interval = 0
	case sampling.Interval > 0 && sampling.Interval < time.Second:
		sampling.Interval = time.Second
	case sampling.Interval > maxInterval:
		sampling.Interval = maxInterval
	}
	return sampling
}

// videoKeyframeInterval returns the sampling step. An explicit interval wins;
// otherwise the frame cap is spread over the duration, so short clips sample
// every second and a feature-length video samples every few minutes. An unknown
// duration falls back to the conservative default, which the frame cap still
// bounds.
func videoKeyframeInterval(duration time.Duration, sampling videoFrameSampling) time.Duration {
	sampling = normalizeVideoFrameSampling(sampling)
	if sampling.Interval > 0 {
		return sampling.Interval
	}
	if duration <= 0 {
		return videoKeyframeDefaultInterval
	}
	seconds := int(math.Ceil(duration.Seconds() / float64(sampling.MaxFrames)))
	if seconds < 1 {
		seconds = 1
	}
	return time.Duration(seconds) * time.Second
}

// videoInputExtension keeps ffmpeg's format probing deterministic. The bytes
// decide the demuxer in practice, but a sane extension avoids surprises on
// containers whose probe is ambiguous. Anything unrecognised is staged as .mp4
// rather than trusting the client's file name with a path.
func videoInputExtension(fileName string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fileName), "."))
	if IsVideoType(ext) {
		return "." + ext
	}
	return ".mp4"
}

// cappedBuffer keeps the first max bytes written to it and silently drops the
// rest. Write always reports the full length so a child process is never told
// its output was truncated mid-stream (which would look like an I/O error).
type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if b.buf.Len() < b.max {
		remaining := b.max - b.buf.Len()
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.buf.Write(p)
	}
	return written, nil
}

func (b *cappedBuffer) String() string {
	return strings.TrimSpace(b.buf.String())
}

// videoFFmpegSettings resolves the ffmpeg binary and timeout once so the audio
// and frame extractors cannot drift apart; both are deployment settings.
func (s *knowledgeService) videoFFmpegSettings() (string, time.Duration) {
	binary := "ffmpeg"
	timeout := config.DefaultVideoAudioExtractionTimeout
	if s.config != nil && s.config.KnowledgeBase != nil {
		if configured := strings.TrimSpace(s.config.KnowledgeBase.FFmpegPath); configured != "" {
			binary = configured
		}
		if configured := s.config.KnowledgeBase.VideoAudioTimeout; configured > 0 {
			timeout = configured
		}
	}
	return binary, timeout
}

// audioExtractor returns the ffmpeg extractor configured for this deployment.
// Tests inject a fake through knowledgeService.videoExtractor, so this
// constructor is only reached in production wiring.
func (s *knowledgeService) audioExtractor() videoAudioExtractor {
	if s.videoExtractor != nil {
		return s.videoExtractor
	}
	binary, timeout := s.videoFFmpegSettings()
	return &ffmpegAudioExtractor{binary: binary, timeout: timeout}
}

// frameExtractor mirrors audioExtractor for the frame branch. Tests inject a
// fake through knowledgeService.videoFrameExtractor.
func (s *knowledgeService) frameExtractor() videoFrameExtractor {
	if s.videoFrameExtractor != nil {
		return s.videoFrameExtractor
	}
	binary, timeout := s.videoFFmpegSettings()
	return &ffmpegFrameExtractor{binary: binary, timeout: timeout}
}

// videoParseCapabilities reports which text sources this parse can actually
// use. The audio source exists when the mode reads narration and the video
// section itself names an ASR model; the audio section's model does not count,
// because the two sections are configured independently and an implicit
// fallback would hide which model the video actually uses. The frame source
// exists when the mode reads the picture and the video section names its own
// VLM model; the image section's vlm_config does not count, for the same
// reason. The direct source exists when the mode hands the whole file to a
// video understanding model the video section names. The model ids are only
// checked for presence here; resolving and type-checking them happens in the
// worker, where a failure degrades the video instead of rejecting the import.
func videoParseCapabilities(kb *types.KnowledgeBase) (audio, frames, direct bool) {
	if kb == nil {
		return false, false, false
	}
	audio = kb.VideoConfig.AllowsAudioParse() && kb.VideoConfig.HasOwnASRModel()
	frames = kb.VideoConfig.AllowsKeyframeParse() && kb.VideoConfig.FrameVLM().IsEnabled()
	direct = kb.VideoConfig.AllowsDirectParse() && kb.VideoConfig.HasOwnVideoModel()
	return audio, frames, direct
}

// videoDirectFallbackSources reports what a failed video_direct parse may fall
// back to: the narration and frame models the video section still has
// configured. The mode selects the direct read, not these sources, so this is
// deliberately not part of videoParseCapabilities — the import gate must keep
// asking for a video model, and only a failed call may spend the others.
func videoDirectFallbackSources(kb *types.KnowledgeBase) (audio, frames bool) {
	if kb == nil || !kb.VideoConfig.AllowsDirectParse() {
		return false, false
	}
	audio = kb.VideoConfig.HasOwnASRModel()
	frames = kb.VideoConfig.FrameVLM().IsEnabled()
	return audio, frames
}

// videoParseReady reports whether this import can turn a video into text: at
// least one source selected by the mode has a model. Every model belongs to the
// video section, so the knowledge base alone decides; a per-upload image
// override cannot make a video parseable.
func videoParseReady(kb *types.KnowledgeBase) bool {
	audio, frames, direct := videoParseCapabilities(kb)
	return audio || frames || direct
}

// videoParseRequirementMessage explains, for the import gate, which model the
// selected mode is missing. The message is user-facing, so it names the mode's
// actual dependency instead of always saying ASR.
func videoParseRequirementMessage(kb *types.KnowledgeBase) string {
	if kb == nil {
		return "上传视频文件需要先启用视频解析"
	}
	audio := kb.VideoConfig.AllowsAudioParse()
	frames := kb.VideoConfig.AllowsKeyframeParse()
	direct := kb.VideoConfig.AllowsDirectParse()
	switch {
	case audio && frames:
		return "上传视频文件需要先配置视频 ASR 语音识别模型或 VLM 视觉模型"
	case frames:
		return "上传视频文件需要先配置VLM视觉模型"
	case audio:
		return "上传视频文件需要先配置视频 ASR 语音识别模型"
	case direct:
		return "上传视频文件需要先配置视频理解模型"
	default:
		return "上传视频文件需要先启用视频解析"
	}
}

var errVideoASRNotConfigured = errors.New("no ASR model is configured for video parsing")

// resolveVideoASRConfig returns the ASR configuration a video parse must use.
//
// This is the single place the video model is chosen. ResolveProcessConfig is
// deliberately not modified: it is a pure merge used by search and validation
// paths with no model repository, and writing the video model into
// eff.ASRConfig would also redirect audio uploads that share that struct. The
// worker resolves the video model once and hands the result to the same ASR
// step an audio upload uses, so transcription has exactly one implementation.
//
// The video section is authoritative on its own: only video_config.model_id is
// consulted, and an empty value is an error rather than a fallback to the audio
// section. Inheriting the audio model would make the bill and the transcription
// behaviour depend on a setting the operator did not choose for videos. A
// configured model is validated here: it must exist and be an ASR model. A
// wrong-typed or missing model is returned as an error for the caller to
// degrade (archive, or frames when the mode asks for them), never as a failed
// import.
func (s *knowledgeService) resolveVideoASRConfig(
	ctx context.Context, kb *types.KnowledgeBase,
) (types.ASRConfig, error) {
	if kb == nil {
		return types.ASRConfig{}, errors.New("video parsing has no knowledge base")
	}
	modelID := strings.TrimSpace(kb.VideoConfig.ModelID)
	if modelID == "" {
		return types.ASRConfig{}, errVideoASRNotConfigured
	}
	if s.modelService == nil {
		return types.ASRConfig{}, errors.New("model service is not available")
	}
	model, err := s.modelService.GetModelByID(ctx, modelID)
	if err != nil {
		return types.ASRConfig{}, fmt.Errorf("resolve video ASR model %q: %w", modelID, err)
	}
	if model == nil {
		return types.ASRConfig{}, fmt.Errorf("video ASR model %q does not exist", modelID)
	}
	if model.Type != types.ModelTypeASR {
		return types.ASRConfig{}, fmt.Errorf(
			"video ASR model %q is a %s model, not an ASR model", modelID, model.Type)
	}
	// The language hint is the video section's own, for the same reason the
	// model is: the audio section's hint belongs to audio uploads. Empty means
	// auto-detection, the vendors' default.
	return types.ASRConfig{
		Enabled:  true,
		ModelID:  modelID,
		Language: strings.TrimSpace(kb.VideoConfig.Language),
	}, nil
}

var errVideoDirectNotConfigured = errors.New("no video model is configured for video parsing")

// resolveVideoDirectModel returns the video understanding client the direct
// mode must use. It is the single place that model is chosen, mirroring
// resolveVideoASRConfig: only video_config.video_model_id is consulted, and an
// empty value is an error rather than a fallback to the frame VLM. A configured
// model is validated here — it must exist and be a Video row — so a stale or
// wrong-typed id degrades the video instead of failing the import.
func (s *knowledgeService) resolveVideoDirectModel(
	ctx context.Context, kb *types.KnowledgeBase,
) (video.Video, error) {
	if kb == nil {
		return nil, errors.New("video parsing has no knowledge base")
	}
	modelID := strings.TrimSpace(kb.VideoConfig.VideoModelID)
	if modelID == "" {
		return nil, errVideoDirectNotConfigured
	}
	if s.modelService == nil {
		return nil, errors.New("model service is not available")
	}
	model, err := s.modelService.GetModelByID(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("resolve video model %q: %w", modelID, err)
	}
	if model == nil {
		return nil, fmt.Errorf("video model %q does not exist", modelID)
	}
	if model.Type != types.ModelTypeVideo {
		return nil, fmt.Errorf(
			"video model %q is a %s model, not a video model", modelID, model.Type)
	}
	client, err := s.modelService.GetVideoModel(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("build video model %q: %w", modelID, err)
	}
	return client, nil
}

// readVideoDirect reads the stored video into memory and hands it to the video
// model. The bytes are capped at the vendor's per-file ceiling: a larger video
// degrades to the archive (or to the narration and frames) instead of
// exhausting the worker.
func (s *knowledgeService) readVideoDirect(
	ctx context.Context, kb *types.KnowledgeBase, payload types.DocumentProcessPayload,
) (string, error) {
	client, err := s.resolveVideoDirectModel(ctx, kb)
	if err != nil {
		return "", err
	}
	var data []byte
	if err := s.withStoredVideo(ctx, kb, payload.FilePath, func(src io.Reader) error {
		body, readErr := io.ReadAll(io.LimitReader(src, maxVideoDirectBytes+1))
		if readErr != nil {
			return fmt.Errorf("read stored video: %w", readErr)
		}
		if len(body) == 0 {
			return errors.New("stored video is empty")
		}
		if len(body) > maxVideoDirectBytes {
			return fmt.Errorf("video exceeds the %d byte upload ceiling", maxVideoDirectBytes)
		}
		data = body
		return nil
	}); err != nil {
		return "", err
	}

	logger.Infof(ctx, "[video] Sending %s (%d bytes) to the direct video model %s",
		payload.FileName, len(data), client.GetModelName())
	text, err := client.Predict(ctx, data, payload.FileName, buildVideoDirectPrompt(ctx, kb.VideoConfig))
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("the video model returned an empty description")
	}
	return text, nil
}

// buildVideoDirectPrompt is the system-owned prompt of the direct mode. It asks
// for the three things a training video actually carries — what it teaches,
// what appears on screen in order, and what is only said out loud — and forbids
// guessing, because a hallucinated step becomes a retrievable chunk. The
// knowledge base's own requirements are appended afterwards with
// types.AppendCustomPromptInstructions, which keeps the output shape and the
// "say so instead of guessing" rule in force.
func buildVideoDirectPrompt(ctx context.Context, cfg types.VideoConfig) string {
	language := types.LanguageNameFromContext(ctx)
	if language == "" {
		language = "English"
	}
	prompt := fmt.Sprintf(`You are given a complete training video to read, so a text-only reader can follow it
without watching.
Describe the video in the order it happens:
1. What the video teaches and who it is for, in one or two sentences.
2. The steps it demonstrates: the screens, menus, dialogs, buttons, terminals and slides that
   appear, in order, with the visible labels.
3. Warnings, caveats or conclusions that are spoken but not visible on screen.
Only report what the video actually shows or says. If a part is inaudible or unreadable, say so
instead of guessing.
Answer in %s. Write short paragraphs grouped by step, in plain markdown with no code fences
and no more than one heading per group.`, language)
	return types.AppendCustomPromptInstructions(prompt, cfg.DirectPrompt, "video_direct")
}

// withStoredVideo opens a fresh reader on the stored video for one extraction.
// Audio and frames need two passes, and re-opening is cheaper than holding a
// whole video in memory.
func (s *knowledgeService) withStoredVideo(
	ctx context.Context, kb *types.KnowledgeBase, filePath string, use func(io.Reader) error,
) error {
	fileSvc := s.resolveFileServiceForPath(ctx, kb, filePath)
	fileReader, err := fileSvc.GetFile(ctx, filePath)
	if err != nil {
		return fmt.Errorf("read stored video: %w", err)
	}
	defer func() { _ = fileReader.Close() }()
	return use(fileReader)
}

// convertVideo is the video branch of convert(). The docreader has no video
// parser, so the narration is extracted locally (ffmpeg ships in the
// application container) and handed to the same ASR step an audio upload uses.
// When the mode also asks for the picture, frames are sampled and captioned by
// the video section's own VLM; the ASR step interleaves both by time.
//
// The direct mode replaces both of those: the whole file goes to a video
// understanding model in one call, which reads narration and picture together.
//
// Returning (nil, nil) means "handled": the video could not be read and the row
// has been settled back into an archive. Failing the import instead would turn
// every codec problem into a red sync item, when the file itself is still worth
// keeping exactly as it was before video parsing existed. The sources degrade
// independently: a failed audio track still leaves the frame captions, and a
// failed frame pass still leaves the narration. A failed direct read falls back
// to whichever of those two the video section still has configured, and only
// archives when neither produced anything.
//
// The docreader stage was opened by the caller (convert); this method closes it
// as skipped when every source degrades, so the timeline does not claim a parse
// that never happened.
func (s *knowledgeService) convertVideo(
	ctx context.Context,
	payload types.DocumentProcessPayload,
	kb *types.KnowledgeBase,
	knowledge *types.Knowledge,
) (*types.ReadResult, error) {
	wantAudio, wantFrames, wantDirect := videoParseCapabilities(kb)
	if kb.VideoConfig.AllowsKeyframeParse() && !wantFrames {
		// The mode asks for the picture but the video section names no VLM.
		// The skip is deliberate and never borrows the image section's model,
		// so say it out loud instead of leaving an unexplained empty frame
		// list in the stage output.
		logger.Warnf(ctx,
			"[video] Skipping frame descriptions for %s: the video section has no VLM model configured",
			payload.FileName)
	}
	if kb.VideoConfig.AllowsDirectParse() && !wantDirect {
		logger.Warnf(ctx,
			"[video] Skipping the direct read for %s: the video section has no video model configured",
			payload.FileName)
	}
	if !wantAudio && !wantFrames && !wantDirect {
		// The worker gate already rejects this; keep the branch total so a
		// stale task cannot panic or index an empty result.
		s.skipStage(ctx, knowledge.ID, types.StageDocReader, "video_unavailable")
		return s.settleVideoAsArchive(ctx, knowledge,
			"video parsing is disabled or no model is configured for its mode")
	}

	var directErr error
	if wantDirect {
		text, readErr := s.readVideoDirect(ctx, kb, payload)
		if readErr == nil {
			s.endStage(ctx, knowledge.ID, types.StageDocReader, types.JSONMap{
				"mode":         kb.VideoConfig.EffectiveMode(),
				"direct_chars": len([]rune(text)),
				"frame_count":  0,
				"audio_bytes":  0,
			})
			return &types.ReadResult{
				MarkdownContent: text,
				Metadata:        map[string]string{},
			}, nil
		}
		directErr = readErr
		logger.Warnf(ctx, "[video] Direct read unavailable for %s: %v", payload.FileName, readErr)
		// The direct read is the whole parse when it works; when it fails, the
		// narration and frame models the operator already configured are the
		// fallback. They are read only here, never on the happy path, so a
		// successful direct parse costs exactly one model call.
		wantAudio, wantFrames = videoDirectFallbackSources(kb)
		if !wantAudio && !wantFrames {
			s.skipStage(ctx, knowledge.ID, types.StageDocReader, "video_unavailable")
			return s.settleVideoAsArchive(ctx, knowledge,
				"video direct read: "+directErr.Error())
		}
		logger.Warnf(ctx,
			"[video] Falling back to narration/frame parsing for knowledge %s: %v",
			knowledge.ID, directErr)
	}

	var audio []byte
	var audioErr error
	if wantAudio {
		audioErr = s.withStoredVideo(ctx, kb, payload.FilePath, func(src io.Reader) error {
			track, err := s.audioExtractor().Extract(ctx, src, payload.FileName)
			if err != nil {
				return fmt.Errorf("extract audio track: %w", err)
			}
			audio = track
			return nil
		})
		if audioErr != nil {
			logger.Warnf(ctx, "[video] Audio track unavailable for %s: %v", payload.FileName, audioErr)
		}
	}

	var frames []types.VideoFrameDescription
	var framesErr error
	if wantFrames {
		frames, framesErr = s.describeVideoFrames(ctx, kb, payload)
		if framesErr != nil {
			logger.Warnf(ctx, "[video] Frame descriptions unavailable for %s: %v", payload.FileName, framesErr)
		}
	}

	audioReady := len(audio) > 0
	if !audioReady && len(frames) == 0 {
		s.skipStage(ctx, knowledge.ID, types.StageDocReader, "video_unavailable")
		reason := videoDegradeReason(audioErr, framesErr)
		if directErr != nil {
			reason = "video direct read: " + directErr.Error() + "; " + reason
		}
		return s.settleVideoAsArchive(ctx, knowledge, reason)
	}

	stageOutput := types.JSONMap{
		"mode":        kb.VideoConfig.EffectiveMode(),
		"audio_bytes": len(audio),
		"frame_count": len(frames),
	}
	if directErr != nil {
		stageOutput["direct_error"] = directErr.Error()
	}
	if audioErr != nil {
		stageOutput["audio_error"] = audioErr.Error()
	}
	if framesErr != nil {
		stageOutput["frames_error"] = framesErr.Error()
	}
	s.endStage(ctx, knowledge.ID, types.StageDocReader, stageOutput)

	result := &types.ReadResult{Metadata: map[string]string{}}
	if audioReady {
		// The audio placeholder is replaced by the transcription (and the frame
		// captions) in the shared ASR step.
		result.MarkdownContent = fmt.Sprintf("[Audio track: %s]", payload.FileName)
		result.IsAudio = true
		result.AudioData = audio
	}
	if len(frames) > 0 {
		result.VideoFrames = frames
		if !audioReady {
			// No ASR step will run, so the captions are the final text.
			result.MarkdownContent, result.SourceBlocks = formatVideoFrames(frames)
		}
	}
	return result, nil
}

// describeVideoFrames samples frames and captions them with the video
// section's own VLM (video_config.vlm_config.model_id). The image section's
// vlm_config is deliberately not consulted: a knowledge base that never
// enabled image parsing can still read a video's picture, and retuning image
// descriptions must not silently change the video's output. A VLM that cannot
// be resolved, a sampling failure and a failed caption call all degrade the
// frame source, never the import; the caller decides whether narration alone
// is enough.
func (s *knowledgeService) describeVideoFrames(
	ctx context.Context,
	kb *types.KnowledgeBase,
	payload types.DocumentProcessPayload,
) ([]types.VideoFrameDescription, error) {
	var sampled []videoFrame
	sampling := videoSamplingForConfig(kb.VideoConfig)
	if err := s.withStoredVideo(ctx, kb, payload.FilePath, func(src io.Reader) error {
		frames, err := s.frameExtractor().ExtractKeyframes(ctx, src, payload.FileName, sampling)
		if err != nil {
			return fmt.Errorf("extract keyframes: %w", err)
		}
		sampled = frames
		return nil
	}); err != nil {
		return nil, err
	}
	if len(sampled) == 0 {
		return nil, errors.New("no frames were sampled")
	}
	if s.modelService == nil {
		return nil, errors.New("model service is not available")
	}
	frameVLM := kb.VideoConfig.FrameVLM()
	model, err := s.modelService.GetVLMModel(ctx, frameVLM.ModelID)
	if err != nil {
		return nil, fmt.Errorf("resolve video VLM model: %w", err)
	}
	descriptions := s.captionVideoFrames(ctx, model, sampled, buildVideoFramePrompt(ctx, frameVLM))
	if len(descriptions) == 0 {
		return nil, errors.New("no frame could be described by the VLM")
	}
	return descriptions, nil
}

// captionVideoFrames describes each sampled frame with one VLM call. One frame
// per call keeps the timestamp-to-caption mapping unambiguous: a batched call
// would have to rely on the model numbering its answers in order, and one bad
// batch would lose several captions at once. A frame the model fails on is
// dropped with a warning; the remaining frames still produce text.
func (s *knowledgeService) captionVideoFrames(
	ctx context.Context,
	model vlm.VLM,
	frames []videoFrame,
	prompt string,
) []types.VideoFrameDescription {
	descriptions := make([]types.VideoFrameDescription, 0, len(frames))
	for i := range frames {
		text, err := model.Predict(ctx, [][]byte{frames[i].Data}, prompt)
		if err != nil {
			logger.Warnf(ctx, "[video] VLM failed to describe frame %d at %s: %v",
				i+1, formatVideoTimestamp(frames[i].StartMs), err)
			continue
		}
		text = normalizeFrameDescription(text)
		if text == "" {
			logger.Warnf(ctx, "[video] VLM returned an empty description for frame %d at %s",
				i+1, formatVideoTimestamp(frames[i].StartMs))
			continue
		}
		descriptions = append(descriptions, types.VideoFrameDescription{
			StartMs: frames[i].StartMs,
			Text:    text,
		})
	}
	return descriptions
}

// normalizeFrameDescription flattens the model's answer into one line: the
// caption is merged into a line-oriented markdown document, and a stray
// newline or bullet would split the timestamped line away from its content.
func normalizeFrameDescription(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// buildVideoFramePrompt is the system-owned prompt for training-video frames.
// It names the three surfaces a training video actually uses and orders the
// model to answer "unreadable frame" instead of inventing content: a
// hallucinated menu item becomes a retrievable chunk, so guessing is worse than
// admitting a bad frame. Custom image instructions are not reused here — they
// are written for stills, not for a time-ordered video timeline.
//
// The video section's own custom_instructions are appended after this fixed
// block with types.AppendCustomPromptInstructions, which keeps the
// unreadable-frame rule and the output shape in force and tells the model that
// the system rules win. Empty instructions return the prompt unchanged, so a
// knowledge base that never customized anything sends exactly the prompt it
// sent before the field existed.
func buildVideoFramePrompt(ctx context.Context, cfg types.VLMConfig) string {
	language := strings.TrimSpace(cfg.DescriptionLanguage)
	if language == "" {
		language = types.LanguageNameFromContext(ctx)
	}
	if language == "" {
		language = "English"
	}
	prompt := fmt.Sprintf(`You describe frames sampled from a training video so a text-only reader can follow it.
Look at the frame and report, in this order of priority:
1. Software interfaces: which screen, menu, dialog or button is visible, and the readable labels.
2. Terminals: the command being run and the visible output.
3. Slides, whiteboards or diagrams: the title and the key points.
Only state what is clearly visible. If the frame is blank, blurred or unreadable,
say "unreadable frame" instead of guessing.
Answer in %s with at most three short sentences. No preamble, no markdown headings, no bullet points.`, language)
	return types.AppendCustomPromptInstructions(prompt, cfg.CustomInstructions, "video_frame")
}

// formatVideoFrames renders frame captions as the timestamped markdown lines
// that become chunks, plus source blocks that point each line back to its time.
func formatVideoFrames(frames []types.VideoFrameDescription) (string, []types.SourceBlock) {
	if len(frames) == 0 {
		return "", nil
	}
	lines := make([]string, 0, len(frames))
	blocks := make([]types.SourceBlock, 0, len(frames))
	position := 0
	for _, frame := range frames {
		line := videoFrameLine(frame)
		if position > 0 {
			position++ // the joining newline
		}
		start := position
		position += len([]rune(line))
		lines = append(lines, line)
		blocks = append(blocks, types.SourceBlock{
			Start: start,
			End:   position,
			Locator: types.SourceLocator{
				Type:    types.SourceLocatorTime,
				StartMs: frame.StartMs,
				EndMs:   frame.StartMs,
			},
		})
	}
	return strings.Join(lines, "\n"), blocks
}

// videoFrameLine is the chunked form of one caption: the timestamp makes the
// picture line distinguishable from the narration and gives retrieval a time
// anchor.
func videoFrameLine(frame types.VideoFrameDescription) string {
	return fmt.Sprintf("[%s] 画面：%s", formatVideoTimestamp(frame.StartMs), strings.TrimSpace(frame.Text))
}

// formatVideoTimestamp renders milliseconds as HH:MM:SS. Always three fields so
// the marker width is stable in the chunk text.
func formatVideoTimestamp(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	total := ms / 1000
	return fmt.Sprintf("%02d:%02d:%02d", total/3600, (total%3600)/60, total%60)
}

// mergeVideoContent interleaves narration segments and frame captions by time.
// Narration keeps its plain line shape (its source block carries the timing),
// captions carry an explicit `[HH:MM:SS] 画面：` marker because they are pinned
// to an instant rather than to a spoken range.
//
// Vendors that report no segments (or only empty segment texts) leave no timing
// for the narration; in that case the captions are appended after the full
// transcript, because inventing an order would be worse than losing the
// interleaving. Caption blocks are still exact in both paths.
func mergeVideoContent(
	transcript string, segments []asr.Segment, frames []types.VideoFrameDescription,
) (string, []types.SourceBlock) {
	if len(frames) == 0 {
		return transcript, nil
	}
	transcript = strings.TrimSpace(transcript)

	type entry struct {
		startMs int64
		endMs   int64
		text    string
	}
	narration := make([]entry, 0, len(segments))
	for _, segment := range segments {
		text := strings.TrimSpace(segment.Text)
		if text == "" {
			continue
		}
		narration = append(narration, entry{
			startMs: int64(segment.Start * 1000),
			endMs:   int64(segment.End * 1000),
			text:    text,
		})
	}
	if len(narration) == 0 {
		// No timed narration at all (some vendors return only the plain text):
		// the captions stay exact and the untimed transcript goes first.
		frameText, blocks := formatVideoFrames(frames)
		if transcript == "" {
			return frameText, blocks
		}
		if frameText == "" {
			return transcript, nil
		}
		offset := len([]rune(transcript)) + 2 // the blank line between the parts
		for i := range blocks {
			blocks[i].Start += offset
			blocks[i].End += offset
		}
		return transcript + "\n\n" + frameText, blocks
	}

	entries := make([]entry, 0, len(narration)+len(frames))
	entries = append(entries, narration...)
	for _, frame := range frames {
		entries = append(entries, entry{
			startMs: frame.StartMs,
			endMs:   frame.StartMs,
			text:    videoFrameLine(frame),
		})
	}
	// Stable on purpose: narration is appended first, so at an identical
	// timestamp the words come before the frame that was on screen with them.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].startMs < entries[j].startMs })

	var (
		builder  strings.Builder
		blocks   = make([]types.SourceBlock, 0, len(entries))
		position int
	)
	for _, e := range entries {
		if builder.Len() > 0 {
			builder.WriteByte('\n')
			position++
		}
		start := position
		builder.WriteString(e.text)
		position += len([]rune(e.text))
		blocks = append(blocks, types.SourceBlock{
			Start: start,
			End:   position,
			Locator: types.SourceLocator{
				Type:    types.SourceLocatorTime,
				StartMs: e.startMs,
				EndMs:   e.endMs,
			},
		})
	}
	return builder.String(), blocks
}

// degradeVideoToFrames keeps the captions of a video whose narration could not
// be read, so a partially readable video still becomes chunks instead of an
// archive. It reports whether there was anything to keep; when it returns
// false the caller settles the row as an archive.
func (s *knowledgeService) degradeVideoToFrames(
	ctx context.Context, knowledge *types.Knowledge, result *types.ReadResult, reason string,
) bool {
	if result == nil || len(result.VideoFrames) == 0 {
		return false
	}
	logger.Warnf(ctx, "[video] Falling back to frame descriptions for knowledge %s: %s",
		knowledge.ID, reason)
	result.MarkdownContent, result.SourceBlocks = formatVideoFrames(result.VideoFrames)
	result.IsAudio = false
	result.AudioData = nil
	result.VideoFrames = nil
	return true
}

// videoDegradeReason explains why a video ended up with no text at all. Both
// sources are named when both were attempted, because the operator's fix
// differs (codec vs model vs timeout).
func videoDegradeReason(audioErr, framesErr error) string {
	parts := make([]string, 0, 2)
	if audioErr != nil {
		parts = append(parts, audioErr.Error())
	}
	if framesErr != nil {
		parts = append(parts, framesErr.Error())
	}
	if len(parts) == 0 {
		parts = append(parts, "no audio or frame text could be read from the video")
	}
	return strings.Join(parts, "; ")
}

// canUpgradeStoredVideo reports whether a settled archive is a video the
// knowledge base can now read. It is the one exception to "stored entries are
// not re-parseable": turning the video switch on is how the archives that
// already exist become searchable, and the worker keeps "stored" as its floor
// if extraction or captioning fails. The video section owns both of its models,
// so the knowledge base alone decides; per-upload image overrides cannot turn
// an archive into a parse.
func canUpgradeStoredVideo(
	knowledge *types.Knowledge,
	kb *types.KnowledgeBase,
) bool {
	if knowledge == nil || kb == nil {
		return false
	}
	if !IsVideoType(knowledge.FileType) && !IsVideoType(getFileType(knowledge.FileName)) {
		return false
	}
	return videoParseReady(kb)
}

// transcribeMedia is Step 1.5 of the pipeline: it turns the extracted audio
// into text, and for a video merges the narration with the frame captions. It
// reports whether the row was already settled (the caller must stop without an
// error) and an error that should make asynq retry.
func (s *knowledgeService) transcribeMedia(
	ctx context.Context,
	kb *types.KnowledgeBase,
	knowledge *types.Knowledge,
	eff types.EffectiveProcessConfig,
	convertResult *types.ReadResult,
	isLastRetry bool,
) (settled bool, err error) {
	isVideoAudio := IsVideoType(knowledge.FileType)
	asrConfig := eff.ASRConfig
	if isVideoAudio {
		resolved, resolveErr := s.resolveVideoASRConfig(ctx, kb)
		if resolveErr != nil {
			logger.Warnf(ctx, "[video] Cannot resolve the ASR model for knowledge %s: %v",
				knowledge.ID, resolveErr)
			if s.degradeVideoToFrames(ctx, knowledge, convertResult, resolveErr.Error()) {
				return false, nil
			}
			_, settleErr := s.settleVideoAsArchive(ctx, knowledge,
				"resolve video ASR model: "+resolveErr.Error())
			return true, settleErr
		}
		asrConfig = resolved
	} else if !asrConfig.IsASREnabled() {
		logger.Error(ctx, "Audio file detected but ASR is not configured")
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = "ASR model is not configured for audio transcription"
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		return true, nil
	}

	logger.Infof(ctx, "[ASR] Starting audio transcription for knowledge %s, audio size=%d bytes",
		knowledge.ID, len(convertResult.AudioData))

	asrModel, err := s.modelService.GetASRModel(ctx, asrConfig.ModelID)
	if err != nil {
		logger.Errorf(ctx, "[ASR] Failed to get ASR model: %v", err)
		if isVideoAudio {
			if s.degradeVideoToFrames(ctx, knowledge, convertResult,
				"failed to get ASR model: "+err.Error()) {
				return false, nil
			}
			_, settleErr := s.settleVideoAsArchive(ctx, knowledge,
				"failed to get ASR model: "+err.Error())
			return true, settleErr
		}
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = fmt.Sprintf("failed to get ASR model: %v", err)
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		return true, nil
	}

	// The knowledge base's language hint; the vendor declares where it goes,
	// or that it takes none.
	asrCtx := asr.WithLanguage(ctx, asrConfig.Language)
	transcriptionResult, err := asrModel.Transcribe(asrCtx, convertResult.AudioData, knowledge.FileName)
	if err != nil {
		logger.Errorf(ctx, "[ASR] Transcription failed: %v", err)
		if isLastRetry {
			if isVideoAudio {
				if s.degradeVideoToFrames(ctx, knowledge, convertResult,
					"audio transcription failed: "+err.Error()) {
					return false, nil
				}
				_, settleErr := s.settleVideoAsArchive(ctx, knowledge,
					"audio transcription failed: "+err.Error())
				return true, settleErr
			}
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = fmt.Sprintf("audio transcription failed: %v", err)
			knowledge.UpdatedAt = time.Now()
			s.repo.UpdateKnowledge(ctx, knowledge)
		}
		return false, fmt.Errorf("audio transcription failed: %w", err)
	}

	transcribedText, timeBlocks := transcriptWithSegments(transcriptionResult)

	if transcribedText == "" {
		logger.Warn(ctx, "[ASR] Transcription returned empty text")
		if isVideoAudio {
			// No narration means narration contributes nothing; the frame
			// captions (when present) are still worth indexing.
			if s.degradeVideoToFrames(ctx, knowledge, convertResult, "no speech detected in the video audio track") {
				return false, nil
			}
			_, settleErr := s.settleVideoAsArchive(ctx, knowledge,
				"no speech detected in the video audio track")
			return true, settleErr
		}
		transcribedText = "[No speech detected in audio file]"
		timeBlocks = nil
	}

	logger.Infof(ctx, "[ASR] Transcription completed, text length=%d", len(transcribedText))

	if isVideoAudio && len(convertResult.VideoFrames) > 0 {
		// Interleave every narration segment with the captions by time; the
		// resulting blocks keep both the spoken range and the frame instant.
		var segments []asr.Segment
		if transcriptionResult != nil {
			segments = transcriptionResult.Segments
		}
		merged, blocks := mergeVideoContent(
			transcribedText, segments, convertResult.VideoFrames,
		)
		convertResult.MarkdownContent = merged
		convertResult.SourceBlocks = blocks
	} else {
		// Replace the audio placeholder with the transcribed text.
		convertResult.MarkdownContent = transcribedText
		convertResult.SourceBlocks = timeBlocks
	}
	convertResult.IsAudio = false
	convertResult.AudioData = nil
	convertResult.VideoFrames = nil
	return false, nil
}

// settleVideoAsArchive moves a video that cannot be parsed back to the archive
// state and returns (nil, nil) so the pipeline stops without marking the import
// failed. Every caller-level guard (switch off, no model for the mode, ffmpeg
// missing, extraction or transcription error) lands here, which is what keeps
// "the video is stored" the floor instead of "the sync broke".
//
// Stage bookkeeping stays with the caller: extraction failures close the
// docreader stage as skipped, while a transcription failure leaves it done (the
// track was read; only the model failed).
func (s *knowledgeService) settleVideoAsArchive(
	ctx context.Context, knowledge *types.Knowledge, reason string,
) (*types.ReadResult, error) {
	logger.Warnf(ctx, "[video] Falling back to archive for knowledge %s: %s", knowledge.ID, reason)

	now := time.Now()
	if err := s.repo.UpdateKnowledgeColumns(ctx, knowledge.ID, map[string]interface{}{
		"parse_status":  types.ParseStatusStored,
		"enable_status": "disabled",
		"error_message": "",
		"updated_at":    now,
	}); err != nil {
		// The row still says "processing"; acking would strand it there with no
		// task behind it, so surface the error and let asynq retry.
		return nil, fmt.Errorf("settle video knowledge %s as archive: %w", knowledge.ID, err)
	}
	knowledge.ParseStatus = types.ParseStatusStored
	knowledge.EnableStatus = "disabled"
	knowledge.ErrorMessage = ""
	knowledge.UpdatedAt = now
	return nil, nil
}
