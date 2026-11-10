package dingtalk

import "testing"

func TestHumanSizeMovesToTheNextUnitWhenRoundingReachesIt(t *testing.T) {
	cases := []struct {
		size int64
		want string
	}{
		{0, ""},
		{-1, ""},
		{512, "512.0 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024*1024 - 1, "1.0 MB"},
		{1024*1024 - 52, "1023.9 KB"},
		{1024*1024*1024 - 1, "1.0 GB"},
		{3 * 1024 * 1024 * 1024 * 1024, "3072.0 GB"},
	}
	for _, c := range cases {
		if got := humanSize(c.size); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.size, got, c.want)
		}
	}
}
