package yunzhijia

import (
	"net"
	"testing"
)

func TestDeriveWebSocketURL(t *testing.T) {
	got, err := deriveWebSocketURL(
		"https://dev.kdweibo.cn:8443/gateway/robot/webhook/send?yzjtype=12&yzjtoken=token%20value",
		"kdweibo.cn",
	)
	if err != nil {
		t.Fatalf("deriveWebSocketURL() error = %v", err)
	}
	want := "wss://dev.kdweibo.cn:8443/xuntong/websocket?yzjtoken=token+value"
	if got != want {
		t.Fatalf("deriveWebSocketURL() = %q, want %q", got, want)
	}
}

func TestDeriveWebSocketURLRejectsMissingToken(t *testing.T) {
	_, err := deriveWebSocketURL("https://www.yunzhijia.com/gateway/robot/webhook/send", "yunzhijia.com")
	if err == nil {
		t.Fatal("deriveWebSocketURL() expected missing-token error")
	}
}

func TestValidateEndpointURLHostSuffixBoundary(t *testing.T) {
	valid := []string{
		"https://yunzhijia.com/path",
		"https://www.yunzhijia.com/path",
	}
	for _, rawURL := range valid {
		if _, err := validateEndpointURL(rawURL, "https", "yunzhijia.com"); err != nil {
			t.Errorf("validateEndpointURL(%q) unexpected error: %v", rawURL, err)
		}
	}

	invalid := []string{
		"http://www.yunzhijia.com/path",
		"https://evilyunzhijia.com/path",
		"https://127.0.0.1/path",
		"https://localhost/path",
	}
	for _, rawURL := range invalid {
		if _, err := validateEndpointURL(rawURL, "https", "yunzhijia.com"); err == nil {
			t.Errorf("validateEndpointURL(%q) expected error", rawURL)
		}
	}
}

func TestValidateEndpointURLRequiresHostSuffix(t *testing.T) {
	if _, err := validateEndpointURL("https://www.yunzhijia.com/path", "https", ""); err == nil {
		t.Fatal("validateEndpointURL() expected missing host suffix error")
	}
}

func TestIsPublicIP(t *testing.T) {
	for _, rawIP := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1", "fc00::1"} {
		if isPublicIP(net.ParseIP(rawIP)) {
			t.Errorf("isPublicIP(%q) = true, want false", rawIP)
		}
	}
	if !isPublicIP(net.ParseIP("8.8.8.8")) {
		t.Error("isPublicIP(8.8.8.8) = false, want true")
	}
}

func TestPositiveIntCredential(t *testing.T) {
	if got := positiveIntCredential(map[string]any{"timeout": float64(25)}, "timeout", 10); got != 25 {
		t.Fatalf("numeric timeout = %d, want 25", got)
	}
	if got := positiveIntCredential(map[string]any{"timeout": "30"}, "timeout", 10); got != 30 {
		t.Fatalf("string timeout = %d, want 30", got)
	}
	if got := positiveIntCredential(map[string]any{"timeout": float64(0)}, "timeout", 10); got != 10 {
		t.Fatalf("invalid timeout = %d, want fallback 10", got)
	}
}
