package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	defaultMaxPlaylistVideos = 200
	defaultMaxAudioMinutes   = 240
	defaultAudioSegmentSecs  = 600
	stderrTailBytes          = 2048
)

var (
	// ErrNoTranscript means the video has no usable captions and no speech
	// recognition model was supplied to transcribe its audio.
	ErrNoTranscript = errors.New(
		"this YouTube video has no captions; configure an ASR (speech recognition) model " +
			"for the knowledge base to transcribe its audio")
	// ErrToolMissing means yt-dlp or ffmpeg is not installed on the server.
	ErrToolMissing = errors.New("yt-dlp/ffmpeg is not installed on the server")

	languageCodePattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,32}$`)
)

// Config controls how the client invokes yt-dlp and ffmpeg.
type Config struct {
	YtDlpBinary  string
	FFmpegBinary string
	// CookiesFile is passed to yt-dlp --cookies; needed when YouTube asks the
	// server to "confirm you're not a bot".
	CookiesFile string
	// JSRuntimes is passed to yt-dlp --js-runtimes (e.g. "node").
	JSRuntimes string
	// MaxPlaylistVideos caps how many videos one playlist import creates.
	MaxPlaylistVideos int
	// MaxAudioDuration caps videos sent to speech recognition.
	MaxAudioDuration time.Duration
	// AudioSegmentDuration is the length of each audio piece sent to the ASR
	// model, keeping uploads under typical API size limits.
	AudioSegmentDuration time.Duration
}

// ConfigFromEnv reads the YOUTUBE_* environment variables.
func ConfigFromEnv() Config {
	return Config{
		YtDlpBinary:       envOr("YOUTUBE_YTDLP_BINARY", "yt-dlp"),
		FFmpegBinary:      envOr("YOUTUBE_FFMPEG_BINARY", "ffmpeg"),
		CookiesFile:       strings.TrimSpace(os.Getenv("YOUTUBE_COOKIES_FILE")),
		JSRuntimes:        strings.TrimSpace(os.Getenv("YOUTUBE_JS_RUNTIMES")),
		MaxPlaylistVideos: envInt("YOUTUBE_PLAYLIST_MAX_VIDEOS", defaultMaxPlaylistVideos),
		MaxAudioDuration: time.Duration(
			envInt("YOUTUBE_MAX_AUDIO_MINUTES", defaultMaxAudioMinutes)) * time.Minute,
		AudioSegmentDuration: time.Duration(
			envInt("YOUTUBE_ASR_SEGMENT_SECONDS", defaultAudioSegmentSecs)) * time.Second,
	}
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil && value > 0 {
		return value
	}
	return fallback
}

// runFunc executes a binary and returns its stdout. It is a field so tests
// can replace process execution.
type runFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

// Client fetches YouTube metadata, captions and audio through yt-dlp.
type Client struct {
	cfg Config
	run runFunc
}

// NewClient builds a client that executes the configured binaries.
func NewClient(cfg Config) *Client {
	if cfg.YtDlpBinary == "" {
		cfg.YtDlpBinary = "yt-dlp"
	}
	if cfg.FFmpegBinary == "" {
		cfg.FFmpegBinary = "ffmpeg"
	}
	if cfg.MaxPlaylistVideos <= 0 {
		cfg.MaxPlaylistVideos = defaultMaxPlaylistVideos
	}
	if cfg.AudioSegmentDuration <= 0 {
		cfg.AudioSegmentDuration = defaultAudioSegmentSecs * time.Second
	}
	return &Client{cfg: cfg, run: runCommand}
}

// Config returns the effective configuration.
func (c *Client) Config() Config {
	return c.cfg
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- fixed binary, args built from validated IDs
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrToolMissing, name)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("%s: %w", name, ctxErr)
		}
		return nil, fmt.Errorf("%s failed: %s", name, commandErrorDetail(stderr.Bytes(), err))
	}
	return stdout.Bytes(), nil
}

// commandErrorDetail prefers yt-dlp's "ERROR:" lines, which name the actual
// problem (private video, bot check, ...), over the generic exit status.
func commandErrorDetail(stderr []byte, err error) string {
	var errorLines []string
	for _, line := range strings.Split(string(stderr), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "ERROR:") {
			errorLines = append(errorLines, strings.TrimSpace(line))
		}
	}
	if len(errorLines) > 0 {
		return strings.Join(errorLines, "; ")
	}
	tail := strings.TrimSpace(string(stderr))
	if len(tail) > stderrTailBytes {
		tail = tail[len(tail)-stderrTailBytes:]
	}
	if tail == "" {
		return err.Error()
	}
	return tail
}

func (c *Client) ytDlpArgs(extra ...string) []string {
	args := []string{"--ignore-config", "--no-update", "--no-warnings", "--no-progress"}
	if c.cfg.CookiesFile != "" {
		args = append(args, "--cookies", c.cfg.CookiesFile)
	}
	if c.cfg.JSRuntimes != "" {
		args = append(args, "--js-runtimes", c.cfg.JSRuntimes)
	}
	return append(args, extra...)
}

// PlaylistEntry is one video in a playlist listing.
type PlaylistEntry struct {
	VideoID string
	Title   string
}

// Playlist is a flat playlist listing.
type Playlist struct {
	ID      string
	Title   string
	Entries []PlaylistEntry
	// Truncated reports that the playlist has more than MaxPlaylistVideos videos.
	Truncated bool
}

type rawPlaylist struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Entries []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"entries"`
}

