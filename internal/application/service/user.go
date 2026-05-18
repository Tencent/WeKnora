package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

type oidcAuthorizationState struct {
	Nonce       string `json:"nonce"`
	RedirectURI string `json:"redirect_uri,omitempty"`
}

var (
	jwtSecretOnce sync.Once
	jwtSecret     string
)

// getJwtSecret retrieves the JWT secret from the environment, falling back to a securely generated random secret.
func getJwtSecret() string {
	jwtSecretOnce.Do(func() {
		if envSecret := strings.TrimSpace(os.Getenv("JWT_SECRET")); envSecret != "" {
			jwtSecret = envSecret
			return
		}

		randomBytes := make([]byte, 32)
		if _, err := rand.Read(randomBytes); err != nil {
			panic(fmt.Sprintf("failed to generate JWT secret: %v", err))
		}
		jwtSecret = base64.StdEncoding.EncodeToString(randomBytes)
	})

	return jwtSecret
}

// userService implements the UserService interface
type userService struct {
	userRepo      interfaces.UserRepository
	tokenRepo     interfaces.AuthTokenRepository
	tenantService interfaces.TenantService
	memberService interfaces.TenantMemberService
	config        *config.Config
}

// NewUserService creates a new user service instance
func NewUserService(
	configInfo *config.Config,
	userRepo interfaces.UserRepository,
	tokenRepo interfaces.AuthTokenRepository,
	tenantService interfaces.TenantService,
	memberService interfaces.TenantMemberService,
) interfaces.UserService {
	return &userService{
		userRepo:      userRepo,
		tokenRepo:     tokenRepo,
		tenantService: tenantService,
		memberService: memberService,
		config:        configInfo,
	}
}

// Register creates a new user account
func (s *userService) Register(ctx context.Context, req *types.RegisterRequest) (*types.User, error) {
	logger.Info(ctx, "Start user registration")

	// Validate input
	if req.Username == "" || req.Email == "" || req.Password == "" {
		return nil, errors.New("username, email and password are required")
	}

	// Check if user already exists
	existingUser, _ := s.userRepo.GetUserByEmail(ctx, req.Email)
	if existingUser != nil {
		return nil, errors.New("user with this email already exists")
	}

	existingUser, _ = s.userRepo.GetUserByUsername(ctx, req.Username)
	if existingUser != nil {
		return nil, errors.New("user with this username already exists")
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		logger.Errorf(ctx, "Failed to hash password: %v", err)
		return nil, errors.New("failed to process password")
	}

	// Create default tenant for the user
	// Note: RetrieverEngines is left empty - system will use defaults from RETRIEVE_DRIVER env
	tenant := &types.Tenant{
		Name:        fmt.Sprintf("%s's Workspace", secutils.SanitizeForLog(req.Username)),
		Description: "Default workspace",
		Status:      "active",
	}

	createdTenant, err := s.tenantService.CreateTenant(ctx, tenant)
	if err != nil {
		logger.Errorf(ctx, "Failed to create tenant")
		return nil, errors.New("failed to create workspace")
	}

	// Create user
	user := &types.User{
		ID:           uuid.New().String(),
		Username:     req.Username,
		Email:        req.Email,
		PasswordHash: string(hashedPassword),
		TenantID:     createdTenant.ID,
		IsActive:     true,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	err = s.userRepo.CreateUser(ctx, user)
	if err != nil {
		logger.Errorf(ctx, "Failed to create user: %v", err)
		return nil, errors.New("failed to create user")
	}

	// Bootstrap an Owner membership so the registrant has full control over
	// the tenant their account just created. Failure here only logs — the
	// user record exists and the auth middleware's orphan-tenant recovery
	// path will recreate the membership on next login.
	if s.memberService != nil {
		if _, err := s.memberService.EnsureOwner(ctx, user.ID, createdTenant.ID); err != nil {
			logger.Errorf(ctx, "Failed to create owner membership for user %s tenant %d: %v",
				user.ID, createdTenant.ID, err)
		}
	}

	logger.Info(ctx, "User registered successfully")
	return user, nil
}

// Login authenticates a user and returns tokens
func (s *userService) Login(ctx context.Context, req *types.LoginRequest) (*types.LoginResponse, error) {
	logger.Info(ctx, "Start user login")
	// Get user by email
	user, err := s.userRepo.GetUserByEmail(ctx, req.Email)
	if err != nil {
		logger.Errorf(ctx, "Failed to get user by email: %v", err)
		return &types.LoginResponse{
			Success: false,
			Message: "Invalid email or password",
		}, nil
	}
	if user == nil {
		logger.Warn(ctx, "User not found for email")
		return &types.LoginResponse{
			Success: false,
			Message: "Invalid email or password",
		}, nil
	}

	// Check if user is active
	if !user.IsActive {
		logger.Warn(ctx, "User account is disabled")
		return &types.LoginResponse{
			Success: false,
			Message: "Account is disabled",
		}, nil
	}

	// Verify password
	err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password))
	if err != nil {
		logger.Warn(ctx, "Password verification failed")
		return &types.LoginResponse{
			Success: false,
			Message: "Invalid email or password",
		}, nil
	}
	logger.Info(ctx, "Password verification successful")

	// Generate tokens. Resolve the target tenant once so the JWT claim
	// and the tenant we return below agree — otherwise an honoured
	// "last active tenant" preference would mint a token for tenant N
	// but tell the client they're in their home tenant.
	logger.Info(ctx, "Generating tokens")
	resolvedTenantID := s.resolveLoginTenantID(ctx, user)
	accessToken, refreshToken, err := s.generateTokensForTenant(ctx, user, resolvedTenantID)
	if err != nil {
		logger.Errorf(ctx, "Failed to generate tokens: %v", err)
		return &types.LoginResponse{
			Success: false,
			Message: "Login failed",
		}, nil
	}
	logger.Info(ctx, "Tokens generated successfully")

	// Get tenant information
	tenant, err := s.tenantService.GetTenantByID(ctx, resolvedTenantID)
	if err != nil {
		logger.Warn(ctx, "Failed to get tenant info")
	} else {
		logger.Info(ctx, "Tenant information retrieved successfully")
	}

	memberships := s.buildMembershipsForUser(ctx, user, tenant)

	logger.Info(ctx, "User logged in successfully")
	return &types.LoginResponse{
		Success:      true,
		Message:      "Login successful",
		User:         user,
		ActiveTenant: tenant,
		Memberships:  memberships,
		Token:        accessToken,
		RefreshToken: refreshToken,
	}, nil
}

