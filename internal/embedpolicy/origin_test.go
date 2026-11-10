package embedpolicy

import "testing"

func TestOriginPatterns(t *testing.T) {
	for _, tc := range []struct {
		origin, pattern string
		want            bool
	}{
		{"https://B.example", "https://b.example/", true},
		{"https://a.example", "https://a.example:443", true},
		{"http://a.example:8080", "http://a.example:8080", true},
		{"http://a.example", "https://a.example", false},
		{"https://a.example:8443", "https://a.example", false},
		{"https://shop.example.com:8443", "*.example.com", true},
		{"https://SHOP.EXAMPLE.COM", "*.EXAMPLE.COM", true},
		{"https://shop.example.com", "*.example.com:443", true},
		{"http://shop.example.com", "*.example.com:443", false},
		{"https://example.com", "*.example.com", false},
		{"https://evil-example.com", "*.example.com", false},
		{"https://example.com.evil.test", "*.example.com", false},
		{"https://evil.test/path.example.com", "*.example.com", false},
		{"null", "*", false},
		{"", "*", false},
	} {
		t.Run(tc.origin+"/"+tc.pattern, func(t *testing.T) {
			if got := Allows(tc.origin, []string{tc.pattern}); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestRejectInvalidPatterns(t *testing.T) {
	for _, raw := range []string{
		"https://a.example/path", "https://a.example?x=1", "https://a.example?", "https://a.example#",
		"https://user@a.example", "https://a.example;", "https://a.example; frame-ancestors *",
		"https://*", "javascript:alert(1)", "*.127.0.0.1", "https://a.example/../",
	} {
		if _, err := NormalizePattern(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestFrameAncestors(t *testing.T) {
	for _, tc := range []struct {
		patterns []string
		want     string
	}{
		{nil, "frame-ancestors 'none'"},
		{[]string{" ", "https://a.example;"}, "frame-ancestors 'none'"},
		{[]string{"https://A.example:443/"}, "frame-ancestors 'self' https://a.example"},
		{[]string{"*.example.com"}, "frame-ancestors 'self' http://*.example.com:* https://*.example.com:*"},
		{[]string{"*.example.com:8443"}, "frame-ancestors 'self' http://*.example.com:8443 https://*.example.com:8443"},
		{[]string{"*"}, "frame-ancestors *"},
	} {
		if got := FrameAncestors(tc.patterns); got != tc.want {
			t.Errorf("%v: got %q want %q", tc.patterns, got, tc.want)
		}
	}
}
