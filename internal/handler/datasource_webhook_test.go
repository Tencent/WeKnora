package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubWebhookService fakes only the two methods the webhook handler needs;
// the embedded interface keeps it valid as interfaces.DataSourceService grows.
type stubWebhookService struct {
	interfaces.DataSourceService
	get       func(ctx context.Context, id string) (*types.DataSource, error)
	webhookFn func(ctx context.Context, id string) (*types.SyncLog, error)
	syncedID  string
	syncCalls int
}

func (s *stubWebhookService) GetDataSource(ctx context.Context, id string) (*types.DataSource, error) {
	if s.get != nil {
		return s.get(ctx, id)
	}
	return nil, nil
}

func (s *stubWebhookService) WebhookSync(ctx context.Context, id string) (*types.SyncLog, error) {
	s.syncCalls++
	s.syncedID = id
	if s.webhookFn != nil {
		return s.webhookFn(ctx, id)
	}
	return &types.SyncLog{ID: "log-1"}, nil
}

// gitRepoDataSource builds a git_repo DataSource whose config serializes to
// the given settings, mirroring how ParseConfig reads the stored JSON.
func gitRepoDataSource(t *testing.T, settings map[string]interface{}) *types.DataSource {
	t.Helper()
	cfg := types.DataSourceConfig{Settings: settings}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return &types.DataSource{
		ID:     "ds-1",
		Type:   types.ConnectorTypeGitRepo,
		Status: types.DataSourceStatusActive,
		Config: raw,
	}
}

func webhookRouter(t *testing.T, svc *stubWebhookService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewDataSourceWebhookHandler(svc)
	r.POST("/api/v1/datasource/webhooks/git/:id", h.HandleGitPush)
	return r
}

func postWebhook(r *gin.Engine, headers map[string]string, body interface{}) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/datasource/webhooks/git/ds-1", bytes.NewReader(raw))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func gitlabPushBody(repoURL, ref, after string) map[string]interface{} {
	return map[string]interface{}{
		"object_kind": "push",
		"ref":         ref,
		"after":       after,
		"repository":  map[string]interface{}{"git_http_url": repoURL},
	}
}

const pushSHA = "5f6d7e8c9a0b1c2d3e4f5061728394a5b6c7d8e9"

func TestWebhookGitLabPushTriggersSync(t *testing.T) {
	t.Setenv(webhookSecretEnv, "")
	svc := &stubWebhookService{get: func(_ context.Context, _ string) (*types.DataSource, error) {
		return gitRepoDataSource(t, map[string]interface{}{
			"webhook_secret": "s3cret",
			"repos": []interface{}{map[string]interface{}{
				"repo_url": "https://gitlab.com/org/blog.git",
			}},
		}), nil
	}}
	r := webhookRouter(t, svc)

	w := postWebhook(r, map[string]string{"X-Gitlab-Event": "Push Hook", "X-Gitlab-Token": "s3cret"},
		gitlabPushBody("https://gitlab.com/org/blog.git", "refs/heads/main", pushSHA))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if svc.syncCalls != 1 || svc.syncedID != "ds-1" {
		t.Fatalf("syncCalls=%d syncedID=%q, want 1 call for ds-1", svc.syncCalls, svc.syncedID)
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "triggered" {
		t.Fatalf("resp = %v", resp)
	}
}

func TestWebhookGitLabWrongTokenRejected(t *testing.T) {
	svc := &stubWebhookService{get: func(_ context.Context, _ string) (*types.DataSource, error) {
		return gitRepoDataSource(t, map[string]interface{}{
			"webhook_secret": "s3cret",
			"repos":          []interface{}{map[string]interface{}{"repo_url": "https://gitlab.com/org/blog.git"}},
		}), nil
	}}
	r := webhookRouter(t, svc)

	w := postWebhook(r, map[string]string{"X-Gitlab-Event": "Push Hook", "X-Gitlab-Token": "wrong"},
		gitlabPushBody("https://gitlab.com/org/blog.git", "refs/heads/main", pushSHA))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (auth failure is not distinguished from not found)", w.Code)
	}
	if svc.syncCalls != 0 {
		t.Fatalf("syncCalls = %d, want 0", svc.syncCalls)
	}
}

