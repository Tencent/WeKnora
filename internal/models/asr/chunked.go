package asr

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	// DefaultMaxSegmentDuration is the longest audio one upstream request may
	// carry when the deployment names no value. It is well under the vendor
	// ceiling on purpose: Alibaba documents 300 s for qwen3-asr-flash, but a
	// 485 s recording was refused with "The audio is too long" while 250 s
	// passed, so 120 s — 40 % of the documented ceiling — leaves headroom for
	// a vendor that counts container overhead. It also keeps a two-hour
	// recording at the request ceiling below.
	DefaultMaxSegmentDuration = 120 * time.Second

	// maxAudioSegments bounds what one recording may cost in vendor requests:
	// at the default segment length, two hours of audio. The ceiling is on
	// requests rather than on duration, so a deployment that raises the
	// segment length raises the recording length it accepts with it.
	//
	// Beyond the ceiling Transcribe returns an error instead of transcribing a
	// prefix: a silently truncated transcript looks complete to every
	// downstream consumer, and the knowledge pipeline already has an explicit
	// degradation path for a transcription that fails.
	maxAudioSegments = 60

	// minTrailingSegment is the shortest tail worth a request of its own. A
	// duration that rounds just over a multiple of the segment length
	// (250.004 s, say) would otherwise upload milliseconds of container
	// padding as a segment, and a clip that short can be refused outright —
	// which would fail the whole transcription. The tail is folded into the
	// previous window instead, making that window at most minTrailingSegment
	// longer than the ceiling: 121 s against a documented 300 s.
	minTrailingSegment = 1 * time.Second

	// asrMediaTimeout bounds a single ffprobe or ffmpeg invocation. Cutting
	// one segment of a two-hour recording is far cheaper than this — the
	// input is seeked, not decoded from the start — so a process still
	// running when it expires is wedged, and the fallback below takes over.
	asrMediaTimeout = 5 * time.Minute

	// segmentSampleRate and segmentBitrate are what each piece is encoded at.
	// Mono 16 kHz speech at 64 kbit/s is what the video path already extracts
	// for ASR, and it puts a 120 s piece at roughly 1 MB — a tenth of
	// Alibaba's 10 MB encoded ceiling, which leaves room for a longer
	// configured segment.
	segmentSampleRate = 16000
	segmentBitrate    = "64k"

	// segmentExtension picks the container every piece is encoded into, and
	// with it the vendor request format. MP3 is the only container every
	// recognition vendor in this build lists (Zhipu and MiMo take wav/mp3
	// only), so the cutter always produces the same thing rather than
	// preserving whatever the caller uploaded.
	segmentExtension = ".mp3"

	defaultFFmpegPath  = "ffmpeg"
	defaultFFprobePath = "ffprobe"
)

// mediaSettings are the deployment-wide knobs the cutter reads when a model
// row carries none of its own.
type mediaSettings struct {
	maxSegment  time.Duration
	ffmpegPath  string
	ffprobePath string
}

// mediaDefaults holds the deployment's settings, replaced once at startup.
// The zero value (nothing stored) means the built-in defaults.
var mediaDefaults atomic.Pointer[mediaSettings]

// SetMediaDefaults records the deployment's segment ceiling and media
// binaries. It is called once at startup from the container, before any ASR
// instance exists; a zero field keeps the built-in default. Values on an
// individual Config still win, so a caller can override one model.
func SetMediaDefaults(maxSegment time.Duration, ffmpegPath, ffprobePath string) {
	mediaDefaults.Store(&mediaSettings{
		maxSegment:  maxSegment,
		ffmpegPath:  ffmpegPath,
		ffprobePath: ffprobePath,
	})
}

func currentMediaSettings() mediaSettings {
	stored := mediaSettings{}
	if loaded := mediaDefaults.Load(); loaded != nil {
		stored = *loaded
	}
	if stored.maxSegment <= 0 {
		stored.maxSegment = DefaultMaxSegmentDuration
	}
	stored.ffmpegPath = binaryOrDefault(stored.ffmpegPath, defaultFFmpegPath)
	stored.ffprobePath = binaryOrDefault(stored.ffprobePath, defaultFFprobePath)
	return stored
}

func binaryOrDefault(configured, fallback string) string {
	if trimmed := strings.TrimSpace(configured); trimmed != "" {
		return trimmed
	}
	return fallback
}

// mediaTool is the ffprobe/ffmpeg surface the cutter needs. It is an
// interface so the unit tests drive the merge, the fallbacks and the
// per-segment failures without a binary; ffmpegTool is the real one.
type mediaTool interface {
	// ProbeDuration reports the recording's length.
	ProbeDuration(ctx context.Context, path string) (time.Duration, error)
	// Cut writes [start, start+length) of source to outPath, re-encoded into
	// the container the vendors accept.
	Cut(ctx context.Context, source string, start, length time.Duration, outPath string) error
}

