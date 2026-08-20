package git_repo

import (
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
)

func TestNormalizeRepoURLSSRF(t *testing.T) {
	// The SSRF whitelist is loaded once per process (sync.Once), so pin the env
	// and force a reload before asserting, and restore a clean slate after
	// (same pattern as the searxng tests). SSRF_WHITELIST_EXTRA is cleared too:
	// an operator-supplied EXTRA (e.g. 10.0.0.0/8 from the repo's .env) would
	// otherwise leak into the "blocked" assertions below and break the test.
	//
	// Config under test: an internal git server is allowlisted (exact IP, CIDR,
	// suffix), while loopback / link-local / unlisted private targets stay
	// blocked and public hosts are always fine.
	utils.ResetSSRFWhitelistForTest()
	t.Setenv("SSRF_WHITELIST", "192.168.0.200,192.168.1.0/24,*.corp.example")
	t.Setenv("SSRF_WHITELIST_EXTRA", "")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)

	allowed := []string{
		"https://github.com/org/blog.git",   // public
		"https://gitlab.com/group/project",  // public
		"http://example.com/repo.git",       // public
		"http://192.168.0.200/git/repo.git", // exact IP whitelisted
		"http://192.168.1.15/git/repo.git",  // CIDR whitelisted
		"https://git.corp.example/repo.git", // suffix whitelisted
	}
	for _, raw := range allowed {
		got, err := normalizeRepoURL(raw)
		if err != nil {
			t.Errorf("normalizeRepoURL(%q) unexpected error: %v", raw, err)
			continue
		}
		if got == "" {
			t.Errorf("normalizeRepoURL(%q) returned empty", raw)
		}
	}

	blocked := []string{
		"http://127.0.0.1/repo.git",     // loopback (not whitelisted)
		"http://192.168.0.201/repo.git", // same subnet, not listed
		"http://10.0.0.1/repo.git",      // private, not whitelisted
		"http://169.254.169.254/latest", // cloud metadata link-local
		"http://localhost/repo.git",     // reserved hostname
		"http://myhost.local/repo.git",  // reserved suffix
		"http://[::1]/repo.git",         // IPv6 loopback
	}
	for _, raw := range blocked {
		if _, err := normalizeRepoURL(raw); err == nil {
			t.Errorf("normalizeRepoURL(%q) expected SSRF error, got nil", raw)
		}
	}
}

func TestNormalizeRepoURLLocalPathUnaffected(t *testing.T) {
	// Absolute local paths are used by tests/checkouts and must not go through
	// URL-based SSRF validation.
	dir := filepath.Join(t.TempDir(), "repo.git")
	got, err := normalizeRepoURL(dir)
	if err != nil {
		t.Fatalf("local path rejected: %v", err)
	}
	if got != dir {
		t.Fatalf("got %q, want %q", got, dir)
	}
}

func TestEnsureSSRFTransportIsIdempotent(_ *testing.T) {
	// Must not panic and must be safe to call repeatedly (sync.Once path).
	ensureSSRFTransport()
	ensureSSRFTransport()
}