func TestWebhookNoSecretConfiguredFailsClosed(t *testing.T) {
	t.Setenv(webhookSecretEnv, "")
	svc := &stubWebhookService{get: func(_ context.Context, _ string) (*types.DataSource, error) {
		return gitRepoDataSource(t, map[string]interface{}{
			"repos": []interface{}{map[string]interface{}{"repo_url": "https://gitlab.com/org/blog.git"}},
		}), nil
	}}
	r := webhookRouter(t, svc)

	w := postWebhook(r, map[string]string{"X-Gitlab-Token": "anything"},
		gitlabPushBody("https://gitlab.com/org/blog.git", "refs/heads/main", pushSHA))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (fail closed, no enumeration)", w.Code)
	}
	if svc.syncCalls != 0 {
		t.Fatalf("syncCalls = %d, want 0", svc.syncCalls)
	}
}

func TestWebhookEnvSecretAccepted(t *testing.T) {
	t.Setenv(webhookSecretEnv, "global-secret")
	svc := &stubWebhookService{get: func(_ context.Context, _ string) (*types.DataSource, error) {
		return gitRepoDataSource(t, map[string]interface{}{
			"repos": []interface{}{map[string]interface{}{"repo_url": "https://gitlab.com/org/blog.git"}},
		}), nil
	}}
	r := webhookRouter(t, svc)

	w := postWebhook(r, map[string]string{"X-Gitlab-Token": "global-secret"},
		gitlabPushBody("https://gitlab.com/org/blog.git", "refs/heads/main", pushSHA))

	if w.Code != http.StatusOK || svc.syncCalls != 1 {
		t.Fatalf("status = %d syncCalls = %d, want 200/1", w.Code, svc.syncCalls)
	}
}

func TestWebhookRepoMismatchIgnored(t *testing.T) {
	svc := &stubWebhookService{get: func(_ context.Context, _ string) (*types.DataSource, error) {
		return gitRepoDataSource(t, map[string]interface{}{
			"webhook_secret": "s3cret",
			"repos":          []interface{}{map[string]interface{}{"repo_url": "https://gitlab.com/org/blog.git"}},
		}), nil
	}}
	r := webhookRouter(t, svc)

	// Same host, different project: must not trigger.
	w := postWebhook(r, map[string]string{"X-Gitlab-Token": "s3cret"},
		gitlabPushBody("https://gitlab.com/org/other.git", "refs/heads/main", pushSHA))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (ignored)", w.Code)
	}
	if svc.syncCalls != 0 {
		t.Fatalf("syncCalls = %d, want 0", svc.syncCalls)
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ignored" {
		t.Fatalf("resp = %v", resp)
	}
}

func TestWebhookBranchFilter(t *testing.T) {
	svc := &stubWebhookService{get: func(_ context.Context, _ string) (*types.DataSource, error) {
		return gitRepoDataSource(t, map[string]interface{}{
			"webhook_secret": "s3cret",
			"repos": []interface{}{map[string]interface{}{
				"repo_url": "https://gitlab.com/org/blog.git", "branch": "main",
			}},
		}), nil
	}}
	r := webhookRouter(t, svc)

	// Push to a filtered-out branch: authenticated but ignored.
	w := postWebhook(r, map[string]string{"X-Gitlab-Token": "s3cret"},
		gitlabPushBody("https://gitlab.com/org/blog.git", "refs/heads/feature/other", pushSHA))
	if w.Code != http.StatusOK || svc.syncCalls != 0 {
		t.Fatalf("status = %d syncCalls = %d, want 200/0", w.Code, svc.syncCalls)
	}

	// Push to the configured branch: triggers.
	w = postWebhook(r, map[string]string{"X-Gitlab-Token": "s3cret"},
		gitlabPushBody("https://gitlab.com/org/blog.git", "refs/heads/main", pushSHA))
	if w.Code != http.StatusOK || svc.syncCalls != 1 {
		t.Fatalf("status = %d syncCalls = %d, want 200/1", w.Code, svc.syncCalls)
	}
}

