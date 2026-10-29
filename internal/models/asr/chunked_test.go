package asr

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeASR records every vendor call the cutter makes and answers with whatever
// the test asks for, so the tests pin the calls as well as the merge.
type fakeASR struct {
	calls  []fakeCall
	answer func(ctx context.Context, call fakeCall, index int) (*TranscriptionResult, error)
}

type fakeCall struct {
	fileName string
	audio    []byte
}

func (f *fakeASR) GetModelName() string { return "fake-asr" }
func (f *fakeASR) GetModelID() string   { return "fake-asr-id" }

func (f *fakeASR) Transcribe(
	ctx context.Context, audio []byte, fileName string,
) (*TranscriptionResult, error) {
	call := fakeCall{fileName: fileName, audio: append([]byte(nil), audio...)}
	f.calls = append(f.calls, call)
	if f.answer == nil {
		return &TranscriptionResult{Text: "text:" + fileName}, nil
	}
	return f.answer(ctx, call, len(f.calls)-1)
}

// fakeMediaTool stands in for ffprobe and ffmpeg: it answers the duration the
// test gives it and writes distinguishable bytes for each window it is asked
// to cut.
type fakeMediaTool struct {
	duration time.Duration
	probeErr error
	probes   int

	cuts     []fakeCut
	cutErr   error
	cutErrAt map[int]error
	piece    func(index int) []byte
}

type fakeCut struct {
	start  time.Duration
	length time.Duration
	out    string
}

func (f *fakeMediaTool) ProbeDuration(context.Context, string) (time.Duration, error) {
	f.probes++
	return f.duration, f.probeErr
}

func (f *fakeMediaTool) Cut(
	_ context.Context, _ string, start, length time.Duration, outPath string,
) error {
	index := len(f.cuts)
	f.cuts = append(f.cuts, fakeCut{start: start, length: length, out: outPath})
	if err := f.cutErrAt[index]; err != nil {
		return err
	}
	if f.cutErr != nil {
		return f.cutErr
	}
	body := []byte("piece-" + strconv.Itoa(index))
	if f.piece != nil {
		body = f.piece(index)
	}
	return os.WriteFile(outPath, body, 0o600)
}

func newTestChunked(inner ASR, tool mediaTool, maxSegment time.Duration) *chunkedASR {
	return &chunkedASR{inner: inner, maxSegment: maxSegment, tool: tool}
}

// captureLogs makes logger.Warnf observable so the degradation paths can be
// asserted rather than merely inspected.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buffer := &bytes.Buffer{}
	logger.SetOutput(buffer)
	t.Cleanup(func() { logger.SetOutput(os.Stdout) })
	return buffer
}

func cutWindows(cuts []fakeCut) [][2]time.Duration {
	windows := make([][2]time.Duration, len(cuts))
	for index, cut := range cuts {
		windows[index] = [2]time.Duration{cut.start, cut.length}
	}
	return windows
}

// Audio that fits in one request must reach the vendor exactly as it always
// did: same bytes, same file name, and the vendor's own answer passed through
// without a synthesised duration or segments.
func TestShortAudioTakesTheUntouchedPath(t *testing.T) {
	tests := []struct {
		name       string
		duration   time.Duration
		probeErr   error
		audio      []byte
		wantProbes int
	}{
		{
			name: "under the ceiling", duration: 64 * time.Second,
			audio: []byte("sixty-four seconds"), wantProbes: 1,
		},
		{
			name: "exactly at the ceiling", duration: 120 * time.Second,
			audio: []byte("two minutes"), wantProbes: 1,
		},
		{
			name: "duration unknown", duration: 0,
			audio: []byte("no duration"), wantProbes: 1,
		},
		{
			name: "probe failed", probeErr: errors.New("ffprobe: invalid data"),
			audio: []byte("undecodable"), wantProbes: 1,
		},
		{
			name: "empty audio", audio: nil, wantProbes: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inner := &fakeASR{}
			tool := &fakeMediaTool{duration: tc.duration, probeErr: tc.probeErr}
			got, err := newTestChunked(inner, tool, 120*time.Second).
				Transcribe(context.Background(), tc.audio, "talk.mp3")
			require.NoError(t, err)

			require.Len(t, inner.calls, 1, "the vendor must see exactly one request")
			assert.Equal(t, "talk.mp3", inner.calls[0].fileName)
			assert.Equal(t, tc.audio, inner.calls[0].audio)
			assert.Equal(t, tc.wantProbes, tool.probes)
			assert.Empty(t, tool.cuts, "nothing may be cut")
			assert.Equal(t, &TranscriptionResult{Text: "text:talk.mp3"}, got,
				"the vendor's answer must come back untouched")
		})
	}
}

