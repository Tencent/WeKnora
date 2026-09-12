package buildinfo

import (
	"runtime"
	"strings"
	"testing"
)

func TestGetAppliesFallbacks(t *testing.T) {
	info := Get()
	if Unknown(info.GoVersion) {
		t.Fatal("GoVersion must fall back to runtime.Version() when the linker value is unknown")
	}
	if !strings.HasPrefix(info.GoVersion, "go") && !strings.HasPrefix(GoVersion, "go") {
		t.Fatalf("GoVersion = %q, want a go-prefixed toolchain version", info.GoVersion)
	}
	if runtime.Version() != "" && GoVersion == "unknown" && info.GoVersion != runtime.Version() {
		t.Fatalf("GoVersion fallback = %q, want %q", info.GoVersion, runtime.Version())
	}
	if info.Edition == "" {
		t.Fatal("Edition must never be empty")
	}
	// Dirty is null when the VCS state is unavailable; it must never be
	// fabricated as a hardcoded false.
	if info.Dirty != nil && *info.Dirty && Unknown(info.CommitID) {
		t.Fatal("dirty without a commit id is an inconsistent build state")
	}
}

func TestUnknownClassification(t *testing.T) {
	if !Unknown("") || !Unknown("unknown") {
		t.Fatal("empty and explicit unknown must classify as unavailable")
	}
	if Unknown("v1.2.3") {
		t.Fatal("a real version must not classify as unavailable")
	}
}
