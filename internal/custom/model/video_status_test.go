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
		{name: "uploaded", status: VideoStatusUploaded, want: false},
		{name: "initializing", status: VideoStatusInitializing, want: false},
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
