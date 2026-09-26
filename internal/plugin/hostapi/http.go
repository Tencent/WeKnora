package hostapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// PathPrefix is where the Host API is served. The global auth middleware
// lets it through; Handler authenticates plugin tokens itself.
const PathPrefix = "/api/v1/plugin-host/"

const claimsKey = "pluginHostClaims"

// Handler serves the Host API.
type Handler struct {
	issuer *Issuer
	kv     *KV
}

// NewHandler creates the Host API handler.
func NewHandler(issuer *Issuer, kv *KV) *Handler { return &Handler{issuer: issuer, kv: kv} }

func writeErr(c *gin.Context, err error) {
	var e *pluginapi.Error
	if !errors.As(err, &e) {
		logger.Errorf(c.Request.Context(), "[plugin] host api %s: %v", c.Request.URL.Path, err)
		e = &pluginapi.Error{Code: pluginapi.CodeInternal, Message: "host api failed"}
	}
	c.AbortWithStatusJSON(e.Code.HTTPStatus(), pluginapi.ErrorBody{Error: *e})
}

// authenticate verifies the plugin token and the scope the route needs.
func (h *Handler) authenticate(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || token == "" {
			writeErr(c, pluginapi.Errorf(pluginapi.CodeUnauthorized, "missing plugin token"))
			return
		}
		claims, err := h.issuer.Verify(token)
		if err != nil {
			writeErr(c, pluginapi.Errorf(pluginapi.CodeUnauthorized, "invalid or expired plugin token"))
			return
		}
		if !claims.Has(scope) {
			writeErr(
				c,
				pluginapi.Errorf(pluginapi.CodeUnauthorized, "the plugin was not granted the %q scope", scope),
			)
			return
		}
		c.Set(claimsKey, claims)
		c.Next()
	}
}

func claimsOf(c *gin.Context) *Claims {
	v, _ := c.Get(claimsKey)
	claims, _ := v.(*Claims)
	return claims
}

// Register mounts the Host API on the engine root.
func (h *Handler) Register(r gin.IRouter) {
	g := r.Group(strings.TrimSuffix(PathPrefix, "/"))
	kv := g.Group("/kv", h.authenticate(ScopeKV))
	kv.GET("", h.kvGet)
	kv.PUT("", h.kvPut)
	kv.DELETE("", h.kvDelete)
	kv.GET("/list", h.kvList)
}

func (h *Handler) kvGet(c *gin.Context) {
	e, err := h.kv.Get(c.Request.Context(), claimsOf(c), c.Query("key"))
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, e)
}

func (h *Handler) kvPut(c *gin.Context) {
	var in pluginapi.KVPut
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, pluginapi.KVMaxValueBytes+pluginapi.KVMaxKeyBytes+1024))
	if err == nil {
		err = json.Unmarshal(body, &in)
	}
	if err != nil {
		writeErr(c, pluginapi.Errorf(pluginapi.CodeBadRequest, "body must be {key, value, ttlSeconds}"))
		return
	}
	e, err := h.kv.Put(c.Request.Context(), claimsOf(c), in)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, e)
}

func (h *Handler) kvDelete(c *gin.Context) {
	if err := h.kv.Delete(c.Request.Context(), claimsOf(c), c.Query("key")); err != nil {
		writeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) kvList(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	out, err := h.kv.List(c.Request.Context(), claimsOf(c), c.Query("prefix"), c.Query("after"), limit)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
