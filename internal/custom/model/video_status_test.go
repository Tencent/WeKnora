package model

import "testing"

func TestVideoIsReadyForHome(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   bool
	}{
		{name: "ready", status: VideoStatusReady, want: true},
		{name: "processing", status: VideoStatusProcessing, want: true},
		{name: "completed", status: VideoStatusCompleted, want: true},
		{name: "uploaded", status: VideoStatusUploaded, want: true},
		{name: "initializing", status: VideoStatusInitializing, want: true},
		{name: "failed", status: VideoStatusFailed, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := VideoIsReadyForHome(tc.status); got != tc.want {
				t.Fatalf("VideoIsReadyForHome(%q) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

func TestVideoIsInitiallyAvailable(t *testing.T) {
	cases := []struct {
		name         string
		status       string
		fileURL      string
		thumbnailURL string
		want         bool
	}{
		{name: "uploaded with file and cover", status: VideoStatusUploaded, fileURL: "https://cdn/video.mp4", thumbnailURL: "https://cdn/cover.jpg", want: true},
		{name: "uploaded without cover is playable", status: VideoStatusUploaded, fileURL: "https://cdn/video.mp4", want: true},
		{name: "initializing without cover is playable", status: VideoStatusInitializing, fileURL: "https://cdn/video.mp4", want: true},
		{name: "initializing with cover", status: VideoStatusInitializing, fileURL: "https://cdn/video.mp4", thumbnailURL: "https://cdn/cover.jpg", want: true},
		{name: "ready without cover degrades to placeholder", status: VideoStatusReady, fileURL: "https://cdn/video.mp4", want: true},
		{name: "processing without cover degrades to placeholder", status: VideoStatusProcessing, fileURL: "https://cdn/video.mp4", want: true},
		{name: "completed without cover degrades to placeholder", status: VideoStatusCompleted, fileURL: "https://cdn/video.mp4", want: true},
		{name: "ready without file", status: VideoStatusReady, want: false},
		{name: "uploading with placeholder url", status: VideoStatusUploading, fileURL: "https://cdn/video.mp4", thumbnailURL: "https://cdn/cover.jpg", want: false},
		{name: "content failure with file remains playable", status: VideoStatusFailed, fileURL: "https://cdn/video.mp4", thumbnailURL: "https://cdn/cover.jpg", want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := VideoIsInitiallyAvailable(tc.status, tc.fileURL, tc.thumbnailURL); got != tc.want {
				t.Fatalf("VideoIsInitiallyAvailable(%q, %q, %q) = %v, want %v", tc.status, tc.fileURL, tc.thumbnailURL, got, tc.want)
			}
		})
	}
}

func TestVideoIsVisibleInListKeepsFailures(t *testing.T) {
	if !VideoIsVisibleInList(VideoStatusFailed, "", "") {
		t.Fatal("failed videos must remain visible for error reporting")
	}
	if VideoIsVisibleInList(VideoStatusUploading, "https://cdn/video.mp4", "https://cdn/cover.jpg") {
		t.Fatal("active uploads must not appear before multipart completion")
	}
	if !VideoIsVisibleInList(VideoStatusInitializing, "https://cdn/video.mp4", "") {
		t.Fatal("videos with a merged file must appear while the cover is generating")
	}
	if !VideoIsVisibleInList(VideoStatusReady, "https://cdn/video.mp4", "") {
		t.Fatal("cover-degraded videos must remain visible with placeholder")
	}
}

func TestVideoIsPlayableAfterContentFailure(t *testing.T) {
	if !VideoIsPlayable(VideoStatusFailed, "https://cdn/video.mp4", "") {
		t.Fatal("failed parsing must keep an uploaded video playable")
	}
	if VideoIsPlayable(VideoStatusFailed, "", "") {
		t.Fatal("failed upload without a core file must not be playable")
	}
}