// A 250 s recording with a 120 s ceiling is three requests, in order, and the
// merged timeline is derived from the offsets rather than from a vendor reply.
func TestLongAudioIsCutMergedAndRetimed(t *testing.T) {
	inner := &fakeASR{answer: func(_ context.Context, call fakeCall, _ int) (*TranscriptionResult, error) {
		// Deliberately padded: the merge trims, so the transcripts join with a
		// single newline and nothing else.
		return &TranscriptionResult{Text: " " + string(call.audio) + " "}, nil
	}}
	tool := &fakeMediaTool{duration: 250 * time.Second}

	got, err := newTestChunked(inner, tool, 120*time.Second).
		Transcribe(context.Background(), []byte("the whole 250 seconds"), "lecture.m4a")
	require.NoError(t, err)

	require.Equal(t, [][2]time.Duration{
		{0, 120 * time.Second},
		{120 * time.Second, 120 * time.Second},
		{240 * time.Second, 10 * time.Second},
	}, cutWindows(tool.cuts), "each window is cut at its own offset")

	require.Len(t, inner.calls, 3)
	names := []string{inner.calls[0].fileName, inner.calls[1].fileName, inner.calls[2].fileName}
	assert.Equal(t, []string{"part-000.mp3", "part-001.mp3", "part-002.mp3"}, names)
	for index, call := range inner.calls {
		assert.Equal(t, []byte("piece-"+strconv.Itoa(index)), call.audio,
			"each request carries its own window, not the whole file")
	}

	assert.Equal(t, "piece-0\npiece-1\npiece-2", got.Text)
	assert.Equal(t, []Segment{
		{Start: 0, End: 120, Text: "piece-0"},
		{Start: 120, End: 240, Text: "piece-1"},
		{Start: 240, End: 250, Text: "piece-2"},
	}, got.Segments)
	assert.Equal(t, 250.0, got.Duration)
}

// ffprobe missing, or a container whose duration it cannot read, must not stop
// the transcription: the vendor decides, as it did before this decorator.
func TestProbeFailureSendsTheWholeFile(t *testing.T) {
	inner := &fakeASR{}
	tool := &fakeMediaTool{probeErr: errors.New(`ffprobe reported no usable duration ("N/A")`)}

	got, err := newTestChunked(inner, tool, 120*time.Second).
		Transcribe(context.Background(), []byte("whole"), "clip.aac")
	require.NoError(t, err)
	require.Len(t, inner.calls, 1)
	assert.Equal(t, "clip.aac", inner.calls[0].fileName)
	assert.Equal(t, []byte("whole"), inner.calls[0].audio)
	assert.Empty(t, tool.cuts)
	assert.Equal(t, "text:clip.aac", got.Text)
}

// A broken cutter is not a broken recording: the whole file still goes to the
// vendor, which then refuses it for its length — today's behaviour rather than
// a new failure mode.
func TestCutFailureSendsTheWholeFile(t *testing.T) {
	tests := []struct {
		name      string
		failAt    int
		wantCuts  int
		wantCalls int
	}{
		{name: "first window", failAt: 0, wantCuts: 1, wantCalls: 1},
		{name: "last window", failAt: 2, wantCuts: 3, wantCalls: 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inner := &fakeASR{}
			tool := &fakeMediaTool{
				duration: 250 * time.Second,
				cutErrAt: map[int]error{tc.failAt: errors.New("ffmpeg: unknown encoder 'libmp3lame'")},
			}

			got, err := newTestChunked(inner, tool, 120*time.Second).
				Transcribe(context.Background(), []byte("the whole 250 seconds"), "lecture.m4a")
			require.NoError(t, err)
			require.Len(t, tool.cuts, tc.wantCuts)
			require.Len(t, inner.calls, tc.wantCalls)

			last := inner.calls[len(inner.calls)-1]
			assert.Equal(t, "lecture.m4a", last.fileName)
			assert.Equal(t, []byte("the whole 250 seconds"), last.audio)
			assert.Equal(t, "text:lecture.m4a", got.Text)
		})
	}
}

