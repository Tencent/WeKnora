package handler

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// AuthHandler implements HTTP request handlers for user authentication
// Provides functionality for user registration, login, logout, and token management
// through the REST API endpoints
type AuthHandler struct {
	userService   interfaces.UserService
	tenantService interfaces.TenantService
	configInfo    *config.Config
}

// NewAuthHandler creates a new auth handler instance with the provided services
// Parameters:
//   - userService: An implementation of the UserService interface for business logic
//   - tenantService: An implementation of the TenantService interface for tenant management
//
// Returns a pointer to the newly created AuthHandler
func NewAuthHandler(configInfo *config.Config,
	userService interfaces.UserService, tenantService interfaces.TenantService) *AuthHandler {
	return &AuthHandler{
		configInfo:    configInfo,
		userService:   userService,
		tenantService: tenantService,
	}
}

// Register godoc
// @Summary      用户注册
// @Description  注册新用户账号
// @Tags         认证
// @Accept       json
// @Produce      json
// @Param        request  body      types.RegisterRequest  true  "注册请求参数"
// @Success      201      {object}  types.RegisterResponse
// @Failure      400      {object}  errors.AppError  "请求参数错误"
// @Failure      403      {object}  errors.AppError  "注册功能已禁用"
// @Router       /auth/register [post]
func (h *AuthHandler) Register(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start user registration")

	// 通过环境变量 DISABLE_REGISTRATION=true 禁止注册
	if os.Getenv("DISABLE_REGISTRATION") == "true" {
		logger.Warn(ctx, "Registration is disabled by DISABLE_REGISTRATION env")
		appErr := errors.NewForbiddenError("Registration is disabled")
		c.Error(appErr)
		return
	}

	var req types.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse registration request parameters", err)
		appErr := errors.NewValidationError("Invalid registration parameters").WithDetails(err.Error())
		c.Error(appErr)
		return
	}
	req.Username = secutils.SanitizeForLog(req.Username)
	req.Email = secutils.SanitizeForLog(req.Email)
	req.Password = secutils.SanitizeForLog(req.Password)

	// Validate required fields
	if req.Username == "" || req.Email == "" || req.Password == "" {
		logger.Error(ctx, "Missing required registration fields")
		appErr := errors.NewValidationError("Username, email and password are required")
		c.Error(appErr)
		return
	}
	req.Username = secutils.SanitizeForLog(req.Username)
	req.Email = secutils.SanitizeForLog(req.Email)
	// Call service to register user
	user, err := h.userService.Register(ctx, &req)
	if err != nil {
		logger.Errorf(ctx, "Failed to register user: %v", err)
		appErr := errors.NewBadRequestError("Registration failed").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	// Return success response
	response := &types.RegisterResponse{
		Success: true,
		Message: "Registration successful",
		User:    user,
	}

	logger.Infof(ctx, "User registered successfully: %s", secutils.SanitizeForLog(user.Email))
	c.JSON(http.StatusCreated, response)
}

// Login godoc
// @Summary      用户登录
// @Description  用户登录并获取访问令牌
// @Tags         认证
// @Accept       json
// @Produce      json
// @Param        request  body      types.LoginRequest  true  "登录请求参数"
// @Success      200      {object}  types.LoginResponse
// @Failure      401      {object}  errors.AppError  "认证失败"
// @Router       /auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start user login")

	var req types.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse login request parameters", err)
		appErr := errors.NewValidationError("Invalid login parameters").WithDetails(err.Error())
		c.Error(appErr)
		return
	}
	email := secutils.SanitizeForLog(req.Email)

	// Validate required fields
	if req.Email == "" || req.Password == "" {
		logger.Error(ctx, "Missing required login fields")
		appErr := errors.NewValidationError("Email and password are required")
		c.Error(appErr)
		return
	}

	// Call service to authenticate user
	response, err := h.userService.Login(ctx, &req)
	if err != nil {
		logger.Errorf(ctx, "Failed to login user: %v", err)
		appErr := errors.NewUnauthorizedError("Login failed").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	// Check if login was successful
	if !response.Success {
		logger.Warnf(ctx, "Login failed: %s", response.Message)
		c.JSON(http.StatusUnauthorized, response)
		return
	}

	// User is already in the correct format from service

	logger.Infof(ctx, "User logged in successfully, email: %s", email)
	c.JSON(http.StatusOK, response)
}

// Logout godoc
// @Summary      用户登出
// @Description  撤销当前访问令牌并登出
// @Tags         认证
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "登出成功"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Router       /auth/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start user logout")

	// Extract token from Authorization header
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		logger.Error(ctx, "Missing Authorization header")
		appErr := errors.NewValidationError("Authorization header is required")
		c.Error(appErr)
		return
	}

	// Parse Bearer token
	tokenParts := strings.Split(authHeader, " ")
	if len(tokenParts) != 2 || tokenParts[0] != "Bearer" {
		logger.Error(ctx, "Invalid Authorization header format")
		appErr := errors.NewValidationError("Invalid Authorization header format")
		c.Error(appErr)
		return
	}

	token := tokenParts[1]

	// Revoke token
	err := h.userService.RevokeToken(ctx, token)
	if err != nil {
		logger.Errorf(ctx, "Failed to revoke token: %v", err)
		appErr := errors.NewInternalServerError("Logout failed").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	logger.Info(ctx, "User logged out successfully")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Logout successful",
	})
}

