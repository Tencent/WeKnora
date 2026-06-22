package handler

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"html"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// DataSourceOAuthHandler drives the OAuth2 authorization-code flow that lets a
// data source act with an end user's identity (个人身份) instead of the app
// identity. It is connector-agnostic: any connector implementing
// datasource.OAuthConnector (currently Feishu) is supported.
//
// Flow:
//  1. Authorize (Admin+, authenticated): builds a signed state and returns the
//     provider consent URL. The frontend opens it in a popup.
//  2. Callback (no-auth, hit by the provider's browser redirect): verifies the
//     signed state, exchanges the code for user tokens, persists them, and
//     returns a small HTML page that posts the result back to the opener.
//
// State is an HMAC-SHA256(SYSTEM_AES_KEY) signature over the data source ID,
// redirect URI and an expiry. Because the callback is unauthenticated, the
// signature is what binds the returned code to a specific, caller-authorized
// data source and prevents tampering.
type DataSourceOAuthHandler struct {
	service   interfaces.DataSourceService
	kbService interfaces.KnowledgeBaseService
}

// NewDataSourceOAuthHandler creates a new data source OAuth handler.
func NewDataSourceOAuthHandler(
	service interfaces.DataSourceService,
	kbService interfaces.KnowledgeBaseService,
) *DataSourceOAuthHandler {
	return &DataSourceOAuthHandler{service: service, kbService: kbService}
}

// oauthStateTTL bounds how long an authorization may be left pending. The
// consent page should be completed promptly; a stale state is rejected.
const oauthStateTTL = 10 * time.Minute

// ownDataSource enforces tenant isolation — same check as datasource.go and the
// credentials handler, duplicated to avoid coupling handlers via internals.
func (h *DataSourceOAuthHandler) ownDataSource(c *gin.Context) (*types.DataSource, bool) {
	ctx := c.Request.Context()
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(errors.NewBadRequestError("Tenant ID cannot be empty"))
		return nil, false
	}
	id := c.Param("id")
	ds, err := h.service.GetDataSource(ctx, id)
	if err != nil || ds == nil {
		c.Error(errors.NewNotFoundError("data source not found"))
		return nil, false
	}
	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, ds.KnowledgeBaseID)
	if err != nil || kb == nil || kb.TenantID != tenantID {
		c.Error(errors.NewNotFoundError("data source not found"))
		return nil, false
	}
	return ds, true
}

// Authorize returns the provider consent URL for a data source. Admin+.
func (h *DataSourceOAuthHandler) Authorize(c *gin.Context) {
	ds, ok := h.ownDataSource(c)
	if !ok {
		return
	}
	redirectURI := strings.TrimSpace(c.Query("redirect_uri"))
	if redirectURI == "" {
		c.Error(errors.NewBadRequestError("redirect_uri is required"))
		return
	}

	state, err := signOAuthState(&oauthStatePayload{
		DataSourceID: ds.ID,
		RedirectURI:  redirectURI,
		Nonce:        randomNonce(),
		Exp:          time.Now().Add(oauthStateTTL).Unix(),
	})
	if err != nil {
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	authURL, err := h.service.BuildOAuthAuthorizeURL(c.Request.Context(), ds.ID, redirectURI, state)
	if err != nil {
		if stderrors.Is(err, datasource.ErrOAuthUnsupported) {
			c.Error(errors.NewBadRequestError("this connector does not support user authorization"))
			return
		}
		logger.ErrorWithFields(c.Request.Context(), err, map[string]interface{}{
			"data_source_id": secutils.SanitizeForLog(ds.ID),
		})
		c.Error(errors.NewBadRequestError("failed to build authorization URL: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"authorize_url": authURL},
	})
}

// Callback handles the provider's browser redirect. No-auth: it is reached
// directly by the user's browser without a WeKnora session, so authorization is
// established entirely by the signed state. It always renders an HTML page
// (never a JSON error) because a human is looking at it.
func (h *DataSourceOAuthHandler) Callback(c *gin.Context) {
	ctx := c.Request.Context()

	if provErr := strings.TrimSpace(c.Query("error")); provErr != "" {
		desc := strings.TrimSpace(c.Query("error_description"))
		h.renderResult(c, false, strings.TrimSpace(provErr+" "+desc))
		return
	}

	payload, err := verifyOAuthState(strings.TrimSpace(c.Query("state")))
	if err != nil {
		logger.Errorf(ctx, "[DataSourceOAuth] invalid state: %v", err)
		h.renderResult(c, false, "invalid_state")
		return
	}

	code := strings.TrimSpace(c.Query("code"))
	if code == "" {
		h.renderResult(c, false, "missing_code")
		return
	}

	if err := h.service.CompleteOAuth(ctx, payload.DataSourceID, code, payload.RedirectURI); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"data_source_id": secutils.SanitizeForLog(payload.DataSourceID),
		})
		h.renderResult(c, false, err.Error())
		return
	}

	h.renderResult(c, true, "")
}

