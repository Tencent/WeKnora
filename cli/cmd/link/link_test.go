package linkcmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	opts := &Options{KB: "kb_xxx"}
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
	opts := &Options{KB: "foo"}
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
	opts := &Options{KB: "missing"}
	err := runLink(context.Background(), opts, f)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeKBNotFound, typed.Code)
}

func TestLink_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, _ = iostreams.SetForTest(t)

	// Pre-existing link.
	linkPath := filepath.Join(dir, ".weknora", "project.yaml")
	require.NoError(t, projectlink.Save(linkPath, &projectlink.Project{
		Context: "default", KBID: "kb_old",
	}))

	f := newFactory("default", nil)
	opts := &Options{KB: "kb_new"}
	require.NoError(t, runLink(context.Background(), opts, f))

	p, err := projectlink.Load(linkPath)
	require.NoError(t, err)
	assert.Equal(t, "kb_new", p.KBID, "link should overwrite silently")
}

// TestLink_NonInteractive_NoKB exercises the non-TTY-without-flag error path.
// SetForTest gives us a non-TTY iostreams, so omitting --kb must error rather
// than hang on a prompt.
func TestLink_NonInteractive_NoKB(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, _ = iostreams.SetForTest(t)

	f := newFactory("default", nil)
	opts := &Options{} // no KB
	err := runLink(context.Background(), opts, f)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeKBIDRequired, typed.Code)
}
