package tools

import "testing"

func TestFormatFileSizeMovesToTheNextUnitWhenRoundingReachesIt(t *testing.T) {
	cases := []struct {
		size int64
		want string
	}{
		{0, "Unknown"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024*1024 - 1, "1.0 MB"},
		{1024*1024 - 52, "1023.9 KB"},
		{1024*1024*1024 - 1, "1.0 GB"},
		{5 * 1024 * 1024, "5.0 MB"},
	}
	for _, c := range cases {
		if got := formatFileSize(c.size); got != c.want {
			t.Errorf("formatFileSize(%d) = %q, want %q", c.size, got, c.want)
		}
	}
}