// buildMembershipsForUser returns the user's tenant memberships projected
// into the login-response shape. activeTenant (if non-nil and matching one
// of the rows) is used to reuse its already-fetched name without a second
// DB lookup; other tenants are looked up individually. Errors are logged
// but never propagated — a missing memberships array degrades gracefully
// to length 0 rather than failing the whole login.
//
// When the membership service is unavailable (e.g. in tests that wire only
// part of the dependency graph), this falls back to a single synthesized
// row built from User.TenantID + the active tenant so callers always get
// at least one entry.
func (s *userService) BuildLoginMemberships(
	ctx context.Context,
	user *types.User,
	activeTenant *types.Tenant,
) []types.Membership {
	return s.buildMembershipsForUser(ctx, user, activeTenant)
}

func (s *userService) buildMembershipsForUser(
	ctx context.Context,
	user *types.User,
	activeTenant *types.Tenant,
) []types.Membership {
	if user == nil {
		return []types.Membership{}
	}
	if s.memberService == nil {
		return synthFallbackMembership(user, activeTenant)
	}
	rows, err := s.memberService.ListByUser(ctx, user.ID)
	if err != nil {
		logger.Warnf(ctx, "Failed to list memberships for user %s: %v", user.ID, err)
		return synthFallbackMembership(user, activeTenant)
	}
	if len(rows) == 0 {
		return synthFallbackMembership(user, activeTenant)
	}
	// 收集需要批量查询名称的 tenant id（跳过 activeTenant 因为它已经在手）。
	needsLookup := make([]uint64, 0, len(rows))
	for _, m := range rows {
		if m == nil || m.Status != types.TenantMemberStatusActive {
			continue
		}
		if activeTenant != nil && m.TenantID == activeTenant.ID {
			continue
		}
		needsLookup = append(needsLookup, m.TenantID)
	}
	tenantByID := map[uint64]*types.Tenant{}
	if len(needsLookup) > 0 {
		if found, terr := s.tenantService.GetTenantsByIDs(ctx, needsLookup); terr == nil {
			tenantByID = found
		} else {
			logger.Warnf(ctx, "Failed to batch-load tenants for memberships (user=%s): %v",
				user.ID, terr)
		}
	}

	out := make([]types.Membership, 0, len(rows))
	for _, m := range rows {
		if m == nil || m.Status != types.TenantMemberStatusActive {
			continue
		}
		name := ""
		if activeTenant != nil && m.TenantID == activeTenant.ID {
			name = activeTenant.Name
		} else if t, ok := tenantByID[m.TenantID]; ok && t != nil {
			name = t.Name
		}
		out = append(out, types.Membership{
			TenantID:   m.TenantID,
			TenantName: name,
			Role:       m.Role,
		})
	}
	if len(out) == 0 {
		return synthFallbackMembership(user, activeTenant)
	}
	return out
}