// A window ffmpeg writes nothing for is the same failure as a window it cannot
// cut, and takes the same fallback.
func TestEmptyCutSendsTheWholeFile(t *testing.T) {
	inner := &fakeASR{}
	tool := &fakeMediaTool{
		duration: 250 * time.Second,
		piece:    func(int) []byte { return nil },
	}

	got, err := newTestChunked(inner, tool, 120*time.Second).
		Transcribe(context.Background(), []byte("whole"), "lecture.m4a")
	require.NoError(t, err)
	require.Len(t, inner.calls, 1)
	assert.Equal(t, "lecture.m4a", inner.calls[0].fileName)
	assert.Equal(t, "text:lecture.m4a", got.Text)
}

// One window failing is not the recording failing: the rest is kept, the gap
// keeps its place on the timeline, and the failure is logged.
func TestOneSegmentFailingKeepsTheRest(t *testing.T) {
	logs := captureLogs(t)
	inner := &fakeASR{answer: func(_ context.Context, call fakeCall, _ int) (*TranscriptionResult, error) {
		if call.fileName == "part-001.mp3" {
			return nil, errors.New("400 InternalError.Algo.InvalidParameter")
		}
		return &TranscriptionResult{Text: strings.TrimSuffix(call.fileName, ".mp3")}, nil
	}}
	tool := &fakeMediaTool{duration: 250 * time.Second}

	got, err := newTestChunked(inner, tool, 120*time.Second).
		Transcribe(context.Background(), []byte("250 s"), "lecture.m4a")
	require.NoError(t, err, "a partial transcription is still a transcription")

	assert.Equal(t, "part-000\npart-002", got.Text, "the successful transcripts keep their order")
	assert.Equal(t, []Segment{
		{Start: 0, End: 120, Text: "part-000"},
		{Start: 120, End: 240, Text: ""},
		{Start: 240, End: 250, Text: "part-002"},
	}, got.Segments, "the failed window keeps its place with an empty text")
	assert.Contains(t, logs.String(), "keeping the other segments")
	assert.Contains(t, logs.String(), "400 InternalError.Algo.InvalidParameter")
}

// Only when nothing came back is the caller told to degrade.
func TestAllSegmentsFailingReturnsAnError(t *testing.T) {
	logs := captureLogs(t)
	inner := &fakeASR{answer: func(context.Context, fakeCall, int) (*TranscriptionResult, error) {
		return nil, errors.New("upstream 500")
	}}
	tool := &fakeMediaTool{duration: 250 * time.Second}

	got, err := newTestChunked(inner, tool, 120*time.Second).
		Transcribe(context.Background(), []byte("250 s"), "lecture.m4a")
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "all 3 audio segments failed")
	assert.Contains(t, err.Error(), "upstream 500")
	assert.Len(t, inner.calls, 3)
	assert.Contains(t, logs.String(), "keeping the other segments")
}

// A cancelled parse stops at the first failed window instead of spending the
// remaining requests: partial text is worthless once the caller has given up.
func TestCancelledRunStopsAtTheFirstFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inner := &fakeASR{answer: func(ctx context.Context, _ fakeCall, _ int) (*TranscriptionResult, error) {
		cancel()
		return nil, ctx.Err()
	}}
	tool := &fakeMediaTool{duration: 250 * time.Second}

	got, err := newTestChunked(inner, tool, 120*time.Second).
		Transcribe(ctx, []byte("250 s"), "lecture.m4a")
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "interrupted")
	assert.Len(t, inner.calls, 1, "the other windows must not be attempted")
}

// A cancelled parse must not upload the whole recording as a consolation
// prize: that request would be cancelled too, after the upload.
func TestCancelledDuringCutDoesNotSendTheWholeFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	inner := &fakeASR{}
	tool := &fakeMediaTool{duration: 250 * time.Second, cutErr: context.Canceled}
	cancel()

	got, err := newTestChunked(inner, tool, 120*time.Second).
		Transcribe(ctx, []byte("250 s"), "lecture.m4a")
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "interrupted")
	assert.Empty(t, inner.calls)
}

// The request ceiling is a refusal, not a silent truncation: a prefix would
// look like a complete transcript to everything downstream.
func TestTooManySegmentsIsRefusedBeforeAnyRequest(t *testing.T) {
	inner := &fakeASR{}
	tool := &fakeMediaTool{duration: 3 * time.Hour}

	got, err := newTestChunked(inner, tool, 120*time.Second).
		Transcribe(context.Background(), []byte("three hours"), "lecture.m4a")
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Empty(t, inner.calls, "nothing may be sent")
	assert.Empty(t, tool.cuts, "nothing may be cut")
	assert.Contains(t, err.Error(), "needs 90 requests")
	assert.Contains(t, err.Error(), "asr_max_segment_duration")
}