// renderResult returns a minimal HTML page that posts the outcome to the opener
// window (the data source editor that launched the popup) and then closes.
func (h *DataSourceOAuthHandler) renderResult(c *gin.Context, success bool, message string) {
	msg := map[string]interface{}{"type": "weknora-datasource-oauth", "success": success}
	if message != "" {
		msg["message"] = message
	}
	// json.Marshal escapes <, >, & by default, so the payload is safe to embed
	// directly inside a <script> block.
	msgJSON, _ := json.Marshal(msg)

	statusText := "授权成功，请返回 WeKnora 窗口。"
	if !success {
		statusText = "授权失败：" + html.EscapeString(message)
	}

	page := fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh">
<head><meta charset="utf-8"><title>WeKnora</title></head>
<body style="font-family:system-ui,sans-serif;padding:2.5rem;text-align:center;color:#333">
<p>%s</p>
<script>(function(){try{if(window.opener){window.opener.postMessage(%s,"*");}}catch(e){}setTimeout(function(){window.close();},800);})();</script>
</body>
</html>`, statusText, string(msgJSON))

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(page))
}

// --- Signed state helpers ---

type oauthStatePayload struct {
	DataSourceID string `json:"ds_id"`
	RedirectURI  string `json:"redirect_uri"`
	Nonce        string `json:"nonce"`
	Exp          int64  `json:"exp"`
}

// oauthStateKey returns the HMAC key for signing OAuth state. Reuses
// SYSTEM_AES_KEY (also used for credential encryption), which is required for
// this feature — without it, refresh tokens would be stored unencrypted anyway.
func oauthStateKey() ([]byte, error) {
	key := os.Getenv("SYSTEM_AES_KEY")
	if len(key) < 16 {
		return nil, errors.NewInternalServerError(
			"data source OAuth requires SYSTEM_AES_KEY (>=16 bytes) to be configured")
	}
	return []byte(key), nil
}

func signOAuthState(payload *oauthStatePayload) (string, error) {
	key, err := oauthStateKey()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	b64 := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(b64))
	return b64 + "." + hex.EncodeToString(mac.Sum(nil)), nil
}

func verifyOAuthState(state string) (*oauthStatePayload, error) {
	key, err := oauthStateKey()
	if err != nil {
		return nil, err
	}
	b64, sig, ok := strings.Cut(state, ".")
	if !ok {
		return nil, fmt.Errorf("malformed state")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(b64))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return nil, fmt.Errorf("state signature mismatch")
	}
	raw, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("decode state: %w", err)
	}
	var payload oauthStatePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal state: %w", err)
	}
	if payload.Exp < time.Now().Unix() {
		return nil, fmt.Errorf("state expired")
	}
	if payload.DataSourceID == "" || payload.RedirectURI == "" {
		return nil, fmt.Errorf("incomplete state")
	}
	return &payload, nil
}

func randomNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