// RefreshToken godoc
// @Summary      刷新令牌
// @Description  使用刷新令牌获取新的访问令牌
// @Tags         认证
// @Accept       json
// @Produce      json
// @Param        request  body      object{refreshToken=string}  true  "刷新令牌"
// @Success      200      {object}  map[string]interface{}       "新令牌"
// @Failure      401      {object}  errors.AppError              "令牌无效"
// @Router       /auth/refresh [post]
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start token refresh")

	var req struct {
		RefreshToken string `json:"refreshToken" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse refresh token request", err)
		appErr := errors.NewValidationError("Invalid refresh token request").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	// Call service to refresh token
	accessToken, newRefreshToken, err := h.userService.RefreshToken(ctx, req.RefreshToken)
	if err != nil {
		logger.Errorf(ctx, "Failed to refresh token: %v", err)
		appErr := errors.NewUnauthorizedError("Token refresh failed").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	logger.Info(ctx, "Token refreshed successfully")
	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"message":       "Token refreshed successfully",
		"access_token":  accessToken,
		"refresh_token": newRefreshToken,
	})
}

// GetCurrentUser godoc
// @Summary      获取当前用户信息
// @Description  获取当前登录用户的详细信息
// @Tags         认证
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "用户信息"
// @Failure      401  {object}  errors.AppError         "未授权"
// @Security     Bearer
// @Router       /auth/me [get]
func (h *AuthHandler) GetCurrentUser(c *gin.Context) {
	ctx := c.Request.Context()

	// Get current user from service (which extracts from context)
	user, err := h.userService.GetCurrentUser(ctx)
	if err != nil {
		logger.Errorf(ctx, "Failed to get current user: %v", err)
		appErr := errors.NewUnauthorizedError("Failed to get user information").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	// Get tenant information
	var tenant *types.Tenant
	if user.TenantID > 0 {
		tenant, err = h.tenantService.GetTenantByID(ctx, user.TenantID)
		if err != nil {
			logger.Warnf(ctx, "Failed to get tenant info for user %s, tenant ID %d: %v", user.Email, user.TenantID, err)
			// Don't fail the request if tenant info is not available
		}
	}
	userInfo := user.ToUserInfo()
	userInfo.CanAccessAllTenants = user.CanAccessAllTenants && h.configInfo.Tenant.EnableCrossTenantAccess
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"user":   userInfo,
			"tenant": tenant,
		},
	})
}

// ChangePassword godoc
// @Summary      修改密码
// @Description  修改当前用户的登录密码
// @Tags         认证
// @Accept       json
// @Produce      json
// @Param        request  body      object{old_password=string,new_password=string}  true  "密码修改请求"
// @Success      200      {object}  map[string]interface{}                           "修改成功"
// @Failure      400      {object}  errors.AppError                                  "请求参数错误"
// @Security     Bearer
// @Router       /auth/change-password [post]
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start password change")

	var req struct {
		OldPassword string `json:"old_password" binding:"required"`
		NewPassword string `json:"new_password" binding:"required,min=6"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse password change request", err)
		appErr := errors.NewValidationError("Invalid password change request").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	// Get current user
	user, err := h.userService.GetCurrentUser(ctx)
	if err != nil {
		logger.Errorf(ctx, "Failed to get current user: %v", err)
		appErr := errors.NewUnauthorizedError("Failed to get user information").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	// Change password
	err = h.userService.ChangePassword(ctx, user.ID, req.OldPassword, req.NewPassword)
	if err != nil {
		logger.Errorf(ctx, "Failed to change password: %v", err)
		appErr := errors.NewBadRequestError("Password change failed").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	logger.Infof(ctx, "Password changed successfully for user: %s", user.Email)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Password changed successfully",
	})
}

// ValidateToken godoc
// @Summary      验证令牌
// @Description  验证访问令牌是否有效
// @Tags         认证
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "令牌有效"
// @Failure      401  {object}  errors.AppError         "令牌无效"
// @Security     Bearer
// @Router       /auth/validate [get]
func (h *AuthHandler) ValidateToken(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start token validation")

	// Extract token from Authorization header
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		logger.Error(ctx, "Missing Authorization header")
		appErr := errors.NewValidationError("Authorization header is required")
		c.Error(appErr)
		return
	}

	// Parse Bearer token
	tokenParts := strings.Split(authHeader, " ")
	if len(tokenParts) != 2 || tokenParts[0] != "Bearer" {
		logger.Error(ctx, "Invalid Authorization header format")
		appErr := errors.NewValidationError("Invalid Authorization header format")
		c.Error(appErr)
		return
	}

	token := tokenParts[1]

	// Validate token
	user, err := h.userService.ValidateToken(ctx, token)
	if err != nil {
		logger.Errorf(ctx, "Failed to validate token: %v", err)
		appErr := errors.NewUnauthorizedError("Token validation failed").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	logger.Infof(ctx, "Token validated successfully for user: %s", user.Email)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Token is valid",
		"user":    user.ToUserInfo(),
	})
}
