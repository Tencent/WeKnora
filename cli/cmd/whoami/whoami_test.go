package whoami

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeSvc struct {
	resp *sdk.CurrentUserResponse
	err  error
}

func (f *fakeSvc) GetCurrentUser(_ context.Context) (*sdk.CurrentUserResponse, error) {
	return f.resp, f.err
}

func newRespOK() *sdk.CurrentUserResponse {
	r := &sdk.CurrentUserResponse{}
	r.Data.User = &sdk.AuthUser{ID: "usr_abc"}
	r.Data.Tenant = &sdk.AuthTenant{ID: 42}
	return r
}

func TestWhoami_Human(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeSvc{resp: newRespOK()}
	if err := runWhoami(context.Background(), &Options{}, svc); err != nil {
		t.Fatalf("runWhoami: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "user_id:") || !strings.Contains(got, "usr_abc") {
		t.Errorf("expected user_id: usr_abc in %q", got)
	}
	if !strings.Contains(got, "tenant_id:") || !strings.Contains(got, "42") {
		t.Errorf("expected tenant_id: 42 in %q", got)
	}
}

func TestWhoami_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeSvc{resp: newRespOK()}
	if err := runWhoami(context.Background(), &Options{JSONOut: true}, svc); err != nil {
		t.Fatalf("runWhoami: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"ok":true`) {
		t.Errorf("expected ok:true in %q", got)
	}
	if !strings.Contains(got, `"user_id":"usr_abc"`) {
		t.Errorf("expected user_id field in %q", got)
	}
}

func TestWhoami_Unauthenticated(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeSvc{err: errors.New("HTTP error 401: unauthenticated")}
	err := runWhoami(context.Background(), &Options{}, svc)
	if err == nil {
		t.Fatal("expected error")
	}
	if !cmdutil.IsAuthError(err) {
		t.Errorf("expected auth error, got %v", err)
	}
}

func TestWhoami_NilUser(t *testing.T) {
	// SDK could return success but without user pointer (server bug or partial response)
	_, _ = iostreams.SetForTest(t)
	svc := &fakeSvc{resp: &sdk.CurrentUserResponse{}} // Data.User == nil
	err := runWhoami(context.Background(), &Options{}, svc)
	if err == nil {
		t.Fatal("expected error for nil user")
	}
}