// Playlist lists the videos of a playlist without downloading them.
func (c *Client) Playlist(ctx context.Context, playlistID string) (*Playlist, error) {
	if !playlistIDPattern.MatchString(playlistID) {
		return nil, fmt.Errorf("invalid YouTube playlist ID %q", playlistID)
	}
	limit := c.cfg.MaxPlaylistVideos
	out, err := c.run(ctx, c.cfg.YtDlpBinary, c.ytDlpArgs(
		"--flat-playlist", "--dump-single-json",
		"--playlist-end", strconv.Itoa(limit+1),
		"--", PlaylistURL(playlistID),
	)...)
	if err != nil {
		return nil, fmt.Errorf("list YouTube playlist: %w", err)
	}
	return parsePlaylist(out, limit)
}

func parsePlaylist(data []byte, limit int) (*Playlist, error) {
	var raw rawPlaylist
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse YouTube playlist listing: %w", err)
	}
	playlist := &Playlist{ID: raw.ID, Title: raw.Title}
	seen := make(map[string]bool, len(raw.Entries))
	for _, entry := range raw.Entries {
		// Private and deleted videos stay in listings as placeholders.
		if !videoIDPattern.MatchString(entry.ID) || seen[entry.ID] ||
			entry.Title == "[Private video]" || entry.Title == "[Deleted video]" {
			continue
		}
		if len(playlist.Entries) == limit {
			playlist.Truncated = true
			break
		}
		seen[entry.ID] = true
		playlist.Entries = append(playlist.Entries, PlaylistEntry{VideoID: entry.ID, Title: entry.Title})
	}
	return playlist, nil
}

type captionTrack struct {
	Ext string `json:"ext"`
}

// VideoInfo is the subset of yt-dlp metadata used to build a document.
type VideoInfo struct {
	ID                string                    `json:"id"`
	Title             string                    `json:"title"`
	Channel           string                    `json:"channel"`
	Uploader          string                    `json:"uploader"`
	UploadDate        string                    `json:"upload_date"`
	Description       string                    `json:"description"`
	Language          string                    `json:"language"`
	Duration          float64                   `json:"duration"`
	LiveStatus        string                    `json:"live_status"`
	Subtitles         map[string][]captionTrack `json:"subtitles"`
	AutomaticCaptions map[string][]captionTrack `json:"automatic_captions"`
}

// ChannelName returns the channel, falling back to the uploader.
func (v *VideoInfo) ChannelName() string {
	if v.Channel != "" {
		return v.Channel
	}
	return v.Uploader
}

// TranscriptSource records where transcript text came from.
type TranscriptSource string

// Transcript sources, from most to least faithful to the speaker.
const (
	// SourceCaptions is a human-made caption track.
	SourceCaptions TranscriptSource = "captions"
	// SourceAutoCaptions is YouTube's automatic speech recognition track.
	SourceAutoCaptions TranscriptSource = "auto_captions"
	// SourceSpeechRecognition is the knowledge base's ASR model over the audio.
	SourceSpeechRecognition TranscriptSource = "speech_recognition"
)