// synthFallbackMembership returns a single-row membership list inferred
// from User.TenantID. Used when the membership table has not been
// populated yet (e.g. during the rollout window where the migration has
// run but the auth middleware's auto-promotion hasn't fired) so the
// response shape stays consistent.
//
// The fallback role is intentionally TenantRoleViewer (least privilege):
// the login response only feeds UI rendering, and the backend re-derives
// the real role from tenant_members on every request. If membership data
// is temporarily unavailable, showing a Viewer UI is preferable to
// granting a misleading Owner UI that would surface admin controls the
// backend will then 403. Once the membership row appears (via the auth
// middleware's home-tenant auto-promotion or an admin invitation) the
// next /auth/me-style refresh will upgrade the UI to the real role.
func synthFallbackMembership(user *types.User, activeTenant *types.Tenant) []types.Membership {
	if user == nil || user.TenantID == 0 {
		// Always return a non-nil slice so the login response carries an
		// empty array rather than `null`, preserving the documented
		// "always populated" contract on LoginResponse.Memberships.
		return []types.Membership{}
	}
	name := ""
	if activeTenant != nil && activeTenant.ID == user.TenantID {
		name = activeTenant.Name
	}
	return []types.Membership{{
		TenantID:   user.TenantID,
		TenantName: name,
		Role:       types.TenantRoleViewer,
	}}
}

