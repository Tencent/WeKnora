package cmdutil

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/projectlink"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	"github.com/Tencent/WeKnora/cli/internal/secrets"
	sdk "github.com/Tencent/WeKnora/client"
)

// TestFactory_Lazy ensures none of the closures execute work at construction
// time — `--help` / `completion` must not trigger HTTP / keyring access.
func TestFactory_Lazy(t *testing.T) {
	var configCalls, clientCalls, prompterCalls int
	f := &Factory{
		Config: func() (*config.Config, error) {
			configCalls++
			return &config.Config{}, nil
		},
		Client: func() (*sdk.Client, error) {
			clientCalls++
			return nil, nil
		},
		Prompter: func() prompt.Prompter {
			prompterCalls++
			return prompt.AgentPrompter{}
		},
	}
	// Asserting on closure presence — none should have run yet.
	assert.Equal(t, 0, configCalls)
	assert.Equal(t, 0, clientCalls)
	assert.Equal(t, 0, prompterCalls)
	// Smoke: each closure runs exactly once when called.
	_, err := f.Config()
	require.NoError(t, err)
	assert.Equal(t, 1, configCalls)
	_, _ = f.Client()
	assert.Equal(t, 1, clientCalls)
	_ = f.Prompter()
	assert.Equal(t, 1, prompterCalls)
}

// TestNew_FoundationDefaults verifies the production New() returns a usable
// Factory and that Client surfaces auth.unauthenticated when no current
// context is configured (the precondition for `weknora auth login`).
func TestNew_FoundationDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // empty config → no current context
	f := New()
	require.NotNil(t, f)
	require.NotNil(t, f.Config)
	require.NotNil(t, f.Client)
	require.NotNil(t, f.Prompter)
	require.NotNil(t, f.Secrets)

	_, err := f.Client()
	require.Error(t, err)
	var typed *Error
	require.True(t, errors.As(err, &typed), "expected *cmdutil.Error")
	assert.Equal(t, CodeAuthUnauthenticated, typed.Code)
}

// TestFactory_ContextOverride verifies the global --context flag mechanism:
// f.ContextOverride replaces config.CurrentContext for this invocation only,
// without writing to disk. Spec §1.2.
func TestFactory_ContextOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	// Seed config with two contexts; CurrentContext = "default"
	cfgPath := dir + "/weknora/config.yaml"
	require.NoError(t, os.MkdirAll(dir+"/weknora", 0o700))
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
current_context: default
contexts:
  default:
    host: https://default.example
  other:
    host: https://other.example
