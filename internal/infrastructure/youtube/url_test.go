package youtube

import "testing"

func TestParseURL(t *testing.T) {
	video := Link{Kind: LinkVideo, VideoID: "jNQXAC9IVRw"}
	tests := []struct {
		name string
		raw  string
		want Link
		ok   bool
	}{
		{"watch", "https://www.youtube.com/watch?v=jNQXAC9IVRw", video, true},
		{"watch with extra params", "https://youtube.com/watch?feature=share&v=jNQXAC9IVRw&t=42s", video, true},
		{"watch in playlist is a video", "https://www.youtube.com/watch?v=jNQXAC9IVRw&list=PL1234567890", video, true},
		{"mobile", "https://m.youtube.com/watch?v=jNQXAC9IVRw", video, true},
		{"music", "https://music.youtube.com/watch?v=jNQXAC9IVRw", video, true},
		{"short link", "https://youtu.be/jNQXAC9IVRw?si=abc", video, true},
		{"shorts", "https://www.youtube.com/shorts/jNQXAC9IVRw", video, true},
		{"live", "https://www.youtube.com/live/jNQXAC9IVRw", video, true},
		{"embed", "https://www.youtube-nocookie.com/embed/jNQXAC9IVRw", video, true},
		{
			"playlist", "https://www.youtube.com/playlist?list=PLRqwX-V7Uu6ZiZxtDDRCi6uhfTH4FilpH",
			Link{Kind: LinkPlaylist, PlaylistID: "PLRqwX-V7Uu6ZiZxtDDRCi6uhfTH4FilpH"},
			true,
		},
		{"surrounding whitespace", "  https://youtu.be/jNQXAC9IVRw \n", video, true},
		{"other site", "https://example.com/watch?v=jNQXAC9IVRw", Link{}, false},
		{"lookalike host", "https://youtube.com.evil.test/watch?v=jNQXAC9IVRw", Link{}, false},
		{"channel page", "https://www.youtube.com/@somechannel", Link{}, false},
		{"bad video id", "https://www.youtube.com/watch?v=short", Link{}, false},
		{"id with injection", "https://youtu.be/--exec=rm", Link{}, false},
		{"playlist without list", "https://www.youtube.com/playlist", Link{}, false},
		{"non-http scheme", "ftp://www.youtube.com/watch?v=jNQXAC9IVRw", Link{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseURL(tt.raw)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("ParseURL(%q) = %+v, %v; want %+v, %v", tt.raw, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestCanonicalURLs(t *testing.T) {
	if got := VideoURL("jNQXAC9IVRw"); got != "https://www.youtube.com/watch?v=jNQXAC9IVRw" {
		t.Fatalf("VideoURL = %q", got)
	}
	if got := PlaylistURL("PL123"); got != "https://www.youtube.com/playlist?list=PL123" {
		t.Fatalf("PlaylistURL = %q", got)
	}
}
