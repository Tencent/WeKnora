package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
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
)

// videoAudioExtractor turns the bytes of a stored video into an audio track the
// ASR step can transcribe. It is an interface so the parse pipeline is testable
// without ffmpeg: the behaviour worth pinning is the decision around the
// extractor (which path runs, what happens when it fails), not the codec flags.
type videoAudioExtractor interface {
	Extract(ctx context.Context, src io.Reader, fileName string) ([]byte, error)
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

	inputPath := filepath.Join(workDir, "input"+videoInputExtension(fileName))
	input, err := os.Create(inputPath)
	if err != nil {
		return nil, fmt.Errorf("create video staging file: %w", err)
	}
	if _, err := io.Copy(input, src); err != nil {
		_ = input.Close()
		return nil, fmt.Errorf("stage video for extraction: %w", err)
	}
	if err := input.Close(); err != nil {
		return nil, fmt.Errorf("close staged video: %w", err)
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

// audioExtractor returns the ffmpeg extractor configured for this deployment.
// Tests inject a fake through knowledgeService.videoExtractor, so this
// constructor is only reached in production wiring.
func (s *knowledgeService) audioExtractor() videoAudioExtractor {
	if s.videoExtractor != nil {
		return s.videoExtractor
	}
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
	return &ffmpegAudioExtractor{binary: binary, timeout: timeout}
}

// videoAudioParseReady reports whether this import can turn a video into text.
// The switch is a knowledge-base setting, but whether an ASR model is available
// is evaluated against the effective config so a per-upload override (the
// confirm dialog lets the caller pick an ASR model) counts the same way the
// pipeline will use it.
func videoAudioParseReady(kb *types.KnowledgeBase, eff types.EffectiveProcessConfig) bool {
	return kb != nil && kb.VideoConfig.AllowsAudioOnlyParse() && eff.ASRConfig.IsASREnabled()
}

// canUpgradeStoredVideo reports whether a settled archive is a video the
// knowledge base can now read. It is the one exception to "stored entries are
// not re-parseable": turning the video switch on is how the archives that
// already exist become searchable, and the worker keeps "stored" as its floor
// if extraction or transcription fails.
func canUpgradeStoredVideo(
	knowledge *types.Knowledge,
	kb *types.KnowledgeBase,
	processOverrides *types.KnowledgeProcessOverrides,
) bool {
	if knowledge == nil || kb == nil {
		return false
	}
	if !IsVideoType(knowledge.FileType) && !IsVideoType(getFileType(knowledge.FileName)) {
		return false
	}
	return videoAudioParseReady(kb, ResolveProcessConfig(kb, processOverrides))
}

// convertVideoToAudio is the video branch of convert(). The docreader has no
// video parser, so the narration is extracted locally (ffmpeg ships in the
// application container) and handed to the same ASR step an audio upload uses;
// everything after that step — chunking, embedding, source blocks with
// timestamps — is shared.
//
// Returning (nil, nil) means "handled": the video could not be read and the row
// has been settled back into an archive. Failing the import instead would turn
// every codec problem into a red sync item, when the file itself is still
// worth keeping exactly as it was before video parsing existed.
//
// The docreader stage was opened by the caller (convert); this method closes it
// as skipped when extraction degrades, so the timeline does not claim a parse
// that never happened.
func (s *knowledgeService) convertVideoToAudio(
	ctx context.Context,
	payload types.DocumentProcessPayload,
	kb *types.KnowledgeBase,
	knowledge *types.Knowledge,
) (*types.ReadResult, error) {
	fileSvc := s.resolveFileServiceForPath(ctx, kb, payload.FilePath)
	fileReader, err := fileSvc.GetFile(ctx, payload.FilePath)
	if err != nil {
		s.skipStage(ctx, knowledge.ID, types.StageDocReader, "video_audio_unavailable")
		return s.settleVideoAsArchive(ctx, knowledge, "read stored video: "+err.Error())
	}
	defer func() { _ = fileReader.Close() }()

	audio, err := s.audioExtractor().Extract(ctx, fileReader, payload.FileName)
	if err != nil {
		s.skipStage(ctx, knowledge.ID, types.StageDocReader, "video_audio_unavailable")
		return s.settleVideoAsArchive(ctx, knowledge, "extract audio track: "+err.Error())
	}

	logger.Infof(ctx, "[video] Extracted %d audio bytes from %s (knowledge=%s)",
		len(audio), payload.FileName, knowledge.ID)
	s.endStage(ctx, knowledge.ID, types.StageDocReader, types.JSONMap{
		"is_audio":    true,
		"audio_bytes": len(audio),
		"mode":        types.VideoParseModeAudioOnly,
	})

	// The audio placeholder is replaced by the transcription in the shared ASR
	// step; keeping it here means a model that returns nothing still leaves a
	// trace of what was read.
	return &types.ReadResult{
		MarkdownContent: fmt.Sprintf("[Audio track: %s]", payload.FileName),
		IsAudio:         true,
		AudioData:       audio,
	}, nil
}

// settleVideoAsArchive moves a video that cannot be transcribed back to the
// archive state and returns (nil, nil) so the pipeline stops without marking
// the import failed. Every caller-level guard (switch off, ASR unset, ffmpeg
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
