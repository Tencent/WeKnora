package youtube

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func tracks(codes ...string) map[string][]captionTrack {
	out := make(map[string][]captionTrack, len(codes))
	for _, code := range codes {
		out[code] = []captionTrack{{Ext: "json3"}}
	}
	return out
}

func TestSelectCaptionTrack(t *testing.T) {
	tests := []struct {
		name     string
		info     VideoInfo
		wantLang string
		wantAuto bool
		wantOK   bool
	}{
		{
			name: "manual in original language wins",
			info: VideoInfo{
				Language: "en", Subtitles: tracks("de", "en"), AutomaticCaptions: tracks("en-orig", "en"),
			},
			wantLang: "en",
		},
		{
			name:     "regional manual variant matches",
			info:     VideoInfo{Language: "en", Subtitles: tracks("en-GB", "fr")},
			wantLang: "en-GB",
		},
		{
			name:     "original auto captions beat translated manual captions",
			info:     VideoInfo{Subtitles: tracks("zh-CN"), AutomaticCaptions: tracks("ja", "en-orig", "en")},
			wantLang: "en-orig", wantAuto: true,
		},
		{
			name:     "auto captions in declared language without orig suffix",
			info:     VideoInfo{Language: "ko", AutomaticCaptions: tracks("en", "ko")},
			wantLang: "ko", wantAuto: true,
		},
		{
			name:     "english manual when original unknown",
			info:     VideoInfo{Subtitles: tracks("de", "en-US")},
			wantLang: "en-US",
		},
		{
			name:     "first manual track as last manual resort",
			info:     VideoInfo{Subtitles: tracks("fr", "de", "live_chat")},
			wantLang: "de",
		},
		{
			name:     "english auto captions",
			info:     VideoInfo{AutomaticCaptions: tracks("en", "fr")},
			wantLang: "en", wantAuto: true,
		},
		{
			name: "no captions",
			info: VideoInfo{Subtitles: tracks("live_chat"), AutomaticCaptions: tracks("fr")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lang, auto, ok := SelectCaptionTrack(&tt.info)
			wantOK := tt.wantOK || tt.wantLang != ""
			if lang != tt.wantLang || auto != tt.wantAuto || ok != wantOK {
				t.Fatalf("SelectCaptionTrack = %q, %v, %v; want %q, %v, %v",
					lang, auto, ok, tt.wantLang, tt.wantAuto, wantOK)
			}
		})
	}
}

func TestParsePlaylistSkipsUnavailableAndTruncates(t *testing.T) {
	data := []byte(`{"id":"PL1","title":"Course","entries":[
		{"id":"aaaaaaaaaaa","title":"One"},
		{"id":"bbbbbbbbbbb","title":"[Private video]"},
		{"id":"aaaaaaaaaaa","title":"One again"},
		{"id":"not-an-id","title":"Broken"},
		{"id":"ccccccccccc","title":"Two"},
		{"id":"ddddddddddd","title":"Three"}
	]}`)
	playlist, err := parsePlaylist(data, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []PlaylistEntry{{VideoID: "aaaaaaaaaaa", Title: "One"}, {VideoID: "ccccccccccc", Title: "Two"}}
	if playlist.Title != "Course" || !slices.Equal(playlist.Entries, want) || !playlist.Truncated {
		t.Fatalf("parsePlaylist = %+v", playlist)
	}

	full, err := parsePlaylist(data, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Entries) != 3 || full.Truncated {
		t.Fatalf("parsePlaylist without limit hit = %+v", full)
	}
}

// fakeTools emulates yt-dlp and ffmpeg by writing the files they would produce.
type fakeTools struct {
	info     string
	captions string // json3 body written for caption downloads; empty simulates failure
	parts    int
	calls    [][]string
}

func (f *fakeTools) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	output := func() string {
		if i := slices.Index(args, "-o"); i >= 0 {
			return args[i+1]
		}
		return args[len(args)-1]
	}
	switch {
	case name == "ffmpeg":
		dir := filepath.Dir(output())
		for i := 0; i < f.parts; i++ {
			part := filepath.Join(dir, fmt.Sprintf("part_%04d.mp3", i))
			if err := os.WriteFile(part, []byte{byte(i)}, 0o600); err != nil {
				return nil, err
			}
		}
		return nil, nil
	case slices.Contains(args, "--flat-playlist"):
		return []byte(`{"id":"PL1","title":"Course","entries":[{"id":"aaaaaaaaaaa","title":"One"}]}`), nil
	case slices.Contains(args, "--write-subs") || slices.Contains(args, "--write-auto-subs"):
		if f.captions == "" {
			return nil, errors.New("yt-dlp failed: ERROR: unable to download subtitles")
		}
		lang := args[slices.Index(args, "--sub-langs")+1]
		path := strings.Replace(output(), "%(ext)s", lang+".json3", 1)
		return nil, os.WriteFile(path, []byte(f.captions), 0o600)
	case slices.Contains(args, "--dump-single-json"):
		return []byte(f.info), nil
	case slices.Contains(args, "bestaudio/best"):
		return nil, os.WriteFile(strings.Replace(output(), "%(ext)s", "webm", 1), []byte("audio"), 0o600)
	}
	return nil, fmt.Errorf("unexpected command %s %v", name, args)
}