func TestWebhookBranchDeletionIgnored(t *testing.T) {
	svc := &stubWebhookService{get: func(_ context.Context, _ string) (*types.DataSource, error) {
		return gitRepoDataSource(t, map[string]interface{}{
			"webhook_secret": "s3cret",
			"repos":          []interface{}{map[string]interface{}{"repo_url": "https://gitlab.com/org/blog.git"}},
		}), nil
	}}
	r := webhookRouter(t, svc)

	w := postWebhook(r, map[string]string{"X-Gitlab-Token": "s3cret"},
		gitlabPushBody("https://gitlab.com/org/blog.git", "refs/heads/gone",
			"0000000000000000000000000000000000000000"))
	if w.Code != http.StatusOK || svc.syncCalls != 0 {
		t.Fatalf("status = %d syncCalls = %d, want 200/0 (deletion)", w.Code, svc.syncCalls)
	}
}

func TestWebhookGitHubSignatureVerified(t *testing.T) {
	svc := &stubWebhookService{get: func(_ context.Context, _ string) (*types.DataSource, error) {
		return gitRepoDataSource(t, map[string]interface{}{
			"webhook_secret": "s3cret",
			"repos":          []interface{}{map[string]interface{}{"repo_url": "https://github.com/org/blog"}},
		}), nil
	}}
	r := webhookRouter(t, svc)

	body := map[string]interface{}{
		"ref": "refs/heads/main", "after": pushSHA, "deleted": false,
		"repository": map[string]interface{}{"clone_url": "https://github.com/org/blog.git"},
	}
	raw, _ := json.Marshal(body)

	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(raw)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/datasource/webhooks/git/ds-1", bytes.NewReader(raw))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK || svc.syncCalls != 1 {
		t.Fatalf("status = %d syncCalls = %d, want 200/1", w.Code, svc.syncCalls)
	}

	// Tampered signature must fail.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/datasource/webhooks/git/ds-1", bytes.NewReader(raw))
	req2.Header.Set("X-GitHub-Event", "push")
	req2.Header.Set("X-Hub-Signature-256", "sha256="+string(bytes.Repeat([]byte("a"), 64)))
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotFound || svc.syncCalls != 1 {
		t.Fatalf("status = %d syncCalls = %d, want 404/unchanged", w2.Code, svc.syncCalls)
	}

	// Legacy sha1 signature must be rejected even if HMAC matches.
	mac1 := hmac.New(sha1.New, []byte("s3cret"))
	mac1.Write(raw)
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/datasource/webhooks/git/ds-1", bytes.NewReader(raw))
	req3.Header.Set("X-GitHub-Event", "push")
	req3.Header.Set("X-Hub-Signature", "sha1="+hex.EncodeToString(mac1.Sum(nil)))
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusNotFound || svc.syncCalls != 1 {
		t.Fatalf("sha1 status = %d syncCalls = %d, want 404/unchanged", w3.Code, svc.syncCalls)
	}
}

func TestWebhookNonGitRepoTypeRejected(t *testing.T) {
	svc := &stubWebhookService{get: func(_ context.Context, _ string) (*types.DataSource, error) {
		return &types.DataSource{ID: "ds-1", Type: types.ConnectorTypeNotion, Status: types.DataSourceStatusActive}, nil
	}}
	r := webhookRouter(t, svc)

	w := postWebhook(
		r,
		map[string]string{"X-Gitlab-Token": "x"},
		gitlabPushBody("https://gitlab.com/org/blog.git", "refs/heads/main", pushSHA),
	)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (type is not leaked before/after failed match)", w.Code)
	}
}

func TestWebhookPausedIgnored(t *testing.T) {
	svc := &stubWebhookService{
		get: func(_ context.Context, _ string) (*types.DataSource, error) {
			return gitRepoDataSource(t, map[string]interface{}{
				"webhook_secret": "s3cret",
				"repos":          []interface{}{map[string]interface{}{"repo_url": "https://gitlab.com/org/blog.git"}},
			}), nil
		},
		webhookFn: func(_ context.Context, _ string) (*types.SyncLog, error) {
			return nil, datasource.ErrDataSourcePaused
		},
	}
	r := webhookRouter(t, svc)
	w := postWebhook(r, map[string]string{"X-Gitlab-Event": "Push Hook", "X-Gitlab-Token": "s3cret"},
		gitlabPushBody("https://gitlab.com/org/blog.git", "refs/heads/main", pushSHA))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (paused is ignored, not retried)", w.Code)
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ignored" || resp["reason"] != "data source is paused" {
		t.Fatalf("resp = %v", resp)
	}
}

