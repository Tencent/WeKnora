package linkcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/projectlink"
	sdk "github.com/Tencent/WeKnora/client"
)

func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func fakeKBServer(t *testing.T, kbs []sdk.KnowledgeBase) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/knowledge-bases", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sdk.KnowledgeBaseListResponse{Success: true, Data: kbs})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newFactory(currentCtx string, client *sdk.Client) *cmdutil.Factory {
	cfg := &config.Config{
		CurrentContext: currentCtx,
		Contexts: map[string]config.Context{
			currentCtx: {Host: "https://example"},
		},
	}
	return &cmdutil.Factory{
		Config: func() (*config.Config, error) { return cfg, nil },
		Client: func() (*sdk.Client, error) {
			if client == nil {
				return nil, errors.New("client not configured")
			}
			return client, nil
		},
	}
}

func TestLink_ByID(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	out, _ := iostreams.SetForTest(t)

	f := newFactory("default", nil)
	opts := &Options{KBID: "kb_xxx"}
	require.NoError(t, runLink(context.Background(), opts, f))

	linkPath := filepath.Join(dir, ".weknora", "project.yaml")
	p, err := projectlink.Load(linkPath)
	require.NoError(t, err)
	assert.Equal(t, "kb_xxx", p.KBID)
	assert.Equal(t, "default", p.Context)
	assert.Contains(t, out.String(), "✓")
}

func TestLink_ByName(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, _ = iostreams.SetForTest(t)

	srv := fakeKBServer(t, []sdk.KnowledgeBase{
		{ID: "kb_a", Name: "foo"},
		{ID: "kb_b", Name: "bar"},
	})
	cli := sdk.NewClient(srv.URL)
	f := newFactory("default", cli)
	opts := &Options{KBName: "foo"}
	require.NoError(t, runLink(context.Background(), opts, f))

	p, err := projectlink.Load(filepath.Join(dir, ".weknora", "project.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "kb_a", p.KBID)
}

func TestLink_KBNotFound(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, _ = iostreams.SetForTest(t)

	srv := fakeKBServer(t, []sdk.KnowledgeBase{{ID: "kb_a", Name: "foo"}})
	cli := sdk.NewClient(srv.URL)
	f := newFactory("default", cli)
	opts := &Options{KBName: "missing"}
	err := runLink(context.Background(), opts, f)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeKBNotFound, typed.Code)
}

// TestLink_MutuallyExclusive exercises the cobra flag-parse layer. Driving
// the command with both --kb-id and --kb must surface a usage error before
// runLink runs.
func TestLink_MutuallyExclusive(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, _ = iostreams.SetForTest(t)

	f := newFactory("default", nil)
	cmd := NewCmd(f)
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"--kb-id", "kb_a", "--kb", "foo"})
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	require.Error(t, err, "expected mutually-exclusive flag error")
	assert.True(t,
		strings.Contains(err.Error(), "if any flags in the group") ||
			strings.Contains(err.Error(), "mutually exclusive") ||
			strings.Contains(err.Error(), "exclusive"),
		"error should mention mutual exclusivity, got %q", err.Error())
}

// TestLink_OneRequired exercises MarkFlagsOneRequired: zero of {kb-id, kb}
// must be a usage error.
func TestLink_OneRequired(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, _ = iostreams.SetForTest(t)

	f := newFactory("default", nil)
	cmd := NewCmd(f)
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{}) // neither flag
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	require.Error(t, err, "expected required-flag error")
}