// GetOIDCAuthorizationURL builds the OIDC authorization URL.
func (s *userService) GetOIDCAuthorizationURL(ctx context.Context, redirectURI string) (*types.OIDCAuthURLResponse, error) {
	cfg, err := s.getOIDCConfig(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(redirectURI) == "" {
		return nil, errors.New("redirect_uri is required")
	}

	nonce, err := generateRandomString(24)
	if err != nil {
		return nil, fmt.Errorf("failed to generate state: %w", err)
	}

	state, err := encodeOIDCAuthorizationState(&oidcAuthorizationState{
		Nonce:       nonce,
		RedirectURI: strings.TrimSpace(redirectURI),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to encode OIDC state: %w", err)
	}

	query := url.Values{}
	query.Set("response_type", "code")
	query.Set("client_id", cfg.ClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", strings.Join(cfg.Scopes, " "))
	query.Set("state", state)

	authURL := cfg.AuthorizationEndpoint
	if strings.Contains(authURL, "?") {
		authURL += "&" + query.Encode()
	} else {
		authURL += "?" + query.Encode()
	}

	return &types.OIDCAuthURLResponse{
		Success:             true,
		ProviderDisplayName: cfg.ProviderDisplayName,
		AuthorizationURL:    authURL,
		State:               state,
	}, nil
}

// LoginWithOIDC exchanges code for tokens, loads user info, provisions user if needed, and returns local login tokens.
func (s *userService) LoginWithOIDC(ctx context.Context, code, redirectURI string) (*types.OIDCCallbackResponse, error) {
	if strings.TrimSpace(code) == "" {
		return nil, errors.New("code is required")
	}
	if strings.TrimSpace(redirectURI) == "" {
		return nil, errors.New("redirect_uri is required")
	}

	cfg, err := s.getOIDCConfig(ctx)
	if err != nil {
		return nil, err
	}

	tokenResp, err := s.exchangeOIDCCode(ctx, cfg, code, redirectURI)
	if err != nil {
		return nil, err
	}

	userInfo, err := s.resolveOIDCUserInfo(ctx, cfg, tokenResp)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(userInfo.Email) == "" {
		return nil, errors.New("OIDC provider did not return email")
	}

	user, err := s.userRepo.GetUserByEmail(ctx, userInfo.Email)
	if err != nil && !isUserLookupNotFound(err) {
		return nil, fmt.Errorf("failed to query user by email: %w", err)
	}
	isNewUser := false
	if isUserLookupNotFound(err) || user == nil {
		user, err = s.provisionOIDCUser(ctx, userInfo)
		if err != nil {
			return nil, err
		}
		isNewUser = true
	}

	if !user.IsActive {
		return &types.OIDCCallbackResponse{Success: false, Message: "Account is disabled"}, nil
	}

	// Resolve target tenant once so the JWT claim and the tenant we
	// return below stay in sync; see Login for the rationale.
	resolvedTenantID := s.resolveLoginTenantID(ctx, user)
	accessToken, refreshToken, err := s.generateTokensForTenant(ctx, user, resolvedTenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate local tokens: %w", err)
	}

	// 拉取 tenant + memberships，让 OIDC 登录的返回结构与本地登录一致，
	// 前端无须为 OIDC 单独走一次 /auth/me 才能拿到角色。
	var tenant *types.Tenant
	if resolvedTenantID > 0 {
		if t, terr := s.tenantService.GetTenantByID(ctx, resolvedTenantID); terr == nil {
			tenant = t
		} else {
			logger.Warnf(ctx, "OIDC login: failed to load tenant %d for user %s: %v",
				resolvedTenantID, user.ID, terr)
		}
	}
	memberships := s.buildMembershipsForUser(ctx, user, tenant)

	return &types.OIDCCallbackResponse{
		Success:      true,
		Message:      "登录成功",
		User:         user,
		Tenant:       tenant,
		Memberships:  memberships,
		Token:        accessToken,
		RefreshToken: refreshToken,
		IsNewUser:    isNewUser,
	}, nil
}

// GetUserByID gets a user by ID
func (s *userService) GetUserByID(ctx context.Context, id string) (*types.User, error) {
	return s.userRepo.GetUserByID(ctx, id)
}

// GetUsersByIDs proxies to the repository batch fetch. Returns an empty
// map for an empty input; missing ids are absent from the result.
func (s *userService) GetUsersByIDs(ctx context.Context, ids []string) (map[string]*types.User, error) {
	return s.userRepo.GetUsersByIDs(ctx, ids)
}

// GetUserByEmail gets a user by email
func (s *userService) GetUserByEmail(ctx context.Context, email string) (*types.User, error) {
	return s.userRepo.GetUserByEmail(ctx, email)
}

// GetUserByUsername gets a user by username
func (s *userService) GetUserByUsername(ctx context.Context, username string) (*types.User, error) {
	return s.userRepo.GetUserByUsername(ctx, username)
}

// GetUserByTenantID gets the first user (owner) of a tenant
func (s *userService) GetUserByTenantID(ctx context.Context, tenantID uint64) (*types.User, error) {
	return s.userRepo.GetUserByTenantID(ctx, tenantID)
}

// UpdateUser updates user information
func (s *userService) UpdateUser(ctx context.Context, user *types.User) error {
	user.UpdatedAt = time.Now()
	return s.userRepo.UpdateUser(ctx, user)
}

// UpdateUserPreferences applies a partial update over the user's
// preferences blob. PATCH semantics: only keys present in `patch`
// (non-nil pointer fields) replace the existing value; everything else
// is preserved. This lets the front-end PUT only the toggle that
// changed without having to read-modify-write the whole struct, and
// also makes the endpoint forward-compatible — older clients that
// don't know about newer keys won't accidentally erase them.
func (s *userService) UpdateUserPreferences(
	ctx context.Context,
	userID string,
	patch types.UserPreferences,
) (types.UserPreferences, error) {
	user, err := s.userRepo.GetUserByID(ctx, userID)
	if err != nil {
		return types.UserPreferences{}, err
	}

	merged := user.Preferences
	if patch.EnableMemory != nil {
		v := *patch.EnableMemory
		merged.EnableMemory = &v
	}
	if patch.LastActiveTenantID != nil {
		// *0 = "forget my preference, fall back to home on next login";
		// any positive value = set/replace. We do not validate membership
		// here — invalid values get culled on the next login via
		// resolveLoginTenantID, keeping this endpoint cheap.
		if *patch.LastActiveTenantID == 0 {
			merged.LastActiveTenantID = nil
		} else {
			v := *patch.LastActiveTenantID
			merged.LastActiveTenantID = &v
		}
	}

	user.Preferences = merged
	user.UpdatedAt = time.Now()
	if err := s.userRepo.UpdateUser(ctx, user); err != nil {
		return types.UserPreferences{}, err
	}
	return merged, nil
}

// DeleteUser deletes a user
func (s *userService) DeleteUser(ctx context.Context, id string) error {
	return s.userRepo.DeleteUser(ctx, id)
}

// ChangePassword changes user password
func (s *userService) ChangePassword(ctx context.Context, userID string, oldPassword, newPassword string) error {
	user, err := s.userRepo.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}

	// Verify old password
	err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(oldPassword))
	if err != nil {
		return errors.New("invalid old password")
	}

	// Hash new password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	user.PasswordHash = string(hashedPassword)
	user.UpdatedAt = time.Now()

	return s.userRepo.UpdateUser(ctx, user)
}

// ValidatePassword validates user password
func (s *userService) ValidatePassword(ctx context.Context, userID string, password string) error {
	user, err := s.userRepo.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}

	return bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
}

