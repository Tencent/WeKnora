package handler

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/datasource/connector/git_repo"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// webhookSecretEnv is the fallback shared secret when a data source has no
	// settings.webhook_secret of its own.
	webhookSecretEnv = "GIT_REPO_WEBHOOK_SECRET"
	// webhookSecretSetting overrides the env secret per data source.
	webhookSecretSetting = "webhook_secret"
	maxWebhookBodyBytes  = 1 << 20 // push payloads with big diffs stay well under 1 MB
)

var (
	gitlabTokenHeader  = http.CanonicalHeaderKey("X-Gitlab-Token")
	gitlabEventHeader  = http.CanonicalHeaderKey("X-Gitlab-Event")
	githubEventHeader  = http.CanonicalHeaderKey("X-GitHub-Event")
	githubSig256Header = http.CanonicalHeaderKey("X-Hub-Signature-256")
	githubSig1Header   = http.CanonicalHeaderKey("X-Hub-Signature")
)

// DataSourceWebhookHandler handles public git push webhooks (GitLab and
// GitHub). It is registered before the auth middleware — senders cannot
// attach WeKnora credentials — and authenticates with each platform's own
// scheme: GitLab's X-Gitlab-Token shared secret and GitHub's HMAC signature.
type DataSourceWebhookHandler struct {
	service interfaces.DataSourceService
}

// NewDataSourceWebhookHandler creates a webhook handler for git data sources.
func NewDataSourceWebhookHandler(service interfaces.DataSourceService) *DataSourceWebhookHandler {
	return &DataSourceWebhookHandler{service: service}
}

// gitPushEvent carries the push fields both platforms share. GitLab fills
// Repository.GitHTTPURL; GitHub fills CloneURL / HTMLURL.
type gitPushEvent struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Deleted    bool   `json:"deleted"` // GitHub branch deletion
	Repository struct {
		GitHTTPURL string `json:"git_http_url"` // GitLab
		CloneURL   string `json:"clone_url"`    // GitHub
		HTMLURL    string `json:"html_url"`     // GitHub
	} `json:"repository"`
}

// HandleGitPush accepts GitLab ("Push Hook") and GitHub ("push") webhooks for
// a git_repo data source, verifies the shared secret (token or HMAC), matches
// the pushed repository against the data source's configured repos, and
// triggers an incremental sync.
//
// @Summary Git push webhook (GitLab / GitHub)
// @Description Public endpoint (no WeKnora auth; verified via X-Gitlab-Token or X-Hub-Signature-256).
// @Description Triggers an incremental sync of the git_repo data source when the pushed repo/branch matches.
// @Tags DataSource
// @Param id path string true "Data source ID"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /datasource/webhooks/git/{id} [post]
func (h *DataSourceWebhookHandler) HandleGitPush(c *gin.Context) {
	ctx := c.Request.Context()
	dsID := c.Param("id")

	ds, err := h.service.GetDataSource(ctx, dsID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "data source not found"})
		return
	}
	if ds.Type != types.ConnectorTypeGitRepo {
		c.JSON(http.StatusBadRequest, gin.H{"error": "webhook is only supported for git_repo data sources"})
		return
	}
	cfg, err := ds.ParseConfig()
	if err != nil || cfg == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to parse data source config"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBodyBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}
	if len(body) > maxWebhookBodyBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "body too large"})
		return
	}

	secret := resolveWebhookSecret(cfg)
	if secret == "" {
		// Fail closed: an unauthenticated endpoint must not be triggerable.
		c.JSON(http.StatusForbidden, gin.H{"error": "webhook secret not configured"})
		return
	}

	isGitLab := c.GetHeader(gitlabEventHeader) != "" || c.GetHeader(gitlabTokenHeader) != ""
	isGitHub := c.GetHeader(githubEventHeader) != ""
	switch {
	case isGitLab:
		if !verifyGitlabToken(c.GetHeader(gitlabTokenHeader), secret) {
			c.JSON(http.StatusForbidden, gin.H{"error": "invalid token"})
			return
		}
	case isGitHub:
		if !verifyGitHubSignature(c.GetHeader(githubSig256Header), c.GetHeader(githubSig1Header), secret, body) {
			c.JSON(http.StatusForbidden, gin.H{"error": "invalid signature"})
			return
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing webhook platform headers"})
		return
	}

	var event gitPushEvent
	if err := json.Unmarshal(body, &event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON payload"})
		return
	}

	// Only branch pushes matter; tags arrive with a refs/tags/ ref (or, on
	// GitLab, as a separate "Tag Push Hook" event we never get here with).
	branch, ok := strings.CutPrefix(event.Ref, "refs/heads/")
	if !ok {
		ignore(c, "not a branch push")
		return
	}
	// Branch deletion (all-zero after SHA / GitHub deleted flag): nothing to
	// sync, and the connector would fail to fetch the gone branch.
	if event.Deleted || isNullSHA(event.After) {
		ignore(c, "branch deleted")
		return
	}

	repoURL := event.Repository.GitHTTPURL
	if repoURL == "" {
		repoURL = event.Repository.CloneURL
	}
	if repoURL == "" {
		repoURL = event.Repository.HTMLURL
	}
	if repoURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "payload carries no repository URL"})
		return
	}
	if !git_repo.MatchPush(cfg, repoURL, branch) {
		// 200 so the platform stops retrying a webhook pointed at the wrong repo.
		ignore(c, "repository not configured for this data source")
		return
	}

	if _, err := h.service.WebhookSync(ctx, dsID); err != nil {
		if errors.Is(err, datasource.ErrDataSourceNotActive) {
			c.JSON(http.StatusConflict, gin.H{"error": "data source is not active"})
			return
		}
		logger.Errorf(ctx, "webhook sync enqueue failed: ds=%s err=%v", dsID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to trigger sync"})
		return
	}
	logger.Infof(ctx, "webhook triggered sync: ds=%s repo=%s branch=%s", dsID, repoURL, branch)
	c.JSON(http.StatusOK, gin.H{"status": "triggered"})
}

