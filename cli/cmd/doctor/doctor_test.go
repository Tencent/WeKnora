package doctor

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/cli/internal/compat"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeServices struct {
	systemInfo     *sdk.SystemInfo
	systemErr      error
	userResp       *sdk.CurrentUserResponse
	userErr        error
	pingErr        error
	systemInfoHits atomic.Int32 // count GetSystemInfo invocations
}

func (f *fakeServices) GetSystemInfo(ctx context.Context) (*sdk.SystemInfo, error) {
	f.systemInfoHits.Add(1)
	return f.systemInfo, f.systemErr
}
func (f *fakeServices) GetCurrentUser(ctx context.Context) (*sdk.CurrentUserResponse, error) {
	return f.userResp, f.userErr
}
func (f *fakeServices) PingBaseURL(ctx context.Context) error { return f.pingErr }

func goodUserResp() *sdk.CurrentUserResponse {
	r := &sdk.CurrentUserResponse{}
	r.Data.User = &sdk.AuthUser{ID: "u1"}
	return r
}

func TestDoctor_AllOK(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	out, _ := iostreams.SetForTest(t)
	svc := &fakeServices{
		systemInfo: &sdk.SystemInfo{Version: "1.0.0"},
		userResp:   goodUserResp(),
	}
	r := runChecks(context.Background(), &Options{JSONOut: true}, svc, "1.0.0")
	if !r.Summary.AllPassed {
		t.Errorf("expected all_passed, got summary %+v", r.Summary)
	}
	if r.Summary.Failed != 0 || r.Summary.Skipped != 0 {
		t.Errorf("expected 0 fail / 0 skip, got %+v", r.Summary)
	}
	emit(&Options{JSONOut: true}, r)
	if !strings.Contains(out.String(), `"all_passed":true`) {
		t.Errorf("envelope should embed all_passed=true, got %q", out.String())
	}
}

func TestDoctor_BaseURLFails_DownstreamSkip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)
	svc := &fakeServices{
		pingErr:    errors.New("connect refused"),
		userResp:   goodUserResp(),
		systemInfo: &sdk.SystemInfo{Version: "1.0.0"},
	}
	r := runChecks(context.Background(), &Options{}, svc, "1.0.0")
	if r.Summary.Skipped != 2 {
		t.Errorf("expected 2 skipped (auth_credential + server_version), got %d", r.Summary.Skipped)
	}
	if r.Checks[0].Status != "fail" {
		t.Errorf("base_url_reachable status = %q, want fail", r.Checks[0].Status)
	}
	if r.Checks[1].Status != "skip" {
		t.Errorf("auth_credential status = %q, want skip", r.Checks[1].Status)
	}
	if r.Checks[2].Status != "skip" {
		t.Errorf("server_version status = %q, want skip", r.Checks[2].Status)
	}
	// credential_storage 与网络无关,应该独立运行(不受 base_url fail 影响)
	if r.Checks[3].Name != "credential_storage" {
		t.Errorf("Checks[3] = %q, want credential_storage", r.Checks[3].Name)
	}
}

func TestDoctor_Offline_OnlyKeyringChecked(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)
	svc := &fakeServices{}
	r := runChecks(context.Background(), &Options{Offline: true}, svc, "1.0.0")
	if r.Summary.Skipped < 3 {
		t.Errorf("expected >=3 skip in offline mode, got %d", r.Summary.Skipped)
	}
	last := r.Checks[3]
	if last.Name != "credential_storage" {
		t.Errorf("last check = %q, want credential_storage", last.Name)
	}
	if last.Status == "skip" {
		t.Error("credential_storage should NOT be skipped even in offline mode")
	}
}

func TestDoctor_AuthFails_VersionSkipped(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)
	svc := &fakeServices{
		userErr:    errors.New("HTTP error 401: unauthenticated"),
		systemInfo: &sdk.SystemInfo{Version: "1.0.0"},
	}
	r := runChecks(context.Background(), &Options{}, svc, "1.0.0")
	if r.Checks[0].Status != "ok" {
		t.Errorf("base_url should pass, got %q", r.Checks[0].Status)
	}
	if r.Checks[1].Status != "fail" {
		t.Errorf("auth_credential should fail, got %q", r.Checks[1].Status)
	}
	if r.Checks[2].Status != "skip" {
		t.Errorf("server_version should skip due to auth fail, got %q", r.Checks[2].Status)
	}
}

func TestDoctor_CacheHit_SkipsProbe(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)
	// Pre-populate fresh cache
	if err := compat.SaveCache(&compat.Info{ServerVersion: "1.0.0", ProbedAt: time.Now()}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	svc := &fakeServices{userResp: goodUserResp()}
	r := runChecks(context.Background(), &Options{}, svc, "1.0.0")
	if r.Checks[2].Status != "ok" {
		t.Errorf("server_version should be ok via cache, got %q (%s)", r.Checks[2].Status, r.Checks[2].Details)
	}
	if svc.systemInfoHits.Load() != 0 {
		t.Errorf("expected 0 GetSystemInfo calls (cache hit), got %d", svc.systemInfoHits.Load())
	}
	if !strings.Contains(r.Checks[2].Details, "cached") {
		t.Errorf("details should mention cache, got %q", r.Checks[2].Details)
	}
}

func TestDoctor_NoCache_BypassesCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)
	// Pre-populate fresh cache; --no-cache should ignore it
	if err := compat.SaveCache(&compat.Info{ServerVersion: "9.9.9", ProbedAt: time.Now()}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	svc := &fakeServices{
		userResp:   goodUserResp(),
		systemInfo: &sdk.SystemInfo{Version: "1.0.0"},
	}
	r := runChecks(context.Background(), &Options{NoCache: true}, svc, "1.0.0")
	if svc.systemInfoHits.Load() != 1 {
		t.Errorf("expected 1 GetSystemInfo call (--no-cache), got %d", svc.systemInfoHits.Load())
	}
	if !strings.Contains(r.Checks[2].Details, "1.0.0") {
		t.Errorf("details should reflect probed version 1.0.0 not cached 9.9.9, got %q", r.Checks[2].Details)
	}
}
