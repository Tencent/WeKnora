package types

// YouTubePlaylistImportItem describes one playlist video that was not queued
// as new knowledge, either because it already exists or because it failed.
type YouTubePlaylistImportItem struct {
	VideoID     string `json:"video_id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	KnowledgeID string `json:"knowledge_id,omitempty"`
	Error       string `json:"error,omitempty"`
}

// YouTubePlaylistImportResult is the outcome of importing a YouTube playlist:
// one knowledge entry is queued per video and processed asynchronously.
type YouTubePlaylistImportResult struct {
	PlaylistID    string `json:"playlist_id"`
	PlaylistTitle string `json:"playlist_title"`
	// TotalVideos is the number of importable videos considered.
	TotalVideos int `json:"total_videos"`
	// Truncated reports that the playlist exceeded YOUTUBE_PLAYLIST_MAX_VIDEOS
	// and only the first videos were imported.
	Truncated  bool                        `json:"truncated"`
	Created    []*Knowledge                `json:"created"`
	Duplicates []YouTubePlaylistImportItem `json:"duplicates"`
	Failed     []YouTubePlaylistImportItem `json:"failed"`
}