// ignore responds 200 with an ignore reason: the event was authenticated but
// warrants no sync, and a 2xx stops the platform from retrying.
func ignore(c *gin.Context, reason string) {
	c.JSON(http.StatusOK, gin.H{"status": "ignored", "reason": reason})
}

// resolveWebhookSecret prefers the per-data-source secret (connector settings),
// falling back to the process-wide env var.
func resolveWebhookSecret(cfg *types.DataSourceConfig) string {
	if cfg == nil {
		return ""
	}
	if s, ok := cfg.Settings[webhookSecretSetting].(string); ok && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(os.Getenv(webhookSecretEnv))
}

// verifyGitlabToken compares the X-Gitlab-Token header against the secret in
// constant time.
func verifyGitlabToken(token, secret string) bool {
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1
}

// verifyGitHubSignature checks X-Hub-Signature-256 (preferred) or the legacy
// sha1 X-Hub-Signature — an HMAC of the raw request body with the secret.
func verifyGitHubSignature(sig256, sig1, secret string, body []byte) bool {
	if sig256 != "" && verifyHMACSignature("sha256", sig256, sha256.New, secret, body) {
		return true
	}
	if sig1 != "" && verifyHMACSignature("sha1", sig1, sha1.New, secret, body) {
		return true
	}
	return false
}

// verifyHMACSignature verifies a "<algo>=<hex>" HMAC header over body in
// constant time.
func verifyHMACSignature(algo, sigHeader string, newHash func() hash.Hash, secret string, body []byte) bool {
	prefix, digest, ok := strings.Cut(sigHeader, "=")
	if !ok {
		return false
	}
	expect, err := hex.DecodeString(strings.TrimSpace(digest))
	if err != nil {
		return false
	}
	// Accept both "sha256=<hex>" and "<hex>" forms; reject mismatched prefixes.
	if prefix != algo && prefix != "" {
		return false
	}
	mac := hmac.New(newHash, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), expect)
}

// isNullSHA reports whether sha is the all-zero placeholder Git platforms
// send for ref deletions.
func isNullSHA(sha string) bool {
	return sha != "" && strings.Trim(sha, "0") == ""
}