// chunkedASR transcribes a recording the vendor would refuse for its length
// as a series of requests it accepts, and merges the answers into one result.
//
// There is no way around the cut: Alibaba's qwen3-asr-flash stops at five
// minutes (measured: 485 s refused, 250 s accepted) and the model that takes a
// twelve-hour recording — qwen3-asr-flash-filetrans — reads it from a public
// URL, which a private deployment that keeps its videos under /data/files
// cannot offer. Cutting is therefore the only path to narrating a long video,
// and it belongs here, around the vendor client, so every caller gets it
// without knowing: the knowledge pipeline, a chat attachment, a temporary
// document and the model test button all call Transcribe the same way.
type chunkedASR struct {
	inner ASR
	// maxSegment is the longest audio one request may carry.
	maxSegment time.Duration
	// tool measures and cuts the audio. newChunkedASR builds the ffmpeg-backed
	// implementation from the configured binary paths.
	tool mediaTool
}

func (c *chunkedASR) GetModelName() string { return c.inner.GetModelName() }
func (c *chunkedASR) GetModelID() string   { return c.inner.GetModelID() }

// Transcribe measures the audio, and takes the untouched path — same bytes,
// same file name, one vendor call — for anything that fits in one request.
// Only a recording over the ceiling is cut and transcribed piece by piece.
//
// Everything that can go wrong before a piece exists falls back to sending the
// whole file, because the vendor is the better judge of what it accepts: a
// missing ffprobe in a slimmed image, a duration it cannot read, a container it
// cannot seek. Cutting that fails is the same story — the cutter broke, not the
// audio — although the vendor will then refuse a long recording, which is
// exactly today's behaviour. A piece that fails to transcribe is different: it
// is logged and left as an empty gap in the timeline, because the rest of an
// eight-minute narration is still worth indexing, and the pipeline degrades a
// video on an error, not on a gap. Only a run where every piece failed returns
// an error, which is what tells the caller to degrade.
func (c *chunkedASR) Transcribe(
	ctx context.Context, audioBytes []byte, fileName string,
) (*TranscriptionResult, error) {
	if len(audioBytes) == 0 || c.maxSegment <= 0 {
		return c.inner.Transcribe(ctx, audioBytes, fileName)
	}

	workDir, err := os.MkdirTemp("", "weknora-asr-*")
	if err != nil {
		logger.Warnf(ctx, "[ASR] Cannot create a work dir to measure %s: %v; sending it whole",
			fileName, err)
		return c.inner.Transcribe(ctx, audioBytes, fileName)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	// ffprobe needs a seekable file (an mp4 keeps its index at either end) and
	// the cut needs one too, so the upload is staged once for both.
	sourcePath := filepath.Join(workDir, "source"+stageExtension(fileName))
	if err := os.WriteFile(sourcePath, audioBytes, 0o600); err != nil {
		logger.Warnf(ctx, "[ASR] Cannot stage %s: %v; sending it whole", fileName, err)
		return c.inner.Transcribe(ctx, audioBytes, fileName)
	}

	total, err := c.tool.ProbeDuration(ctx, sourcePath)
	if err != nil {
		logger.Warnf(ctx, "[ASR] Cannot measure %s: %v; sending it whole", fileName, err)
		return c.inner.Transcribe(ctx, audioBytes, fileName)
	}
	if total <= c.maxSegment {
		return c.inner.Transcribe(ctx, audioBytes, fileName)
	}

	windows := planSegments(total, c.maxSegment)
	if len(windows) > maxAudioSegments {
		return nil, fmt.Errorf(
			"audio is %s and needs %d requests of %s each, over this deployment's ceiling of %d; "+
				"raise asr_max_segment_duration or send a shorter recording",
			total, len(windows), c.maxSegment, maxAudioSegments)
	}
	logger.Infof(ctx, "[ASR] %s is %s, over the %s request ceiling; transcribing %d segments",
		fileName, total, c.maxSegment, len(windows))

	merged := &TranscriptionResult{
		Duration: total.Seconds(),
		Segments: make([]Segment, 0, len(windows)),
	}
	texts := make([]string, len(windows))
	failed := 0
	var firstErr error

	for index, window := range windows {
		piecePath := filepath.Join(workDir, fmt.Sprintf("part-%03d%s", index, segmentExtension))
		if err := c.tool.Cut(ctx, sourcePath, window.start, window.length, piecePath); err != nil {
			// A cancelled parse must not upload the whole recording as a
			// consolation prize: the vendor request would be cancelled too,
			// after the upload.
			if ctx.Err() != nil {
				return nil, fmt.Errorf("audio segmentation interrupted: %w", ctx.Err())
			}
			logger.Warnf(ctx, "[ASR] Cannot cut %s at %s: %v; sending the whole file instead",
				fileName, window.start, err)
			return c.inner.Transcribe(ctx, audioBytes, fileName)
		}
		piece, err := os.ReadFile(piecePath)
		// The bytes are in memory now; a two-hour recording must not hold all
		// sixty pieces on disk at once.
		_ = os.Remove(piecePath)
		if err != nil {
			logger.Warnf(ctx, "[ASR] Cannot read the segment cut at %s: %v; sending the whole file instead",
				window.start, err)
			return c.inner.Transcribe(ctx, audioBytes, fileName)
		}
		if len(piece) == 0 {
			logger.Warnf(ctx, "[ASR] The segment cut at %s is empty; sending the whole file instead",
				window.start)
			return c.inner.Transcribe(ctx, audioBytes, fileName)
		}

		text := ""
		result, transcribeErr := c.inner.Transcribe(ctx, piece, filepath.Base(piecePath))
		switch {
		case transcribeErr != nil:
			// The caller gave up: the remaining requests would fail the same
			// way, and partial text is worthless to a cancelled parse.
			if ctx.Err() != nil {
				return nil, fmt.Errorf("audio segmentation interrupted: %w", ctx.Err())
			}
			failed++
			if firstErr == nil {
				firstErr = transcribeErr
			}
			logger.Warnf(ctx, "[ASR] Segment %d/%d [%s-%s] failed: %v; keeping the other segments",
				index+1, len(windows), window.start, window.start+window.length, transcribeErr)
		case result != nil:
			text = strings.TrimSpace(result.Text)
		}

		texts[index] = text
		end := window.start + window.length
		if end > total {
			end = total
		}
		merged.Segments = append(merged.Segments, Segment{
			Start: window.start.Seconds(),
			End:   end.Seconds(),
			Text:  text,
		})
	}

	if failed == len(windows) {
		return nil, fmt.Errorf("all %d audio segments failed to transcribe: %w", failed, firstErr)
	}
	merged.Text = joinTranscripts(texts)
	logger.Infof(ctx, "[ASR] Merged %d segments into %d characters (%d failed)",
		len(windows), len([]rune(merged.Text)), failed)
	return merged, nil
}

// segmentWindow is one request's worth of audio: [start, start+length) measured
// from the beginning of the recording.
type segmentWindow struct {
	start  time.Duration
	length time.Duration
}

// planSegments divides a recording into the windows that will be cut.
//
// The boundaries are nominal, not the vendor's: the merge derives every
// timestamp from the offset it asked for, which is more reliable than what the
// vendors return — most of them report no segments at all for these protocols,
// and the two that do report them only for the piece they were given.
func planSegments(total, maxSegment time.Duration) []segmentWindow {
	if total <= 0 || maxSegment <= 0 {
		return nil
	}
	count := int((total + maxSegment - 1) / maxSegment)
	if count > 1 && total-time.Duration(count-1)*maxSegment < minTrailingSegment {
		count--
	}
	windows := make([]segmentWindow, count)
	for index := range windows {
		start := time.Duration(index) * maxSegment
		length := maxSegment
		if index == count-1 {
			length = total - start
		}
		windows[index] = segmentWindow{start: start, length: length}
	}
	return windows
}

// joinTranscripts concatenates the pieces in recording order, skipping the
// empty ones so a failed or silent segment does not leave a blank line.
func joinTranscripts(texts []string) string {
	parts := make([]string, 0, len(texts))
	for _, text := range texts {
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// stageSuffix is the shape of a container suffix worth keeping; see
// stageExtension.
var stageSuffix = regexp.MustCompile(`^\.[a-z0-9]{1,5}$`)

// stageExtension keeps a plausible suffix for the staged copy. ffmpeg
// identifies a container by its content, but its probes still use the suffix
// to guess at raw streams. The name comes from the caller and may be anything
// — the video path hands over an extracted m4a track under the video's own
// .mp4 name — so only a short, alphanumeric suffix is ever used.
func stageExtension(fileName string) string {
	extension := strings.ToLower(filepath.Ext(fileName))
	if stageSuffix.MatchString(extension) {
		return extension
	}
	return ".bin"
}

// ffmpegTool measures and cuts audio with the binary pair from the
// application container. The docreader container has no ffmpeg, which is why
// this runs in the application process and not next to the parser.
type ffmpegTool struct {
	ffmpegPath  string
	ffprobePath string
}

func newFFmpegTool(ffmpegPath, ffprobePath string) *ffmpegTool {
	return &ffmpegTool{
		ffmpegPath:  binaryOrDefault(ffmpegPath, defaultFFmpegPath),
		ffprobePath: binaryOrDefault(ffprobePath, defaultFFprobePath),
	}
}

// probeArgs asks for the container duration and nothing else: no headers, no
// key names, one value on one line.
func probeArgs(path string) []string {
	return []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	}
}

// cutArgs re-encodes one window rather than copying the stream.
//
// Copying cannot produce what the vendors accept here: the pieces would have to
// keep the source codec and container, and Zhipu and MiMo take wav/mp3 only,
// while the video path's source is an AAC track named after the video. Copying
// also cuts at packet boundaries, which is harmless for audio but leaves each
// piece carrying the source's timestamps, and some decoders read a piece that
// starts at 120 s as 120 s of leading silence. Re-encoding sidesteps the
// keyframe question entirely, makes the boundaries exact (the seek is an input
// option, so ffmpeg decodes from the requested instant and only the requested
// length, and the output starts at zero), and gives every vendor the same
// container. The seek is an input option precisely so the cost stays at one
// pass over the recording, not a re-decode from zero for every window, which
// would be quadratic in the number of windows.
func cutArgs(source string, start, length time.Duration, outPath string) []string {
	return []string{
		"-y", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-ss", secondsArg(start),
		"-i", source,
		"-t", secondsArg(length),
		"-vn",
		"-ac", "1", "-ar", strconv.Itoa(segmentSampleRate),
		"-c:a", "libmp3lame", "-b:a", segmentBitrate,
		outPath,
	}
}

// secondsArg formats a position or length for -ss/-t, which take seconds with
// a fraction; a millisecond is well under one encoded frame.
func secondsArg(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 3, 64)
}

func (t *ffmpegTool) ProbeDuration(ctx context.Context, path string) (time.Duration, error) {
	runCtx, cancel := context.WithTimeout(ctx, asrMediaTimeout)
	defer cancel()
	out, err := exec.CommandContext(runCtx, t.ffprobePath, probeArgs(path)...).Output()
	if err != nil {
		if ctx.Err() == nil && runCtx.Err() != nil {
			return 0, fmt.Errorf("ffprobe timed out after %s", asrMediaTimeout)
		}
		return 0, fmt.Errorf("ffprobe failed: %w%s", err, commandStderr(err))
	}
	return parseProbeDuration(string(out))
}

// parseProbeDuration reads ffprobe's one-line answer. A container with no
// duration answers "N/A" — a bare AAC stream, for one — and that is not a
// length to cut a recording at, so it is an error the caller falls back from.
func parseProbeDuration(output string) (time.Duration, error) {
	value := strings.TrimSpace(output)
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
		return 0, fmt.Errorf("ffprobe reported no usable duration (%q)", value)
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func (t *ffmpegTool) Cut(
	ctx context.Context, source string, start, length time.Duration, outPath string,
) error {
	runCtx, cancel := context.WithTimeout(ctx, asrMediaTimeout)
	defer cancel()
	args := cutArgs(source, start, length, outPath)
	if _, err := exec.CommandContext(runCtx, t.ffmpegPath, args...).Output(); err != nil {
		if ctx.Err() == nil && runCtx.Err() != nil {
			return fmt.Errorf("ffmpeg timed out after %s", asrMediaTimeout)
		}
		return fmt.Errorf("ffmpeg failed: %w%s", err, commandStderr(err))
	}
	info, err := os.Stat(outPath)
	if err != nil {
		return fmt.Errorf("ffmpeg wrote no audio: %w", err)
	}
	if info.Size() == 0 {
		return errors.New("ffmpeg wrote an empty segment")
	}
	return nil
}

// commandStderr keeps a failing media command's own diagnostics, which is the
// only place the reason appears, short enough to log: ffmpeg spills thousands
// of lines on a broken container.
func commandStderr(err error) string {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return ""
	}
	message := strings.TrimSpace(string(exitErr.Stderr))
	if message == "" {
		return ""
	}
	if runes := []rune(message); len(runes) > 300 {
		message = string(runes[:300]) + "…"
	}
	return ": " + message
}

// newChunkedASR wraps a vendor client with the cutter. The segment ceiling and
// the binaries come from the model row when it names them and from the
// deployment's settings otherwise, so an operator can move the ceiling for the
// whole process without touching a stored model.
func newChunkedASR(inner ASR, config *Config) ASR {
	settings := currentMediaSettings()
	if config != nil {
		if config.MaxSegmentDuration > 0 {
			settings.maxSegment = config.MaxSegmentDuration
		}
		if path := strings.TrimSpace(config.FFmpegPath); path != "" {
			settings.ffmpegPath = path
		}
		if path := strings.TrimSpace(config.FFprobePath); path != "" {
			settings.ffprobePath = path
		}
	}
	return &chunkedASR{
		inner:      inner,
		maxSegment: settings.maxSegment,
		tool:       newFFmpegTool(settings.ffmpegPath, settings.ffprobePath),
	}
}