`), 0o600))

	f := New()

	t.Run("no override: returns CurrentContext from disk", func(t *testing.T) {
		f.ContextOverride = ""
		cfg, err := f.Config()
		require.NoError(t, err)
		assert.Equal(t, "default", cfg.CurrentContext)
	})

	t.Run("override applied: ContextOverride wins over disk", func(t *testing.T) {
		f.ContextOverride = "other"
		cfg, err := f.Config()
		require.NoError(t, err)
		assert.Equal(t, "other", cfg.CurrentContext)
	})

	t.Run("override does not persist to disk", func(t *testing.T) {
		// Reload from disk: should still be "default" (the original).
		raw, err := os.ReadFile(cfgPath)
		require.NoError(t, err)
		assert.Contains(t, string(raw), "current_context: default")
	})
}

// TestTypedPredicates exercises the namespace and code matchers.
func TestTypedPredicates(t *testing.T) {
	t.Run("IsAuthError matches auth.* prefix", func(t *testing.T) {
		err := NewError(CodeAuthUnauthenticated, "no creds")
		assert.True(t, IsAuthError(err))
		assert.False(t, IsNotFound(err))
	})
	t.Run("IsNotFound matches resource.not_found exactly", func(t *testing.T) {
		err := NewError(CodeResourceNotFound, "kb missing")
		assert.True(t, IsNotFound(err))
		assert.False(t, IsTransient(err))
	})
	t.Run("IsTransient matches network.* and server.timeout / rate_limited", func(t *testing.T) {
		assert.True(t, IsTransient(NewError(CodeNetworkError, "")))
		assert.True(t, IsTransient(NewError(CodeServerTimeout, "")))
		assert.True(t, IsTransient(NewError(CodeServerRateLimited, "")))
		assert.False(t, IsTransient(NewError(CodeServerError, "")))
	})
	t.Run("predicates walk the wrap chain", func(t *testing.T) {
		inner := NewError(CodeAuthTokenExpired, "expired")
		wrapped := Wrapf(CodeServerError, inner, "while calling foo")
		// IsAuthExpired matches the wrapped *Error first; outer Wrapf has
		// CodeServerError so the predicate returns false on the outer match.
		// This documents current behavior: predicates report the first *Error
		// in the chain, not deep walks.
		assert.False(t, IsAuthExpired(wrapped))
		// Direct match works.
		assert.True(t, IsAuthExpired(inner))
	})
	t.Run("non-typed errors are never matched", func(t *testing.T) {
		assert.False(t, IsAuthError(errors.New("plain error")))
		assert.False(t, IsNotFound(nil))
	})
}

// TestError_Format checks the Error/Unwrap surface.
func TestError_Format(t *testing.T) {
	cause := errors.New("dial tcp: refused")
	e := Wrapf(CodeNetworkError, cause, "connect to %s", "host")
	assert.Contains(t, e.Error(), "network.error")
	assert.Contains(t, e.Error(), "connect to host")
	assert.Contains(t, e.Error(), "dial tcp: refused")
	assert.Same(t, cause, errors.Unwrap(e))
}

func memSecretsFn(s *secrets.MemStore) func() (secrets.Store, error) {
	return func() (secrets.Store, error) { return s, nil }
}

func TestBuildClient_NoCurrentContext(t *testing.T) {
	f := &Factory{
		Config:  func() (*config.Config, error) { return &config.Config{}, nil },
		Secrets: memSecretsFn(secrets.NewMemStore()),
	}
	_, err := buildClient(f)
	require.Error(t, err)
	var typed *Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, CodeAuthUnauthenticated, typed.Code)
}

func TestBuildClient_UnknownContext(t *testing.T) {
	f := &Factory{
		Config: func() (*config.Config, error) {
			return &config.Config{CurrentContext: "ghost"}, nil
		},
		Secrets: memSecretsFn(secrets.NewMemStore()),
	}
	_, err := buildClient(f)
	require.Error(t, err)
	var typed *Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, CodeLocalConfigCorrupt, typed.Code)
}

func TestBuildClient_MissingHost(t *testing.T) {
	f := &Factory{
		Config: func() (*config.Config, error) {
			return &config.Config{
				CurrentContext: "p",
				Contexts:       map[string]config.Context{"p": {Host: ""}},
			}, nil
		},
		Secrets: memSecretsFn(secrets.NewMemStore()),
	}
	_, err := buildClient(f)
	require.Error(t, err)
	var typed *Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, CodeLocalConfigCorrupt, typed.Code)
}

func TestBuildClient_HappyPath(t *testing.T) {
	store := secrets.NewMemStore()
	require.NoError(t, store.Set("p", "access", "jwt"))
	require.NoError(t, store.Set("p", "api_key", "sk-x"))
	f := &Factory{
		Config: func() (*config.Config, error) {
			return &config.Config{
				CurrentContext: "p",
				Contexts: map[string]config.Context{
					"p": {
						Host:      "https://kb.example.com",
						TenantID:  7,
						TokenRef:  "mem://p/access",
						APIKeyRef: "mem://p/api_key",
					},
				},
			}, nil
		},
		Secrets: memSecretsFn(store),
	}
	cli, err := buildClient(f)
	require.NoError(t, err)
	require.NotNil(t, cli)
}

func TestBuildClient_SkipsUnreferencedSecrets(t *testing.T) {
	// If the context doesn't list APIKeyRef, buildClient must not call
	// Get(api_key) — a perf invariant: avoid keychain trips for unused creds.
	store := &countingSecrets{MemStore: secrets.NewMemStore()}
	require.NoError(t, store.Set("p", "access", "jwt"))
	f := &Factory{
		Config: func() (*config.Config, error) {
			return &config.Config{
				CurrentContext: "p",
				Contexts: map[string]config.Context{
					"p": {Host: "https://x", TokenRef: "mem://p/access"},
				},
			}, nil
		},
		Secrets: func() (secrets.Store, error) { return store, nil },
	}
	_, err := buildClient(f)
	require.NoError(t, err)
	assert.Equal(t, 1, store.gets, "must fetch only access; api_key was not referenced")
}

// countingSecrets wraps MemStore to count Get invocations.
type countingSecrets struct {
	*secrets.MemStore
	gets int
}

func (c *countingSecrets) Get(ctx, key string) (string, error) {
	c.gets++
	return c.MemStore.Get(ctx, key)
}

// makeResolveKBCmd builds a minimal cobra.Command carrying the single --kb
// local flag so cmd.Flags().GetString lookups in ResolveKB exercise the flag
// path. The single flag accepts either a kb_<id> or a name.
func makeResolveKBCmd(t *testing.T, kb string) *cobra.Command {
	t.Helper()
	c := &cobra.Command{Use: "x"}
	c.Flags().String("kb", "", "")
	if kb != "" {
		require.NoError(t, c.Flags().Set("kb", kb))
	}
	c.SetContext(context.Background())
	return c
}

// resolveKBChdir switches cwd to dir for the duration of t (auto-restored).
// ResolveKB walks up from os.Getwd() so tests must isolate cwd.
func resolveKBChdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// fakeKBServer returns an httptest server that answers GET /api/v1/knowledge-bases
// with kbs (KnowledgeBaseListResponse), so a real *sdk.Client can talk to it.
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

// TestResolveKB_Chain exercises the 4-level fallback chain. Each sub-test
// isolates cwd / env / closure from the others.
func TestResolveKB_Chain(t *testing.T) {
	t.Run("flag_kb_id_wins", func(t *testing.T) {
		// kb_<id> form → no SDK call, no env, no disk.
		t.Setenv("WEKNORA_KB_ID", "kb_env_should_lose")
		dir := t.TempDir()
		resolveKBChdir(t, dir)
		// Drop a project link too — must be ignored.
		require.NoError(t, projectlink.Save(filepath.Join(dir, ".weknora", "project.yaml"), &projectlink.Project{KBID: "kb_disk_should_lose"}))

		clientCalls := 0
		f := &Factory{
			Client: func() (*sdk.Client, error) {
				clientCalls++
				return nil, errors.New("must not be called")
			},
		}
		got, err := f.ResolveKB(makeResolveKBCmd(t, "kb_explicit"))
		require.NoError(t, err)
		assert.Equal(t, "kb_explicit", got)
		assert.Equal(t, 0, clientCalls)
	})

	t.Run("flag_kb_name_resolves", func(t *testing.T) {
		t.Setenv("WEKNORA_KB_ID", "")
		srv := fakeKBServer(t, []sdk.KnowledgeBase{
			{ID: "kb_a", Name: "foo"},
			{ID: "kb_b", Name: "bar"},
		})
		f := &Factory{
			Client: func() (*sdk.Client, error) { return sdk.NewClient(srv.URL), nil },
		}
		got, err := f.ResolveKB(makeResolveKBCmd(t, "foo"))
		require.NoError(t, err)
		assert.Equal(t, "kb_a", got)
	})

	t.Run("flag_kb_name_not_found", func(t *testing.T) {
		t.Setenv("WEKNORA_KB_ID", "")
		srv := fakeKBServer(t, []sdk.KnowledgeBase{{ID: "kb_a", Name: "foo"}})
		f := &Factory{
			Client: func() (*sdk.Client, error) { return sdk.NewClient(srv.URL), nil },
		}
		_, err := f.ResolveKB(makeResolveKBCmd(t, "missing"))
		require.Error(t, err)
		var typed *Error
		require.ErrorAs(t, err, &typed)
		assert.Equal(t, CodeKBNotFound, typed.Code)
	})

	t.Run("env_var", func(t *testing.T) {
		// No flag, env wins over disk.
		t.Setenv("WEKNORA_KB_ID", "kb_env")
		dir := t.TempDir()
		resolveKBChdir(t, dir)
		require.NoError(t, projectlink.Save(filepath.Join(dir, ".weknora", "project.yaml"), &projectlink.Project{KBID: "kb_disk_should_lose"}))

		f := &Factory{}
		got, err := f.ResolveKB(makeResolveKBCmd(t, ""))
		require.NoError(t, err)
		assert.Equal(t, "kb_env", got)
	})

	t.Run("project_link_walk_up", func(t *testing.T) {
		t.Setenv("WEKNORA_KB_ID", "")
		root := t.TempDir()
		require.NoError(t, projectlink.Save(filepath.Join(root, ".weknora", "project.yaml"), &projectlink.Project{KBID: "kb_proj"}))
		// Run from a deep child to exercise walk-up.
		deep := filepath.Join(root, "a", "b", "c")
		require.NoError(t, os.MkdirAll(deep, 0o755))
		resolveKBChdir(t, deep)

		f := &Factory{}
		got, err := f.ResolveKB(makeResolveKBCmd(t, ""))
		require.NoError(t, err)
		assert.Equal(t, "kb_proj", got)
	})

	t.Run("none", func(t *testing.T) {
		// No flag, no env, no project link → CodeKBIDRequired.
		t.Setenv("WEKNORA_KB_ID", "")
		dir := t.TempDir()
		resolveKBChdir(t, dir)

		f := &Factory{}
		_, err := f.ResolveKB(makeResolveKBCmd(t, ""))
		require.Error(t, err)
		var typed *Error
		require.ErrorAs(t, err, &typed)
		assert.Equal(t, CodeKBIDRequired, typed.Code)
	})
}