// GenerateTokens generates access and refresh tokens for user. The
// access token's tenant_id claim defaults to user.TenantID (home), but
// if the user has persisted a still-valid "last active tenant"
// preference we honour it instead — so login (and the refresh-token
// rotation path that also calls into here) lands the user back where
// they left off across devices. SwitchTenant remains the explicit tool
// for switching to an arbitrary membership.
func (s *userService) GenerateTokens(
	ctx context.Context,
	user *types.User,
) (accessToken, refreshToken string, err error) {
	return s.generateTokensForTenant(ctx, user, s.resolveLoginTenantID(ctx, user))
}

// resolveLoginTenantID picks the tenant whose ID should be encoded in a
// freshly minted access token. The contract:
//
//  1. If the user has no LastActiveTenantID preference set (or it points
//     at home), return home — the historical behaviour.
//  2. Otherwise validate the preference: the tenant must still exist and
//     the user must still have an active membership (or be a cross-tenant
//     superuser). Validation failure logs a warning, best-effort clears
//     the stale preference (so we don't waste a DB round-trip on every
//     subsequent login), and falls back to home.
//
// This is intentionally a private method on userService so it can reach
// memberService / tenantService / userRepo. Errors from the validation
// path never fail login; the worst case is the user lands in home.
func (s *userService) resolveLoginTenantID(ctx context.Context, user *types.User) uint64 {
	if user == nil {
		return 0
	}
	pref := user.Preferences.LastActiveTenantID
	if pref == nil || *pref == 0 || *pref == user.TenantID {
		return user.TenantID
	}
	preferred := *pref

	// Tenant must still exist.
	if s.tenantService != nil {
		if _, err := s.tenantService.GetTenantByID(ctx, preferred); err != nil {
			logger.Warnf(ctx,
				"resolveLoginTenantID: preferred tenant %d not loadable for user %s, "+
					"clearing preference and falling back to home: %v",
				preferred, user.ID, err)
			s.clearLastActiveTenantPreference(ctx, user)
			return user.TenantID
		}
	}

	// Membership (or cross-tenant superuser) must still be valid. Mirrors
	// the gate in SwitchTenant so the two entry points stay consistent.
	if !user.CanAccessAllTenants {
		if s.memberService == nil {
			logger.Warnf(ctx,
				"resolveLoginTenantID: member service unavailable; falling back to home for user %s",
				user.ID)
			return user.TenantID
		}
		member, err := s.memberService.GetMembership(ctx, user.ID, preferred)
		if err != nil || member == nil || member.Status != types.TenantMemberStatusActive {
			logger.Warnf(ctx,
				"resolveLoginTenantID: user %s no longer has active membership in tenant %d, "+
					"clearing preference and falling back to home (err=%v)",
				user.ID, preferred, err)
			s.clearLastActiveTenantPreference(ctx, user)
			return user.TenantID
		}
	}

	return preferred
}

// clearLastActiveTenantPreference is the best-effort cleanup half of
// resolveLoginTenantID. Failures here are logged but never propagated:
// the in-memory user already has the preference cleared for this login,
// and the next login will re-attempt the cleanup.
func (s *userService) clearLastActiveTenantPreference(ctx context.Context, user *types.User) {
	if user == nil {
		return
	}
	user.Preferences.LastActiveTenantID = nil
	if err := s.userRepo.UpdateUser(ctx, user); err != nil {
		logger.Warnf(ctx,
			"clearLastActiveTenantPreference: failed to persist cleared preference for user %s: %v",
			user.ID, err)
	}
}

