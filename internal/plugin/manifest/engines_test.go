package manifest

import "testing"

func TestCheckEngines(t *testing.T) {
	cases := []struct {
		rng, host string
		ok        bool
	}{
		{"", "0.3.0", true},
		{">=0.10.0 <1.0.0", "0.10.2", true},
		{">=0.10.0 <1.0.0", "v0.9.9", false},
		{">=0.10.0 <1.0.0", "1.0.0", false},
		{"0.10.0", "0.10.0", true},
		{">=9.0.0", "unknown", true}, // development build
	}
	for _, c := range cases {
		m := &Manifest{Engines: Engines{WeKnora: c.rng}}
		if err := m.CheckEngines(c.host); (err == nil) != c.ok {
			t.Errorf("CheckEngines(%q, %q) = %v, want ok=%v", c.rng, c.host, err, c.ok)
		}
	}
	if _, err := parseRange(">=1.0 <2"); err == nil {
		t.Error("partial versions must be rejected")
	}
}