// Transcript is a video's metadata plus its timed text.
type Transcript struct {
	Video    VideoInfo
	Source   TranscriptSource
	Language string
	Segments []Segment
}

// Transcriber turns one audio file into timed segments. Segment start times
// are relative to the beginning of that file.
type Transcriber func(ctx context.Context, audio []byte, fileName string) ([]Segment, error)

// FetchTranscript returns a video's captions, preferring human-made tracks
// in the original language. When the video has no captions and transcribe
// is non-nil, the audio is downloaded, split and transcribed instead.
func (c *Client) FetchTranscript(ctx context.Context, videoID string, transcribe Transcriber) (*Transcript, error) {
	if !videoIDPattern.MatchString(videoID) {
		return nil, fmt.Errorf("invalid YouTube video ID %q", videoID)
	}
	out, err := c.run(ctx, c.cfg.YtDlpBinary, c.ytDlpArgs(
		"--dump-single-json", "--skip-download", "--no-playlist", "--", VideoURL(videoID),
	)...)
	if err != nil {
		return nil, fmt.Errorf("fetch YouTube video info: %w", err)
	}
	var info VideoInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("parse YouTube video info: %w", err)
	}
	if info.LiveStatus == "is_live" || info.LiveStatus == "is_upcoming" {
		return nil, fmt.Errorf("YouTube live streams can be imported once the broadcast has ended")
	}

	workDir, err := os.MkdirTemp("", "weknora-youtube-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	if lang, auto, ok := SelectCaptionTrack(&info); ok {
		segments, err := c.downloadCaptions(ctx, videoID, lang, auto, workDir)
		if err == nil && len(segments) > 0 {
			source := SourceCaptions
			if auto {
				source = SourceAutoCaptions
			}
			return &Transcript{
				Video: info, Source: source, Language: strings.TrimSuffix(lang, "-orig"), Segments: segments,
			}, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		logger.Warnf(ctx, "[YouTube] caption track %s for %s unusable, falling back to audio: %v", lang, videoID, err)
	}

	if transcribe == nil {
		return nil, ErrNoTranscript
	}
	if c.cfg.MaxAudioDuration > 0 && info.Duration > c.cfg.MaxAudioDuration.Seconds() {
		return nil, fmt.Errorf("video is %s long; speech recognition is limited to %s (YOUTUBE_MAX_AUDIO_MINUTES)",
			FormatTimestamp(info.Duration), FormatTimestamp(c.cfg.MaxAudioDuration.Seconds()))
	}
	segments, err := c.transcribeAudio(ctx, videoID, workDir, transcribe)
	if err != nil {
		return nil, err
	}
	return &Transcript{Video: info, Source: SourceSpeechRecognition, Segments: segments}, nil
}

// SelectCaptionTrack picks the caption track to download. Preference order:
// human captions in the original language, auto captions in the original
// language, human English captions, any human captions, English auto captions.
// YouTube auto-translates auto captions into many languages; only the
// original-language track is a real transcript.
func SelectCaptionTrack(info *VideoInfo) (lang string, auto bool, ok bool) {
	manual := trackLanguages(info.Subtitles)
	automatic := trackLanguages(info.AutomaticCaptions)

	original := info.Language
	if original == "" {
		for _, code := range automatic {
			if strings.HasSuffix(code, "-orig") {
				original = strings.TrimSuffix(code, "-orig")
				break
			}
		}
	}
	if original != "" {
		if code, found := matchLanguage(manual, original); found {
			return code, false, true
		}
		if code, found := matchLanguage(automatic, original+"-orig"); found {
			return code, true, true
		}
		if code, found := matchLanguage(automatic, original); found {
			return code, true, true
		}
	}
	if code, found := matchLanguage(manual, "en"); found {
		return code, false, true
	}
	if len(manual) > 0 {
		return manual[0], false, true
	}
	for _, code := range automatic {
		if strings.HasSuffix(code, "-orig") {
			return code, true, true
		}
	}
	if code, found := matchLanguage(automatic, "en"); found {
		return code, true, true
	}
	return "", false, false
}

