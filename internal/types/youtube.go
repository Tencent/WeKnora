package types

// YouTubeImportItem describes one requested link or video that was not queued
// as new knowledge, either because it already exists or because it failed.
type YouTubeImportItem struct {
	// URL is the canonical video URL, or the link as submitted when it could
	// not be resolved to a video.
	URL         string `json:"url"`
	VideoID     string `json:"video_id,omitempty"`
	Title       string `json:"title,omitempty"`
	KnowledgeID string `json:"knowledge_id,omitempty"`
	Error       string `json:"error,omitempty"`
}

// YouTubeImportPlaylist summarises one playlist link of a YouTube import.
type YouTubeImportPlaylist struct {
	URL        string `json:"url"`
	PlaylistID string `json:"playlist_id"`
	Title      string `json:"title"`
	// FolderPath is the folder this playlist's videos were placed in.
	FolderPath string `json:"folder_path"`
	// Videos is the number of importable videos taken from this playlist.
	Videos int `json:"videos"`
}

// YouTubeImportResult is the outcome of importing a batch of YouTube video and
// playlist links: one knowledge entry is queued per distinct video and
// processed asynchronously.
type YouTubeImportResult struct {
	// TotalVideos is the number of distinct videos considered.
	TotalVideos int `json:"total_videos"`
	// Truncated reports that the links resolved to more videos than
	// YOUTUBE_MAX_VIDEOS_PER_IMPORT and only the first ones were imported.
	Truncated  bool                    `json:"truncated"`
	Playlists  []YouTubeImportPlaylist `json:"playlists"`
	Created    []*Knowledge            `json:"created"`
	Duplicates []YouTubeImportItem     `json:"duplicates"`
	Failed     []YouTubeImportItem     `json:"failed"`
}
