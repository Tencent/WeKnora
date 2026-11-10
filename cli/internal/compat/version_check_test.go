package compat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/compat"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeProbeClient struct {
	info *sdk.SystemInfo
	err  error
}

func (f *fakeProbeClient) GetSystemInfo(ctx context.Context) (*sdk.SystemInfo, error) {
	return f.info, f.err
}

func TestProbe_Success(t *testing.T) {
	c := &fakeProbeClient{info: &sdk.SystemInfo{Version: "1.2.3"}}
	got, err := compat.Probe(context.Background(), c)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got.ServerVersion != "1.2.3" {
		t.Errorf("ServerVersion = %q, want %q", got.ServerVersion, "1.2.3")
	}
	if got.ProbedAt.IsZero() {
		t.Error("ProbedAt should be set")
	}
}

func TestProbe_Error(t *testing.T) {
	c := &fakeProbeClient{err: errors.New("HTTP error 500: down")}
	_, err := compat.Probe(context.Background(), c)
	if err == nil {
		t.Fatal("expected error")
	}
}