// generateTokensForTenant is the shared implementation behind
// GenerateTokens and SwitchTenant. It encodes activeTenantID into the
// access token's tenant_id claim so the auth middleware scopes future
// requests there.
func (s *userService) generateTokensForTenant(
	ctx context.Context,
	user *types.User,
	activeTenantID uint64,
) (accessToken, refreshToken string, err error) {
	// Generate access token (expires in 24 hours)
	accessClaims := jwt.MapClaims{
		"user_id":   user.ID,
		"email":     user.Email,
		"tenant_id": activeTenantID,
		"exp":       time.Now().Add(24 * time.Hour).Unix(),
		"iat":       time.Now().Unix(),
		"type":      "access",
	}

	accessTokenObj := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims)
	accessToken, err = accessTokenObj.SignedString([]byte(getJwtSecret()))
	if err != nil {
		return "", "", err
	}

	// Generate refresh token (expires in 7 days)
	refreshClaims := jwt.MapClaims{
		"user_id": user.ID,
		"exp":     time.Now().Add(7 * 24 * time.Hour).Unix(),
		"iat":     time.Now().Unix(),
		"type":    "refresh",
	}

	refreshTokenObj := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims)
	refreshToken, err = refreshTokenObj.SignedString([]byte(getJwtSecret()))
	if err != nil {
		return "", "", err
	}

	// Store tokens in database
	accessTokenRecord := &types.AuthToken{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		Token:     accessToken,
		TokenType: "access_token",
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	refreshTokenRecord := &types.AuthToken{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		Token:     refreshToken,
		TokenType: "refresh_token",
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	_ = s.tokenRepo.CreateToken(ctx, accessTokenRecord)
	_ = s.tokenRepo.CreateToken(ctx, refreshTokenRecord)

	return accessToken, refreshToken, nil
}

// SwitchTenant verifies that user has an active membership in
// targetTenantID and issues a new token pair scoped to that tenant.
// The previous refresh token (if provided) is revoked so the old session
// can no longer roll forward into the source tenant.
//
// Returns ErrMembershipNotFound when the user is not a member of the
// target tenant. Cross-tenant superuser access (CanAccessAllTenants)
// is allowed without a membership row, mirroring the auth middleware's
// resolveTenantRole behaviour.
func (s *userService) SwitchTenant(
	ctx context.Context,
	user *types.User,
	targetTenantID uint64,
	currentRefreshToken string,
) (*types.LoginResponse, error) {
	if user == nil {
		return nil, errors.New("user is required")
	}
	if targetTenantID == 0 {
		return nil, errors.New("target tenant ID is required")
	}

	// Verify membership unless the caller is a cross-tenant superuser
	// switching outside their home tenant.
	if !user.CanAccessAllTenants || targetTenantID == user.TenantID {
		if s.memberService == nil {
			return nil, errors.New("tenant membership service unavailable")
		}
		member, err := s.memberService.GetMembership(ctx, user.ID, targetTenantID)
		if err != nil {
			return nil, fmt.Errorf("lookup membership: %w", err)
		}
		if member == nil || member.Status != types.TenantMemberStatusActive {
			return nil, ErrMembershipNotFound
		}
	}

	tenant, err := s.tenantService.GetTenantByID(ctx, targetTenantID)
	if err != nil {
		return nil, fmt.Errorf("load target tenant: %w", err)
	}

	accessToken, refreshToken, err := s.generateTokensForTenant(ctx, user, targetTenantID)
	if err != nil {
		return nil, fmt.Errorf("generate tokens: %w", err)
	}

	// Best-effort revoke of the previous refresh token. Failure is
	// logged but not fatal — the new tokens are already issued and the
	// old refresh token will expire naturally.
	if strings.TrimSpace(currentRefreshToken) != "" {
		if err := s.RevokeToken(ctx, currentRefreshToken); err != nil {
			logger.Warnf(ctx, "Failed to revoke previous refresh token during tenant switch: %v", err)
		}
	}

	memberships := s.buildMembershipsForUser(ctx, user, tenant)

	return &types.LoginResponse{
		Success:      true,
		Message:      "Tenant switched",
		User:         user,
		ActiveTenant: tenant,
		Memberships:  memberships,
		Token:        accessToken,
		RefreshToken: refreshToken,
	}, nil
}

// ValidateToken validates an access token. The second return value is
// the JWT's `tenant_id` claim — i.e. the tenant the token was minted
// for, which may differ from user.TenantID after a /auth/switch-tenant
// call. Tokens minted before tenant-level RBAC don't carry the claim;
// in that case we fall back to user.TenantID for backward compatibility.
func (s *userService) ValidateToken(ctx context.Context, tokenString string) (*types.User, uint64, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(getJwtSecret()), nil
	})

	if err != nil || !token.Valid {
		return nil, 0, errors.New("invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, 0, errors.New("invalid token claims")
	}

	userID, ok := claims["user_id"].(string)
	if !ok {
		return nil, 0, errors.New("invalid user ID in token")
	}

	// Check if token is revoked
	tokenRecord, err := s.tokenRepo.GetTokenByValue(ctx, tokenString)
	if err != nil || tokenRecord == nil || tokenRecord.IsRevoked {
		return nil, 0, errors.New("token is revoked")
	}

	user, err := s.userRepo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, 0, err
	}

	// Extract active tenant from the JWT. Anything missing or unparseable
	// falls back to the user's home tenant so old tokens (and tokens issued
	// by code paths that don't yet set the claim) keep working.
	activeTenantID := tenantIDFromClaims(claims, user.TenantID)

	return user, activeTenantID, nil
}

