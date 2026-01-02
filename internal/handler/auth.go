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

// AuthHandler 사용자 인증을 위한 HTTP 요청 핸들러 구현
// REST API 엔드포인트를 통해 사용자 등록, 로그인, 로그아웃 및 토큰 관리 기능 제공
type AuthHandler struct {
	userService   interfaces.UserService
	tenantService interfaces.TenantService
	configInfo    *config.Config
}

// NewAuthHandler 제공된 서비스로 새로운 인증 핸들러 인스턴스 생성
// 매개변수:
//   - userService: 비즈니스 로직을 위한 UserService 인터페이스 구현체
//   - tenantService: 테넌트 관리를 위한 TenantService 인터페이스 구현체
//
// 반환값: 새로 생성된 AuthHandler에 대한 포인터
func NewAuthHandler(configInfo *config.Config,
	userService interfaces.UserService, tenantService interfaces.TenantService) *AuthHandler {
	return &AuthHandler{
		configInfo:    configInfo,
		userService:   userService,
		tenantService: tenantService,
	}
}

// Register godoc
// @Summary      사용자 등록
// @Description  새 사용자 계정 등록
// @Tags         인증
// @Accept       json
// @Produce      json
// @Param        request  body      types.RegisterRequest  true  "등록 요청 매개변수"
// @Success      201      {object}  types.RegisterResponse
// @Failure      400      {object}  errors.AppError  "요청 매개변수 오류"
// @Failure      403      {object}  errors.AppError  "등록 기능 비활성화됨"
// @Router       /auth/register [post]
func (h *AuthHandler) Register(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start user registration")

	// DISABLE_REGISTRATION=true 환경 변수를 통해 등록 금지
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

	// 필수 필드 검증
	if req.Username == "" || req.Email == "" || req.Password == "" {
		logger.Error(ctx, "Missing required registration fields")
		appErr := errors.NewValidationError("Username, email and password are required")
		c.Error(appErr)
		return
	}
	req.Username = secutils.SanitizeForLog(req.Username)
	req.Email = secutils.SanitizeForLog(req.Email)
	// 서비스를 호출하여 사용자 등록
	user, err := h.userService.Register(ctx, &req)
	if err != nil {
		logger.Errorf(ctx, "Failed to register user: %v", err)
		appErr := errors.NewBadRequestError("Registration failed").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	// 성공 응답 반환
	response := &types.RegisterResponse{
		Success: true,
		Message: "Registration successful",
		User:    user,
	}

	logger.Infof(ctx, "User registered successfully: %s", secutils.SanitizeForLog(user.Email))
	c.JSON(http.StatusCreated, response)
}

// Login godoc
// @Summary      사용자 로그인
// @Description  사용자 로그인 및 액세스 토큰 획득
// @Tags         인증
// @Accept       json
// @Produce      json
// @Param        request  body      types.LoginRequest  true  "로그인 요청 매개변수"
// @Success      200      {object}  types.LoginResponse
// @Failure      401      {object}  errors.AppError  "인증 실패"
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

	// 필수 필드 검증
	if req.Email == "" || req.Password == "" {
		logger.Error(ctx, "Missing required login fields")
		appErr := errors.NewValidationError("Email and password are required")
		c.Error(appErr)
		return
	}

	// 서비스를 호출하여 사용자 인증
	response, err := h.userService.Login(ctx, &req)
	if err != nil {
		logger.Errorf(ctx, "Failed to login user: %v", err)
		appErr := errors.NewUnauthorizedError("Login failed").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	// 로그인 성공 여부 확인
	if !response.Success {
		logger.Warnf(ctx, "Login failed: %s", response.Message)
		c.JSON(http.StatusUnauthorized, response)
		return
	}

	// 사용자는 이미 서비스에서 올바른 형식으로 반환됨

	logger.Infof(ctx, "User logged in successfully, email: %s", email)
	c.JSON(http.StatusOK, response)
}

// Logout godoc
// @Summary      사용자 로그아웃
// @Description  현재 액세스 토큰 취소 및 로그아웃
// @Tags         인증
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "로그아웃 성공"
// @Failure      400  {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Router       /auth/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start user logout")

	// Authorization 헤더에서 토큰 추출
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		logger.Error(ctx, "Missing Authorization header")
		appErr := errors.NewValidationError("Authorization header is required")
		c.Error(appErr)
		return
	}

	// Bearer 토큰 파싱
	tokenParts := strings.Split(authHeader, " ")
	if len(tokenParts) != 2 || tokenParts[0] != "Bearer" {
		logger.Error(ctx, "Invalid Authorization header format")
		appErr := errors.NewValidationError("Invalid Authorization header format")
		c.Error(appErr)
		return
	}

	token := tokenParts[1]

	// 토큰 취소
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
// @Summary      토큰 갱신
// @Description  갱신 토큰을 사용하여 새로운 액세스 토큰 획득
// @Tags         인증
// @Accept       json
// @Produce      json
// @Param        request  body      object{refreshToken=string}  true  "갱신 토큰"
// @Success      200      {object}  map[string]interface{}       "새 토큰"
// @Failure      401      {object}  errors.AppError              "토큰 무효"
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

	// 서비스를 호출하여 토큰 갱신
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
// @Summary      현재 사용자 정보 조회
// @Description  현재 로그인한 사용자의 상세 정보 조회
// @Tags         인증
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "사용자 정보"
// @Failure      401  {object}  errors.AppError         "미승인"
// @Security     Bearer
// @Router       /auth/me [get]
func (h *AuthHandler) GetCurrentUser(c *gin.Context) {
	ctx := c.Request.Context()

	// 서비스에서 현재 사용자 가져오기 (컨텍스트에서 추출)
	user, err := h.userService.GetCurrentUser(ctx)
	if err != nil {
		logger.Errorf(ctx, "Failed to get current user: %v", err)
		appErr := errors.NewUnauthorizedError("Failed to get user information").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	// 테넌트 정보 가져오기
	var tenant *types.Tenant
	if user.TenantID > 0 {
		tenant, err = h.tenantService.GetTenantByID(ctx, user.TenantID)
		if err != nil {
			logger.Warnf(ctx, "Failed to get tenant info for user %s, tenant ID %d: %v", user.Email, user.TenantID, err)
			// 테넌트 정보를 사용할 수 없는 경우 요청을 실패 처리하지 않음
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
// @Summary      비밀번호 변경
// @Description  현재 사용자의 로그인 비밀번호 변경
// @Tags         인증
// @Accept       json
// @Produce      json
// @Param        request  body      object{old_password=string,new_password=string}  true  "비밀번호 변경 요청"
// @Success      200      {object}  map[string]interface{}                           "변경 성공"
// @Failure      400      {object}  errors.AppError                                  "요청 매개변수 오류"
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

	// 현재 사용자 가져오기
	user, err := h.userService.GetCurrentUser(ctx)
	if err != nil {
		logger.Errorf(ctx, "Failed to get current user: %v", err)
		appErr := errors.NewUnauthorizedError("Failed to get user information").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	// 비밀번호 변경
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
// @Summary      토큰 검증
// @Description  액세스 토큰이 유효한지 검증
// @Tags         인증
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "토큰 유효"
// @Failure      401  {object}  errors.AppError         "토큰 무효"
// @Security     Bearer
// @Router       /auth/validate [get]
func (h *AuthHandler) ValidateToken(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start token validation")

	// Authorization 헤더에서 토큰 추출
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		logger.Error(ctx, "Missing Authorization header")
		appErr := errors.NewValidationError("Authorization header is required")
		c.Error(appErr)
		return
	}

	// Bearer 토큰 파싱
	tokenParts := strings.Split(authHeader, " ")
	if len(tokenParts) != 2 || tokenParts[0] != "Bearer" {
		logger.Error(ctx, "Invalid Authorization header format")
		appErr := errors.NewValidationError("Invalid Authorization header format")
		c.Error(appErr)
		return
	}

	token := tokenParts[1]

	// 토큰 검증
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