// A duration that lands just over a multiple of the ceiling must not upload
// milliseconds of container padding as a request of its own.
func TestTrailingSliverFoldsIntoTheLastSegment(t *testing.T) {
	inner := &fakeASR{answer: func(_ context.Context, call fakeCall, _ int) (*TranscriptionResult, error) {
		return &TranscriptionResult{Text: call.fileName}, nil
	}}
	tool := &fakeMediaTool{duration: 240400 * time.Millisecond}

	got, err := newTestChunked(inner, tool, 120*time.Second).
		Transcribe(context.Background(), []byte("240.4 s"), "lecture.m4a")
	require.NoError(t, err)

	assert.Equal(t, [][2]time.Duration{
		{0, 120 * time.Second},
		{120 * time.Second, 120400 * time.Millisecond},
	}, cutWindows(tool.cuts))
	require.Len(t, inner.calls, 2)
	assert.Equal(t, []Segment{
		{Start: 0, End: 120, Text: "part-000.mp3"},
		{Start: 120, End: 240.4, Text: "part-001.mp3"},
	}, got.Segments)
	assert.Equal(t, 240.4, got.Duration)
}

func TestPlanSegments(t *testing.T) {
	tests := []struct {
		name       string
		total      time.Duration
		maxSegment time.Duration
		want       [][2]time.Duration
	}{
		{
			name: "fits in one window", total: 120 * time.Second, maxSegment: 120 * time.Second,
			want: [][2]time.Duration{{0, 120 * time.Second}},
		},
		{
			name: "one second over", total: 121 * time.Second, maxSegment: 120 * time.Second,
			want: [][2]time.Duration{{0, 120 * time.Second}, {120 * time.Second, 1 * time.Second}},
		},
		{
			name: "exact multiple", total: 240 * time.Second, maxSegment: 120 * time.Second,
			want: [][2]time.Duration{
				{0, 120 * time.Second},
				{120 * time.Second, 120 * time.Second},
			},
		},
		{
			name: "sliver over a multiple", total: 240 * time.Second, maxSegment: 100 * time.Second,
			want: [][2]time.Duration{
				{0, 100 * time.Second},
				{100 * time.Second, 100 * time.Second},
				{200 * time.Second, 40 * time.Second},
			},
		},
		{
			name: "ten milliseconds over a multiple", total: 240010 * time.Millisecond,
			maxSegment: 120 * time.Second,
			want: [][2]time.Duration{
				{0, 120 * time.Second},
				{120 * time.Second, 120010 * time.Millisecond},
			},
		},
		{name: "unknown duration", total: 0, maxSegment: 120 * time.Second, want: nil},
		{name: "no ceiling", total: 250 * time.Second, maxSegment: 0, want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			windows := planSegments(tc.total, tc.maxSegment)
			if len(tc.want) == 0 {
				assert.Empty(t, windows)
				return
			}
			got := make([][2]time.Duration, len(windows))
			for index, window := range windows {
				got[index] = [2]time.Duration{window.start, window.length}
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

// The suffix of the staged copy is only a hint for ffprobe: a name that is not
// a short container extension is not pasted onto a path.
func TestStageExtension(t *testing.T) {
	tests := map[string]string{
		"/data/files/talk.mp4":  ".mp4",
		"talk.M4A":              ".m4a",
		"talk.wav":              ".wav",
		"talk":                  ".bin",
		"":                      ".bin",
		"talk.verylong":         ".bin",
		"../../etc/passwd":      ".bin",
		"talk.mp3/../../escape": ".bin",
		"talk.tar.gz":           ".gz",
	}
	for name, want := range tests {
		assert.Equal(t, want, stageExtension(name), name)
	}
}

// A container without a duration answers "N/A", and a zero or negative one is
// no length to cut at either.
func TestParseProbeDuration(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   time.Duration
		wantOK bool
	}{
		{name: "milliseconds", output: "250.480000\n", want: 250480 * time.Millisecond, wantOK: true},
		{name: "padded", output: "  64.0  ", want: 64 * time.Second, wantOK: true},
		{name: "unknown", output: "N/A"},
		{name: "empty", output: ""},
		{name: "zero", output: "0"},
		{name: "negative", output: "-3.5"},
		{name: "not a number", output: "Duration: 00:04:10.00"},
		{name: "nan", output: "nan"},
		{name: "infinite", output: "inf"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseProbeDuration(tc.output)
			if !tc.wantOK {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "no usable duration")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// The probe asks for the duration alone, so its answer is one bare number.
func TestProbeArguments(t *testing.T) {
	assert.Equal(t, []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		"/tmp/source.mp3",
	}, probeArgs("/tmp/source.mp3"))
}

// The seek has to be an input option: as an output option ffmpeg decodes from
// the start of the recording for every window, which turns a two-hour file into
// hours of decoding. The length goes after the input, where it limits the
// output.
func TestCutArgumentsSeekBeforeTheInput(t *testing.T) {
	args := cutArgs("/tmp/source.m4a", 120*time.Second, 120*time.Second, "/tmp/part-001.mp3")
	index := func(flag string) int {
		for at, arg := range args {
			if arg == flag {
				return at
			}
		}
		return -1
	}
	require.NotEqual(t, -1, index("-ss"))
	require.NotEqual(t, -1, index("-i"))
	require.NotEqual(t, -1, index("-t"))
	assert.Less(t, index("-ss"), index("-i"), "-ss must be an input option")
	assert.Less(t, index("-i"), index("-t"), "-t must limit the output")
	assert.Equal(t, "120.000", args[index("-ss")+1])
	assert.Equal(t, "120.000", args[index("-t")+1])
	assert.Contains(t, args, "-vn")
	assert.Contains(t, args, "libmp3lame")
	assert.Contains(t, args, strconv.Itoa(segmentSampleRate))
	assert.Equal(t, "/tmp/part-001.mp3", args[len(args)-1])
}

// The deployment settings are the default, and a model row that names its own
// wins per field.
func TestChunkedASRSettings(t *testing.T) {
	t.Cleanup(func() { SetMediaDefaults(0, "", "") })

	SetMediaDefaults(0, "", "")
	build := func() *chunkedASR {
		return newChunkedASR(&fakeASR{}, nil).(*chunkedASR)
	}
	assert.Equal(t, DefaultMaxSegmentDuration, build().maxSegment)
	assert.Equal(t, defaultFFmpegPath, build().tool.(*ffmpegTool).ffmpegPath)
	assert.Equal(t, defaultFFprobePath, build().tool.(*ffmpegTool).ffprobePath)

	SetMediaDefaults(90*time.Second, "/opt/bin/ffmpeg", "/opt/bin/ffprobe")
	assert.Equal(t, 90*time.Second, build().maxSegment)
	assert.Equal(t, "/opt/bin/ffmpeg", build().tool.(*ffmpegTool).ffmpegPath)
	assert.Equal(t, "/opt/bin/ffprobe", build().tool.(*ffmpegTool).ffprobePath)

	overridden := newChunkedASR(&fakeASR{}, &Config{
		MaxSegmentDuration: 300 * time.Second, FFmpegPath: "/srv/ffmpeg",
	}).(*chunkedASR)
	assert.Equal(t, 300*time.Second, overridden.maxSegment)
	assert.Equal(t, "/srv/ffmpeg", overridden.tool.(*ffmpegTool).ffmpegPath)
	assert.Equal(t, "/opt/bin/ffprobe", overridden.tool.(*ffmpegTool).ffprobePath,
		"a field the model row leaves empty keeps the deployment's value")
}

// The decorator order is part of the contract: the cutter is inside Langfuse,
// so one transcription is one generation whatever number of requests it took.
func TestNewASRWrapsTheCutterInsideLangfuse(t *testing.T) {
	if langfuse.GetManager().Enabled() {
		t.Skip("langfuse is enabled in this process; the wrapper order is inspected by NewASR's code")
	}
	allowLoopback(t)
	instance, err := NewASR(&Config{
		Provider: "openai", BaseURL: "http://127.0.0.1:9/v1", ModelName: "whisper-1", APIKey: "k",
	})
	require.NoError(t, err)

	chunked, ok := instance.(*chunkedASR)
	require.True(t, ok, "NewASR returned %T", instance)
	_, ok = chunked.inner.(*protocolASR)
	assert.True(t, ok, "the cutter must wrap the vendor client, got %T", chunked.inner)
	assert.Equal(t, DefaultMaxSegmentDuration, chunked.maxSegment)
}