// tenantIDFromClaims pulls the active tenant ID out of a parsed JWT
// claim map. Returns fallback when the claim is missing or has an
// unrecognised type. Extracted as a free function so it can be unit
// tested without standing up the full userService dependency graph.
//
// JSON numbers come back as float64 from jwt.MapClaims; the int64 /
// uint64 branches cover legacy code paths and tests that build claims
// directly. Negative values are treated as missing.
func tenantIDFromClaims(claims jwt.MapClaims, fallback uint64) uint64 {
	raw, ok := claims["tenant_id"]
	if !ok {
		return fallback
	}
	switch v := raw.(type) {
	case float64:
		if v > 0 {
			return uint64(v)
		}
	case int64:
		if v > 0 {
			return uint64(v)
		}
	case uint64:
		if v > 0 {
			return v
		}
	}
	return fallback
}

// RefreshToken refreshes access token using refresh token
func (s *userService) RefreshToken(
	ctx context.Context,
	refreshTokenString string,
) (accessToken, newRefreshToken string, err error) {
	token, err := jwt.Parse(refreshTokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(getJwtSecret()), nil
	})

	if err != nil || !token.Valid {
		return "", "", errors.New("invalid refresh token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", "", errors.New("invalid token claims")
	}

	tokenType, ok := claims["type"].(string)
	if !ok || tokenType != "refresh" {
		return "", "", errors.New("not a refresh token")
	}

	userID, ok := claims["user_id"].(string)
	if !ok {
		return "", "", errors.New("invalid user ID in token")
	}

	// Check if token is revoked
	tokenRecord, err := s.tokenRepo.GetTokenByValue(ctx, refreshTokenString)
	if err != nil || tokenRecord == nil || tokenRecord.IsRevoked {
		return "", "", errors.New("refresh token is revoked")
	}

	// Get user
	user, err := s.userRepo.GetUserByID(ctx, userID)
	if err != nil {
		return "", "", err
	}

	// Revoke old refresh token
	tokenRecord.IsRevoked = true
	_ = s.tokenRepo.UpdateToken(ctx, tokenRecord)

	// Generate new tokens
	return s.GenerateTokens(ctx, user)
}

// RevokeToken revokes a token
func (s *userService) RevokeToken(ctx context.Context, tokenString string) error {
	tokenRecord, err := s.tokenRepo.GetTokenByValue(ctx, tokenString)
	if err != nil {
		return err
	}

	tokenRecord.IsRevoked = true
	tokenRecord.UpdatedAt = time.Now()

	return s.tokenRepo.UpdateToken(ctx, tokenRecord)
}

// GetCurrentUser gets current user from context
func (s *userService) GetCurrentUser(ctx context.Context) (*types.User, error) {
	user, ok := ctx.Value(types.UserContextKey).(*types.User)
	if !ok {
		return nil, errors.New("user not found in context")
	}

	return user, nil
}

// SearchUsers searches users by username or email
func (s *userService) SearchUsers(ctx context.Context, query string, limit int) ([]*types.User, error) {
	if query == "" {
		return []*types.User{}, nil
	}
	return s.userRepo.SearchUsers(ctx, query, limit)
}

type oidcDiscoveryDocument struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
}

type oidcTokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
}