func TestWebhookNotFound(t *testing.T) {
	svc := &stubWebhookService{get: func(_ context.Context, _ string) (*types.DataSource, error) {
		return nil, errors.New("data source not found")
	}}
	r := webhookRouter(t, svc)

	w := postWebhook(
		r,
		map[string]string{"X-Gitlab-Token": "x"},
		gitlabPushBody("https://gitlab.com/org/blog.git", "refs/heads/main", pushSHA),
	)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestWebhookLookupErrorIs500(t *testing.T) {
	svc := &stubWebhookService{get: func(_ context.Context, _ string) (*types.DataSource, error) {
		return nil, context.DeadlineExceeded
	}}
	r := webhookRouter(t, svc)

	w := postWebhook(
		r,
		map[string]string{"X-Gitlab-Token": "x"},
		gitlabPushBody("https://gitlab.com/org/blog.git", "refs/heads/main", pushSHA),
	)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 so the platform retries", w.Code)
	}
}

func TestWebhookSecretFromCredentials(t *testing.T) {
	t.Setenv(webhookSecretEnv, "")
	svc := &stubWebhookService{get: func(_ context.Context, _ string) (*types.DataSource, error) {
		cfg := types.DataSourceConfig{
			Credentials: map[string]interface{}{"webhook_secret": "from-creds"},
			Settings: map[string]interface{}{
				"repos": []interface{}{map[string]interface{}{"repo_url": "https://gitlab.com/org/blog.git"}},
			},
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return &types.DataSource{
			ID: "ds-1", Type: types.ConnectorTypeGitRepo,
			Status: types.DataSourceStatusActive, Config: raw,
		}, nil
	}}
	r := webhookRouter(t, svc)
	w := postWebhook(r, map[string]string{"X-Gitlab-Event": "Push Hook", "X-Gitlab-Token": "from-creds"},
		gitlabPushBody("https://gitlab.com/org/blog.git", "refs/heads/main", pushSHA))
	if w.Code != http.StatusOK || svc.syncCalls != 1 {
		t.Fatalf("status = %d syncCalls = %d, want 200/1", w.Code, svc.syncCalls)
	}
}

func TestWebhookGitHubPingIgnored(t *testing.T) {
	svc := &stubWebhookService{get: func(_ context.Context, _ string) (*types.DataSource, error) {
		return gitRepoDataSource(t, map[string]interface{}{
			"webhook_secret": "s3cret",
			"repos":          []interface{}{map[string]interface{}{"repo_url": "https://github.com/org/blog"}},
		}), nil
	}}
	r := webhookRouter(t, svc)
	body := map[string]interface{}{"zen": "Keep it logically awesome.", "hook_id": 1}
	raw, _ := json.Marshal(body)
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(raw)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/datasource/webhooks/git/ds-1", bytes.NewReader(raw))
	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || svc.syncCalls != 0 {
		t.Fatalf("ping status=%d syncCalls=%d, want 200/0", w.Code, svc.syncCalls)
	}
}

func TestWebhookBodyTooLarge(t *testing.T) {
	prev := maxWebhookBodyBytes
	maxWebhookBodyBytes = 64
	t.Cleanup(func() { maxWebhookBodyBytes = prev })
	svc := &stubWebhookService{get: func(_ context.Context, _ string) (*types.DataSource, error) {
		return gitRepoDataSource(t, map[string]interface{}{
			"webhook_secret": "s3cret",
			"repos":          []interface{}{map[string]interface{}{"repo_url": "https://gitlab.com/org/blog.git"}},
		}), nil
	}}
	r := webhookRouter(t, svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/datasource/webhooks/git/ds-1",
		bytes.NewReader(bytes.Repeat([]byte("a"), 80)))
	req.Header.Set("X-Gitlab-Token", "s3cret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
}
