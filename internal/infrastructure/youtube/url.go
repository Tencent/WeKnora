// Package youtube turns YouTube videos and playlists into knowledge-base
// documents: it resolves links, fetches captions (or downloads audio for
// speech recognition when a video has none) through yt-dlp, and renders the
// result as Markdown.
package youtube

import (
	"net/url"
	"regexp"
	"strings"
)

// LinkKind distinguishes a single video link from a playlist link.
type LinkKind int

const (
	// LinkVideo is a link to one video (watch, youtu.be, shorts, live, embed).
	LinkVideo LinkKind = iota + 1
	// LinkPlaylist is a link to a playlist page (/playlist?list=...).
	LinkPlaylist
)

// Link is a parsed YouTube link. Only the IDs are kept: callers rebuild
// canonical URLs from them so user-controlled URL text never reaches yt-dlp.
type Link struct {
	Kind       LinkKind
	VideoID    string
	PlaylistID string
}

var (
	videoIDPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	playlistIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{2,64}$`)
)

var youtubeHosts = map[string]bool{
	"youtube.com":              true,
	"www.youtube.com":          true,
	"m.youtube.com":            true,
	"music.youtube.com":        true,
	"youtube-nocookie.com":     true,
	"www.youtube-nocookie.com": true,
}

// ParseURL recognises YouTube video and playlist links. A watch link that
// also carries a list parameter is treated as a single video, matching what
// the user is looking at; use the /playlist?list= link to import a playlist.
func ParseURL(raw string) (Link, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return Link{}, false
	}
	host := strings.ToLower(u.Hostname())
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")

	if host == "youtu.be" || host == "www.youtu.be" {
		return videoLink(segments[0])
	}
	if !youtubeHosts[host] {
		return Link{}, false
	}

	query := u.Query()
	switch strings.ToLower(segments[0]) {
	case "watch":
		return videoLink(query.Get("v"))
	case "playlist":
		id := query.Get("list")
		if !playlistIDPattern.MatchString(id) {
			return Link{}, false
		}
		return Link{Kind: LinkPlaylist, PlaylistID: id}, true
	case "shorts", "live", "embed", "v":
		if len(segments) < 2 {
			return Link{}, false
		}
		return videoLink(segments[1])
	}
	return Link{}, false
}

func videoLink(id string) (Link, bool) {
	if !videoIDPattern.MatchString(id) {
		return Link{}, false
	}
	return Link{Kind: LinkVideo, VideoID: id}, true
}

// VideoURL returns the canonical watch URL for a video ID.
func VideoURL(videoID string) string {
	return "https://www.youtube.com/watch?v=" + videoID
}

// PlaylistURL returns the canonical playlist URL for a playlist ID.
func PlaylistURL(playlistID string) string {
	return "https://www.youtube.com/playlist?list=" + playlistID
}
