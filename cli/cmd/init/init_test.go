package initcmd

import (
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

// chdir switches cwd to dir for the duration of the test.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// fakeKBServer returns an httptest server answering ListKnowledgeBases.
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

// newFactory builds a minimal Factory for init tests with the supplied
// CurrentContext + Client closure. Tests inject the cobra-level deps directly
// because runInit is the test boundary (matches kb/list_test.go pattern).
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

func TestInit_CreatesWithFlags_Yes(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	out, _ := iostreams.SetForTest(t)

	f := newFactory("default", nil)
	opts := &Options{Context: "default", KBID: "kb_explicit", Yes: true}
	require.NoError(t, runInit(context.Background(), opts, f))

	// Verify file written with the right contents.
	linkPath := filepath.Join(dir, ".weknora", "project.yaml")
	p, err := projectlink.Load(linkPath)
	require.NoError(t, err)
	assert.Equal(t, "kb_explicit", p.KBID)
	assert.Equal(t, "default", p.Context)
	assert.False(t, p.CreatedAt.IsZero(), "CreatedAt should be stamped")

	// Human output mentions the path + ✓.
	assert.Contains(t, out.String(), "✓")
	assert.Contains(t, out.String(), "kb_explicit")
}

func TestInit_CreatesWithFlags_KBName(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, _ = iostreams.SetForTest(t)

	srv := fakeKBServer(t, []sdk.KnowledgeBase{
		{ID: "kb_a", Name: "engineering"},
		{ID: "kb_b", Name: "marketing"},
	})
	cli := sdk.NewClient(srv.URL)
	f := newFactory("default", cli)
	opts := &Options{Context: "default", KBName: "engineering", Yes: true}
	require.NoError(t, runInit(context.Background(), opts, f))

	linkPath := filepath.Join(dir, ".weknora", "project.yaml")
	p, err := projectlink.Load(linkPath)
	require.NoError(t, err)
	assert.Equal(t, "kb_a", p.KBID, "KB name 'engineering' should resolve to kb_a")
}

func TestInit_RefusesWhenExists(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, _ = iostreams.SetForTest(t)

	// Pre-seed an existing link.
	linkPath := filepath.Join(dir, ".weknora", "project.yaml")
	require.NoError(t, projectlink.Save(linkPath, &projectlink.Project{KBID: "kb_pre"}))

	f := newFactory("default", nil)
	opts := &Options{Context: "default", KBID: "kb_new", Yes: true} // no --force
	err := runInit(context.Background(), opts, f)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeProjectAlreadyLinked, typed.Code)

	// Original kb_pre must NOT be overwritten.
	p, err := projectlink.Load(linkPath)
	require.NoError(t, err)
	assert.Equal(t, "kb_pre", p.KBID)
}

func TestInit_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, _ = iostreams.SetForTest(t)

	linkPath := filepath.Join(dir, ".weknora", "project.yaml")
	require.NoError(t, projectlink.Save(linkPath, &projectlink.Project{KBID: "kb_pre"}))

	f := newFactory("default", nil)
	opts := &Options{Context: "default", KBID: "kb_new", Yes: true, Force: true}
	require.NoError(t, runInit(context.Background(), opts, f))

	p, err := projectlink.Load(linkPath)
	require.NoError(t, err)
	assert.Equal(t, "kb_new", p.KBID, "--force must overwrite the prior link")
}

func TestInit_JSONOutput(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	out, _ := iostreams.SetForTest(t)

	f := newFactory("default", nil)
	opts := &Options{Context: "default", KBID: "kb_explicit", Yes: true, JSONOut: true}
	require.NoError(t, runInit(context.Background(), opts, f))

	got := out.String()
	assert.True(t, strings.HasPrefix(got, `{"ok":true`), "envelope should start with ok:true; got %q", got)
	assert.Contains(t, got, `"kb_id":"kb_explicit"`)
	assert.Contains(t, got, `"project_link_path"`)
}