func (s *userService) getOIDCConfig(ctx context.Context) (*config.OIDCAuthConfig, error) {
	if s.config == nil || s.config.OIDCAuth == nil || !s.config.OIDCAuth.Enable {
		return nil, errors.New("OIDC login is disabled")
	}
	cfg := *s.config.OIDCAuth
	if cfg.UserInfoMapping == nil {
		cfg.UserInfoMapping = &config.OIDCUserInfoMapping{Username: "name", Email: "email"}
	}
	if err := s.populateOIDCEndpoints(ctx, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (s *userService) populateOIDCEndpoints(ctx context.Context, cfg *config.OIDCAuthConfig) error {
	if strings.TrimSpace(cfg.AuthorizationEndpoint) != "" && strings.TrimSpace(cfg.TokenEndpoint) != "" {
		return nil
	}
	if strings.TrimSpace(cfg.DiscoveryURL) == "" {
		return errors.New("OIDC discovery_url or explicit endpoints are required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.DiscoveryURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create OIDC discovery request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to load OIDC discovery document: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("OIDC discovery request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var doc oidcDiscoveryDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("failed to decode OIDC discovery document: %w", err)
	}
	if cfg.AuthorizationEndpoint == "" {
		cfg.AuthorizationEndpoint = doc.AuthorizationEndpoint
	}
	if cfg.TokenEndpoint == "" {
		cfg.TokenEndpoint = doc.TokenEndpoint
	}
	if cfg.UserInfoEndpoint == "" {
		cfg.UserInfoEndpoint = doc.UserInfoEndpoint
	}
	if cfg.AuthorizationEndpoint == "" || cfg.TokenEndpoint == "" {
		return errors.New("OIDC discovery document missing required endpoints")
	}
	return nil
}

func (s *userService) exchangeOIDCCode(ctx context.Context, cfg *config.OIDCAuthConfig, code, redirectURI string) (*oidcTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", cfg.ClientID)
	form.Set("client_secret", cfg.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create OIDC token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange OIDC code: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("OIDC token exchange failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tokenResp oidcTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode OIDC token response: %w", err)
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" && strings.TrimSpace(tokenResp.IDToken) == "" {
		return nil, errors.New("OIDC token response missing access_token and id_token")
	}
	return &tokenResp, nil
}

func (s *userService) resolveOIDCUserInfo(ctx context.Context, cfg *config.OIDCAuthConfig, tokenResp *oidcTokenResponse) (*types.OIDCUserInfo, error) {
	claims := map[string]interface{}{}

	if strings.TrimSpace(tokenResp.IDToken) != "" {
		idTokenClaims, err := decodeJWTClaims(tokenResp.IDToken)
		if err != nil {
			logger.Warnf(ctx, "Failed to decode OIDC id_token claims: %v", err)
		} else {
			for k, v := range idTokenClaims {
				claims[k] = v
			}
		}
	}

	if strings.TrimSpace(cfg.UserInfoEndpoint) != "" && strings.TrimSpace(tokenResp.AccessToken) != "" {
		userInfoClaims, err := s.fetchOIDCUserInfo(ctx, cfg.UserInfoEndpoint, tokenResp.AccessToken)
		if err != nil {
			logger.Warnf(ctx, "Failed to fetch OIDC userinfo, fallback to id_token claims: %v", err)
		} else {
			for k, v := range userInfoClaims {
				claims[k] = v
			}
		}
	}

	info := &types.OIDCUserInfo{Claims: claims}
	if sub, _ := claims["sub"].(string); sub != "" {
		info.Subject = sub
	}
	info.Username = extractClaimAsString(claims, cfg.UserInfoMapping.Username)
	info.Email = extractClaimAsString(claims, cfg.UserInfoMapping.Email)
	if info.Username == "" {
		info.Username = extractClaimAsString(claims, "preferred_username")
	}
	if info.Username == "" {
		info.Username = extractClaimAsString(claims, "name")
	}
	if info.Username == "" && info.Email != "" {
		info.Username = strings.Split(info.Email, "@")[0]
	}
	return info, nil
}

func (s *userService) fetchOIDCUserInfo(ctx context.Context, endpoint, accessToken string) (map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("userinfo request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var claims map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func (s *userService) provisionOIDCUser(ctx context.Context, info *types.OIDCUserInfo) (*types.User, error) {
	username := s.generateOIDCUsername(ctx, info)
	randomPassword, err := generateRandomString(32)
	if err != nil {
		return nil, fmt.Errorf("failed to generate password for OIDC user: %w", err)
	}

	user, err := s.Register(ctx, &types.RegisterRequest{
		Username: username,
		Email:    info.Email,
		Password: randomPassword,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to auto-provision OIDC user: %w", err)
	}
	return user, nil
}

func (s *userService) generateOIDCUsername(ctx context.Context, info *types.OIDCUserInfo) string {
	base := sanitizeUsernameCandidate(info.Username)
	if base == "" {
		base = sanitizeUsernameCandidate(strings.Split(info.Email, "@")[0])
	}
	if base == "" {
		base = "oidc-user"
	}

	candidate := base
	for i := 0; i < 20; i++ {
		existing, err := s.userRepo.GetUserByUsername(ctx, candidate)
		if isUserLookupNotFound(err) || (err == nil && existing == nil) {
			return candidate
		}
		if err != nil && !isUserLookupNotFound(err) {
			logger.Warnf(ctx, "Failed to check existing OIDC username %q: %v", candidate, err)
		}
		candidate = fmt.Sprintf("%s-%d", base, i+1)
	}
	return fmt.Sprintf("%s-%d", base, time.Now().Unix())
}

func generateRandomString(length int) (string, error) {
	buffer := make([]byte, length)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func encodeOIDCAuthorizationState(state *oidcAuthorizationState) (string, error) {
	payload, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeJWTClaims(token string) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, errors.New("invalid JWT format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func extractClaimAsString(claims map[string]interface{}, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	value, ok := claims[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func sanitizeUsernameCandidate(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(b.String(), "-._")
	if len(result) > 50 {
		result = strings.Trim(result[:50], "-._")
	}
	return result
}

func isUserLookupNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, apprepo.ErrUserNotFound) || strings.Contains(strings.ToLower(err.Error()), "user not found")
}