func newFakeClient(tools *fakeTools, cfg Config) *Client {
	client := NewClient(cfg)
	client.run = tools.run
	return client
}

func TestFetchTranscriptUsesCaptions(t *testing.T) {
	tools := &fakeTools{
		info:     `{"id":"jNQXAC9IVRw","title":"Me at the zoo","language":"en","subtitles":{"en":[{"ext":"json3"}]}}`,
		captions: `{"events":[{"tStartMs":1200,"segs":[{"utf8":"elephants"}]}]}`,
	}
	client := newFakeClient(tools, Config{CookiesFile: "/secrets/cookies.txt", JSRuntimes: "node"})

	transcript, err := client.FetchTranscript(context.Background(), "jNQXAC9IVRw", nil)
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Source != SourceCaptions || transcript.Language != "en" || len(transcript.Segments) != 1 {
		t.Fatalf("unexpected transcript %+v", transcript)
	}
	for _, call := range tools.calls {
		if !slices.Contains(call, "--ignore-config") || !slices.Contains(call, "/secrets/cookies.txt") ||
			!slices.Contains(call, "node") {
			t.Fatalf("yt-dlp call missing hardening/config flags: %v", call)
		}
		if call[len(call)-2] != "--" || call[len(call)-1] != "https://www.youtube.com/watch?v=jNQXAC9IVRw" {
			t.Fatalf("yt-dlp must receive only the canonical URL after --: %v", call)
		}
	}
}

func TestFetchTranscriptWithoutCaptionsNeedsTranscriber(t *testing.T) {
	tools := &fakeTools{info: `{"id":"jNQXAC9IVRw","title":"Silent"}`}
	client := newFakeClient(tools, Config{})
	if _, err := client.FetchTranscript(context.Background(), "jNQXAC9IVRw", nil); !errors.Is(err, ErrNoTranscript) {
		t.Fatalf("expected ErrNoTranscript, got %v", err)
	}
}

func TestFetchTranscriptFallsBackToSpeechRecognition(t *testing.T) {
	tools := &fakeTools{
		// Captions are listed but the download fails, which must fall through to audio.
		info:  `{"id":"jNQXAC9IVRw","title":"Talk","duration":1300,"automatic_captions":{"en-orig":[{"ext":"json3"}]}}`,
		parts: 3,
	}
	client := newFakeClient(tools, Config{AudioSegmentDuration: 10 * time.Minute})

	var pieces []string
	transcribe := func(_ context.Context, audio []byte, fileName string) ([]Segment, error) {
		pieces = append(pieces, fileName)
		return []Segment{{Start: 5, Text: fmt.Sprintf("piece %d", audio[0])}}, nil
	}
	transcript, err := client.FetchTranscript(context.Background(), "jNQXAC9IVRw", transcribe)
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Source != SourceSpeechRecognition {
		t.Fatalf("source = %s", transcript.Source)
	}
	wantPieces := []string{"jNQXAC9IVRw_0000.mp3", "jNQXAC9IVRw_0001.mp3", "jNQXAC9IVRw_0002.mp3"}
	if !slices.Equal(pieces, wantPieces) {
		t.Fatalf("transcribed pieces = %v", pieces)
	}
	wantStarts := []float64{5, 605, 1205}
	for i, seg := range transcript.Segments {
		if seg.Start != wantStarts[i] || seg.Text != fmt.Sprintf("piece %d", i) {
			t.Fatalf("segment %d = %+v, want start %v", i, seg, wantStarts[i])
		}
	}
}