func trackLanguages(tracks map[string][]captionTrack) []string {
	codes := make([]string, 0, len(tracks))
	for code, formats := range tracks {
		if code == "live_chat" || len(formats) == 0 || !languageCodePattern.MatchString(code) {
			continue
		}
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

// matchLanguage finds want exactly, or a regional variant of it ("en" matches
// "en-US"). Auto-translated "-orig"-less variants are not matched for "-orig".
func matchLanguage(codes []string, want string) (string, bool) {
	for _, code := range codes {
		if strings.EqualFold(code, want) {
			return code, true
		}
	}
	if strings.HasSuffix(want, "-orig") {
		return "", false
	}
	for _, code := range codes {
		if strings.HasPrefix(strings.ToLower(code), strings.ToLower(want)+"-") && !strings.HasSuffix(code, "-orig") {
			return code, true
		}
	}
	return "", false
}

func (c *Client) downloadCaptions(ctx context.Context, videoID, lang string, auto bool, dir string) ([]Segment, error) {
	writeFlag := "--write-subs"
	if auto {
		writeFlag = "--write-auto-subs"
	}
	if _, err := c.run(ctx, c.cfg.YtDlpBinary, c.ytDlpArgs(
		"--skip-download", "--no-playlist", writeFlag,
		"--sub-langs", lang, "--sub-format", "json3/vtt/best",
		"-o", filepath.Join(dir, "captions.%(ext)s"),
		"--", VideoURL(videoID),
	)...); err != nil {
		return nil, fmt.Errorf("download captions: %w", err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "captions.*"))
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("read captions: %w", err)
		}
		switch strings.ToLower(filepath.Ext(file)) {
		case ".json3":
			return ParseJSON3(data)
		case ".vtt":
			return ParseVTT(data)
		}
	}
	return nil, fmt.Errorf("no json3 or vtt caption file was produced for %s", lang)
}

func (c *Client) transcribeAudio(ctx context.Context, videoID, dir string, transcribe Transcriber) ([]Segment, error) {
	if _, err := c.run(ctx, c.cfg.YtDlpBinary, c.ytDlpArgs(
		"--no-playlist", "-f", "bestaudio/best",
		"-o", filepath.Join(dir, "source.%(ext)s"),
		"--", VideoURL(videoID),
	)...); err != nil {
		return nil, fmt.Errorf("download YouTube audio: %w", err)
	}
	sources, _ := filepath.Glob(filepath.Join(dir, "source.*"))
	if len(sources) == 0 {
		return nil, fmt.Errorf("download YouTube audio: no file was produced")
	}

	// Mono 16 kHz 32 kbps keeps a 10-minute piece around 2.4 MB, well under
	// the 25 MB upload limit of OpenAI-compatible transcription APIs.
	segmentSeconds := c.cfg.AudioSegmentDuration.Seconds()
	if _, err := c.run(ctx, c.cfg.FFmpegBinary,
		"-hide_banner", "-loglevel", "error", "-nostdin", "-i", sources[0],
		"-vn", "-ac", "1", "-ar", "16000", "-c:a", "libmp3lame", "-b:a", "32k",
		"-f", "segment", "-segment_time", strconv.Itoa(int(segmentSeconds)), "-reset_timestamps", "1",
		filepath.Join(dir, "part_%04d.mp3"),
	); err != nil {
		return nil, fmt.Errorf("prepare audio for speech recognition: %w", err)
	}
	parts, _ := filepath.Glob(filepath.Join(dir, "part_*.mp3"))
	sort.Strings(parts)
	if len(parts) == 0 {
		return nil, fmt.Errorf("prepare audio for speech recognition: no audio was produced")
	}

	var segments []Segment
	for i, part := range parts {
		audio, err := os.ReadFile(part)
		if err != nil {
			return nil, fmt.Errorf("read audio piece: %w", err)
		}
		pieceSegments, err := transcribe(ctx, audio, fmt.Sprintf("%s_%04d.mp3", videoID, i))
		if err != nil {
			return nil, fmt.Errorf("transcribe audio piece %d/%d: %w", i+1, len(parts), err)
		}
		offset := float64(i) * segmentSeconds
		for _, seg := range pieceSegments {
			seg.Start += offset
			segments = append(segments, seg)
		}
		logger.Infof(ctx, "[YouTube] transcribed audio piece %d/%d for %s", i+1, len(parts), videoID)
	}
	return segments, nil
}
