package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/credential"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

const maxCredentialVaultBody = 16 << 10

type credentialVault interface {
	List(ctx context.Context, owner credential.Owner, site string) ([]*types.CredentialVaultItem, error)
	Create(ctx context.Context, owner credential.Owner, in credential.LoginInput) (*types.CredentialVaultItem, error)
	Update(ctx context.Context, owner credential.Owner, id string, p credential.LoginPatch) (*types.CredentialVaultItem, error)
	Delete(ctx context.Context, owner credential.Owner, id string) error
}

// CredentialVaultHandler serves /me/credential-vault. No endpoint returns a
// password; the owner always comes from the context, never the body.
type CredentialVaultHandler struct {
	vault credentialVault
}

// NewCredentialVaultHandler builds the handler.
func NewCredentialVaultHandler(v *credential.Vault) *CredentialVaultHandler {
	return &CredentialVaultHandler{vault: v}
}

type credentialVaultCreateRequest struct {
	Title    string   `json:"title"`
	Site     string   `json:"site"`
	Domains  []string `json:"domains"`
	Username string   `json:"username"`
	Password string   `json:"password"`
}

type credentialVaultUpdateRequest struct {
	Title    *string  `json:"title"`
	Domains  []string `json:"domains"`
	Username *string  `json:"username"`
	Password *string  `json:"password"`
}

func decodeVaultBody(c *gin.Context, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, maxCredentialVaultBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid request body"))
		return false
	}
	return true
}

func vaultOwner(c *gin.Context) (credential.Owner, bool) {
	owner, err := credential.OwnerFromContext(c.Request.Context())
	if err != nil {
		_ = c.Error(err)
		return credential.Owner{}, false
	}
	return owner, true
}

// List godoc
// @Summary      列出我保存的登录凭据
// @Description  只返回元数据（站点、域名、用户名、最近使用时间），不返回密码。available 表示服务端是否可以保存凭据。
// @Tags         Me
// @Produce      json
// @Param        site  query     string                  false  "只列出该站点"
// @Success      200   {object}  map[string]interface{}  "{success, data, available}"
// @Security     Bearer
// @Router       /me/credential-vault [get]
func (h *CredentialVaultHandler) List(c *gin.Context) {
	owner, ok := vaultOwner(c)
	if !ok {
		return
	}
	items, err := h.vault.List(c.Request.Context(), owner, c.Query("site"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": items, "available": credential.Available()})
}

// Create godoc
// @Summary      保存一条登录凭据
// @Tags         Me
// @Accept       json
// @Produce      json
// @Param        request  body      credentialVaultCreateRequest  true  "站点、域名、用户名与密码"
// @Success      200      {object}  map[string]interface{}        "保存的元数据"
// @Failure      409      {object}  apperrors.AppError            "该站点与用户名已存在"
// @Security     Bearer
// @Router       /me/credential-vault [post]
func (h *CredentialVaultHandler) Create(c *gin.Context) {
	owner, ok := vaultOwner(c)
	if !ok {
		return
	}
	var req credentialVaultCreateRequest
	if !decodeVaultBody(c, &req) {
		return
	}
	item, err := h.vault.Create(c.Request.Context(), owner, credential.LoginInput{
		Title: req.Title, Site: req.Site, Domains: req.Domains, Username: req.Username, Password: req.Password,
	})
	req.Password = ""
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": item})
}

// Update godoc
// @Summary      修改一条登录凭据
// @Description  可修改标题、域名、用户名，或设置新密码（只写不读）。
// @Tags         Me
// @Accept       json
// @Produce      json
// @Param        id       path      string                        true  "凭据 ID"
// @Param        request  body      credentialVaultUpdateRequest  true  "要修改的字段"
// @Success      200      {object}  map[string]interface{}        "修改后的元数据"
// @Security     Bearer
// @Router       /me/credential-vault/{id} [patch]
func (h *CredentialVaultHandler) Update(c *gin.Context) {
	owner, ok := vaultOwner(c)
	if !ok {
		return
	}
	var req credentialVaultUpdateRequest
	if !decodeVaultBody(c, &req) {
		return
	}
	item, err := h.vault.Update(c.Request.Context(), owner, c.Param("id"), credential.LoginPatch{
		Title: req.Title, Domains: req.Domains, Username: req.Username, Password: req.Password,
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": item})
}

// Delete godoc
// @Summary      删除一条登录凭据
// @Tags         Me
// @Produce      json
// @Param        id   path      string                  true  "凭据 ID"
// @Success      200  {object}  map[string]interface{}  "已删除"
// @Security     Bearer
// @Router       /me/credential-vault/{id} [delete]
func (h *CredentialVaultHandler) Delete(c *gin.Context) {
	owner, ok := vaultOwner(c)
	if !ok {
		return
	}
	if err := h.vault.Delete(c.Request.Context(), owner, c.Param("id")); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