func TestFetchTranscriptRejectsOverlongAudio(t *testing.T) {
	tools := &fakeTools{info: `{"id":"jNQXAC9IVRw","title":"Marathon","duration":36000}`}
	client := newFakeClient(tools, Config{MaxAudioDuration: time.Hour})
	transcribe := func(context.Context, []byte, string) ([]Segment, error) {
		t.Fatal("transcriber must not run for overlong videos")
		return nil, nil
	}
	_, err := client.FetchTranscript(context.Background(), "jNQXAC9IVRw", transcribe)
	if err == nil || !strings.Contains(err.Error(), "YOUTUBE_MAX_AUDIO_MINUTES") {
		t.Fatalf("expected duration limit error, got %v", err)
	}
}

func TestFetchTranscriptRejectsLiveAndInvalidIDs(t *testing.T) {
	tools := &fakeTools{info: `{"id":"jNQXAC9IVRw","live_status":"is_live"}`}
	client := newFakeClient(tools, Config{})
	if _, err := client.FetchTranscript(context.Background(), "jNQXAC9IVRw", nil); err == nil {
		t.Fatal("expected live stream error")
	}
	if _, err := client.FetchTranscript(context.Background(), "--exec=x", nil); err == nil {
		t.Fatal("expected invalid ID error")
	}
	if _, err := client.Playlist(context.Background(), "bad id!", 5); err == nil {
		t.Fatal("expected invalid playlist ID error")
	}
}

func TestPlaylistRequestsOneExtraEntryToDetectTruncation(t *testing.T) {
	tools := &fakeTools{}
	client := newFakeClient(tools, Config{MaxVideosPerImport: 50})
	playlist, err := client.Playlist(context.Background(), "PL1", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(playlist.Entries) != 1 {
		t.Fatalf("entries = %+v", playlist.Entries)
	}
	call := tools.calls[0]
	if call[slices.Index(call, "--playlist-end")+1] != "6" {
		t.Fatalf("expected --playlist-end 6, got %v", call)
	}
}

func TestCommandErrorDetailPrefersErrorLines(t *testing.T) {
	stderr := []byte("WARNING: slow\nERROR: [youtube] abc: Private video\n")
	if got := commandErrorDetail(stderr, errors.New("exit status 1")); got != "ERROR: [youtube] abc: Private video" {
		t.Fatalf("commandErrorDetail = %q", got)
	}
	if got := commandErrorDetail(nil, errors.New("exit status 1")); got != "exit status 1" {
		t.Fatalf("commandErrorDetail without stderr = %q", got)
	}
}

func TestRunCommandReportsMissingTool(t *testing.T) {
	_, err := runCommand(context.Background(), "weknora-definitely-missing-binary")
	if !errors.Is(err, ErrToolMissing) {
		t.Fatalf("expected ErrToolMissing, got %v", err)
	}
}

func TestRenderMarkdown(t *testing.T) {
	transcript := &Transcript{
		Video: VideoInfo{
			ID: "jNQXAC9IVRw", Title: "Me at the zoo", Uploader: "jawed", UploadDate: "20050424",
			Duration: 19, Description: "The first video",
		},
		Source:   SourceAutoCaptions,
		Language: "en",
		Segments: []Segment{{Start: 1, Text: "elephants"}},
	}
	got := RenderMarkdown(transcript, "### Summary\nElephants have long trunks.")
	for _, want := range []string{
		"# Me at the zoo\n",
		"- Source: https://www.youtube.com/watch?v=jNQXAC9IVRw\n",
		"- Channel: jawed\n",
		"- Published: 2005-04-24\n",
		"- Duration: 0:19\n",
		"- Transcript: YouTube auto-generated captions (en)\n",
		"## Description\n\nThe first video\n",
		"## Documentation\n\n### Summary\nElephants have long trunks.\n",
		"## Transcript\n\n[0:01] elephants\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("RenderMarkdown missing %q in:\n%s", want, got)
		}
	}

	withoutDocs := RenderMarkdown(&Transcript{Video: VideoInfo{ID: "jNQXAC9IVRw"}, Source: SourceSpeechRecognition}, "")
	if strings.Contains(withoutDocs, "## Documentation") ||
		!strings.Contains(withoutDocs, "# YouTube video jNQXAC9IVRw") ||
		!strings.Contains(withoutDocs, "Transcript: Speech recognition") {
		t.Fatalf("RenderMarkdown without documentation:\n%s", withoutDocs)
	}
}
