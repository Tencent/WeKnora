package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Errors returned by WeChat authentication without provider credentials.
var (
	ErrWeChatDisabled     = errors.New("WeChat login is disabled")
	ErrWeChatAttempt      = errors.New("invalid or expired WeChat login attempt")
	ErrWeChatCode         = errors.New("WeChat authorization failed")
	ErrWeChatUnavailable  = errors.New("WeChat is temporarily unavailable")
	ErrWeChatRegistration = errors.New("new WeChat accounts require self-service registration")
	ErrWeChatAccount      = errors.New("WeChat account is unavailable or conflicts with another account")
	errWeChatIdentityRace = errors.New("concurrent WeChat identity registration")
	weChatVerifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
)

// WeChatAttemptTTL bounds a pending authorization attempt.
const WeChatAttemptTTL = 5 * time.Minute

// WeChatAuthService shares the existing account, membership and token services.
// A transaction encompasses provisioning, identity links and token issuance.
type WeChatAuthService struct {
	db         *gorm.DB
	cfg        *config.Config
	settings   interfaces.SystemSettingService
	httpClient *http.Client
}

// NewWeChatAuthService creates a credential-safe WeChat authentication service.
func NewWeChatAuthService(
	db *gorm.DB,
	cfg *config.Config,
	settings interfaces.SystemSettingService,
) *WeChatAuthService {
	c := secutils.NewSSRFSafeHTTPClient(secutils.SSRFSafeHTTPClientConfig{Timeout: 15 * time.Second})
	// Secrets occur in WeChat's query parameters. Never follow a redirect
	// carrying those parameters to a different endpoint.
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &WeChatAuthService{db: db, cfg: cfg, settings: settings, httpClient: c}
}

func weChatApp(cfg *config.WeChatAuthConfig, client string) (*config.WeChatAuthAppConfig, error) {
	var app *config.WeChatAuthAppConfig
	switch client {
	case "web":
		app = &cfg.Web
	case "mobile":
		app = &cfg.Mobile
	default:
		return nil, ErrWeChatDisabled
	}
	if !app.Enabled || app.AppID == "" || app.AppSecret == "" {
		return nil, ErrWeChatDisabled
	}
	return app, nil
}

func (s *WeChatAuthService) app(ctx context.Context, client string) (*config.WeChatAuthAppConfig, error) {
	cfg, err := s.resolveWeChatSettings(ctx)
	if err != nil {
		return nil, err
	}
	return weChatApp(cfg, client)
}

// Includes the namespace and secrets without ever exposing them to a client.
func weChatConfigHash(cfg *config.WeChatAuthConfig) string {
	b, _ := json.Marshal([]any{cfg, cfg.Web.AppSecret, cfg.Mobile.AppSecret})
	return weChatStateHash(string(b))
}

// Enabled reports which login entry points have usable configuration.
func (s *WeChatAuthService) Enabled(ctx context.Context) (bool, bool) {
	cfg, err := s.resolveWeChatSettings(ctx)
	if err != nil {
		return false, false
	}
	_, webErr := weChatApp(cfg, "web")
	_, mobileErr := weChatApp(cfg, "mobile")
	return webErr == nil, mobileErr == nil
}

// WeChatRandomVerifier generates a cryptographically random browser or client verifier.
func WeChatRandomVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// WeChatChallenge computes the URL-safe SHA-256 verifier challenge.
func WeChatChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func weChatStateHash(state string) string {
	h := sha256.Sum256([]byte(state))
	return hex.EncodeToString(h[:])
}

// CreateAttempt binds authorization to a verifier and configuration snapshot.
func (s *WeChatAuthService) CreateAttempt(
	ctx context.Context,
	client, challenge string,
) (*types.WeChatAttemptResponse, error) {
	cfg, err := s.resolveWeChatSettings(ctx)
	if err != nil {
		return nil, err
	}
	app, err := weChatApp(cfg, client)
	if err != nil {
		return nil, err
	}
	b, err := base64.RawURLEncoding.DecodeString(challenge)
	if err != nil || len(b) != 32 || base64.RawURLEncoding.EncodeToString(b) != challenge {
		return nil, ErrWeChatAttempt
	}
	state, err := WeChatRandomVerifier()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if err := s.purgeExpiredAttempts(ctx, now); err != nil {
		return nil, err
	}
	a := types.WeChatLoginAttempt{
		ConfigHash: weChatConfigHash(cfg),
		ID:         uuid.NewString(),
		Client:     client,
		AppID:      app.AppID,
		StateHash:  weChatStateHash(state),
		Challenge:  challenge,
		ExpiresAt:  now.Add(WeChatAttemptTTL),
	}
	if err := s.db.WithContext(ctx).Create(&a).Error; err != nil {
		return nil, err
	}
	scope := "snsapi_userinfo"
	if client == "web" {
		scope = "snsapi_login"
	}
	return &types.WeChatAttemptResponse{
		CallbackURL: app.CallbackURL,
		AttemptID:   a.ID,
		State:       state,
		AppID:       app.AppID,
		Scope:       scope,
		ExpiresAt:   a.ExpiresAt,
	}, nil
}

// purgeExpiredAttempts is request-driven so it also works in Lite without a
// background worker. Completion tickets hold session tokens, so abandoned
// ones must not outlive their expiry until the next login attempt.
func (s *WeChatAuthService) purgeExpiredAttempts(ctx context.Context, now time.Time) error {
	return s.db.WithContext(ctx).Where("expires_at <= ?", now).Delete(&types.WeChatLoginAttempt{}).Error
}

// WebAuthorizationURL builds the official website login URL for a server-issued attempt.
func (s *WeChatAuthService) WebAuthorizationURL(a *types.WeChatAttemptResponse) (string, error) {
	if a == nil || a.CallbackURL == "" || a.AppID == "" {
		return "", ErrWeChatAttempt
	}
	q := url.Values{
		"appid":         {a.AppID},
		"redirect_uri":  {a.CallbackURL},
		"response_type": {"code"},
		"scope":         {"snsapi_login"},
		"state":         {a.State},
	}
	return "https://open.weixin.qq.com/connect/qrconnect?" + q.Encode() + "#wechat_redirect", nil
}

// WeChatWebResult is deliberately minimal; the SPA reads /auth/me after
// installing these credentials. No tokens are carried in a redirect URL.
type WeChatWebResult struct {
	Success      bool   `json:"success"`
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
}

// StoreWebResult creates a short-lived browser-bound completion ticket.
func (s *WeChatAuthService) StoreWebResult(
	ctx context.Context,
	response *types.LoginResponse,
	verifier string,
) (string, error) {
	if !weChatVerifierPattern.MatchString(verifier) {
		return "", ErrWeChatAttempt
	}
	app, err := s.app(ctx, "web")
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(WeChatWebResult{Success: true, Token: response.Token, RefreshToken: response.RefreshToken})
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if err := s.purgeExpiredAttempts(ctx, now); err != nil {
		return "", err
	}
	ticket := types.WeChatLoginAttempt{
		ID: uuid.NewString(), Client: "web_result", AppID: app.AppID,
		Challenge: WeChatChallenge(verifier), ExpiresAt: now.Add(time.Minute), Result: string(b),
	}
	if err := s.db.WithContext(ctx).Create(&ticket).Error; err != nil {
		return "", err
	}
	return ticket.ID, nil
}

// CompleteWebResult consumes a ticket and validates its issued session.
func (s *WeChatAuthService) CompleteWebResult(ctx context.Context, ticket, verifier string) (*WeChatWebResult, error) {
	app, err := s.app(ctx, "web")
	if err != nil {
		return nil, err
	}
	if !weChatVerifierPattern.MatchString(verifier) {
		return nil, ErrWeChatAttempt
	}
	var stored types.WeChatLoginAttempt
	result := s.db.WithContext(ctx).Clauses(clause.Returning{}).
		Where("id = ? AND client = ? AND app_id = ? AND challenge = ? AND expires_at > ?",
			ticket, "web_result", app.AppID, WeChatChallenge(verifier), time.Now().UTC()).
		Delete(&stored)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrWeChatAttempt
	}
	var response WeChatWebResult
	if err := json.Unmarshal([]byte(stored.Result), &response); err != nil {
		return nil, err
	}
	us := &userService{userRepo: apprepo.NewUserRepository(s.db), tokenRepo: apprepo.NewAuthTokenRepository(s.db)}
	if _, _, err := us.ValidateToken(ctx, response.Token); err != nil {
		return nil, ErrWeChatAccount
	}
	return &response, nil
}

type weChatSubject struct {
	OpenID      string `json:"openid"`
	UnionID     string `json:"unionid"`
	AccessToken string `json:"access_token"`
	ErrCode     int    `json:"errcode"`
}

func (s *WeChatAuthService) exchangeCode(
	ctx context.Context,
	app *config.WeChatAuthAppConfig,
	code string,
) (*weChatSubject, error) {
	q := url.Values{
		"appid":      {app.AppID},
		"secret":     {app.AppSecret},
		"code":       {code},
		"grant_type": {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"https://api.weixin.qq.com/sns/oauth2/access_token?"+q.Encode(),
		nil,
	)
	if err != nil {
		return nil, ErrWeChatUnavailable
	}
	resp, err := s.httpClient.Do(req)
	// Do not return/log transport errors: url.Error contains AppSecret/code.
	if err != nil {
		return nil, ErrWeChatUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, ErrWeChatUnavailable
	}
	var subject weChatSubject
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&subject); err != nil {
		return nil, ErrWeChatUnavailable
	}
	if subject.ErrCode != 0 || subject.OpenID == "" || len(subject.OpenID) > 128 || len(subject.UnionID) > 128 ||
		subject.AccessToken == "" {
		return nil, ErrWeChatCode
	}
	// Provider credentials are neither persisted nor returned to the caller.
	subject.AccessToken = ""
	return &subject, nil
}

// Exchange consumes an attempt, verifies the provider code, and signs in the user.
func (s *WeChatAuthService) Exchange(
	ctx context.Context,
	client string,
	req *types.WeChatExchangeRequest,
	allowRegistration bool,
	provisioning types.TenantProvisioningMode,
) (*types.LoginResponse, bool, error) {
	cfg, err := s.resolveWeChatSettings(ctx)
	if err != nil {
		return nil, false, err
	}
	app, err := weChatApp(cfg, client)
	if err != nil {
		return nil, false, err
	}
	if req == nil || req.Code == "" || len(req.Code) > 512 || req.State == "" || len(req.State) > 128 ||
		!weChatVerifierPattern.MatchString(req.CodeVerifier) {
		return nil, false, ErrWeChatAttempt
	}
	// Atomic consume, committed BEFORE contacting WeChat. A replay cannot
	// exchange another code or retry a partially completed authorization.
	result := s.db.WithContext(ctx).
		Where("id = ? AND client = ? AND app_id = ? AND state_hash = ? "+
			"AND challenge = ? AND config_hash = ? AND expires_at > ?",
			req.AttemptID, client, app.AppID, weChatStateHash(req.State),
			WeChatChallenge(req.CodeVerifier), weChatConfigHash(cfg), time.Now().UTC()).
		Delete(&types.WeChatLoginAttempt{})
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, false, ErrWeChatAttempt
	}
	subject, err := s.exchangeCode(ctx, app, req.Code)
	if err != nil {
		return nil, false, err
	}
	// Retry only a unique-identity race, after its entire transaction rolls
	// back. No second provider call and no abandoned personal workspaces.
	for i := 0; i < 2; i++ {
		var response *types.LoginResponse
		var isNew bool
		err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := lockWeChatSettings(tx, false); err != nil {
				return ErrWeChatUnavailable
			}
			scoped := *s
			scoped.db = tx
			current, err := scoped.resolveWeChatSettings(ctx)
			if err != nil {
				return err
			}
			if weChatConfigHash(current) != weChatConfigHash(cfg) {
				return ErrWeChatAttempt
			}
			var txErr error
			response, isNew, txErr = s.login(
				ctx,
				tx,
				app.AppID,
				cfg.PlatformID,
				subject,
				allowRegistration,
				provisioning,
			)
			return txErr
		})
		if !errors.Is(err, errWeChatIdentityRace) {
			return response, isNew, err
		}
	}
	return nil, false, ErrWeChatAccount
}

func (s *WeChatAuthService) login(
	ctx context.Context,
	tx *gorm.DB,
	appID, platformID string,
	subject *weChatSubject,
	allowRegistration bool,
	provisioning types.TenantProvisioningMode,
) (*types.LoginResponse, bool, error) {
	keys := []types.ExternalIdentity{{Provider: "wechat_openid", Namespace: appID, Subject: subject.OpenID}}
	if subject.UnionID != "" && platformID != "" {
		keys = append(
			keys,
			types.ExternalIdentity{Provider: "wechat_unionid", Namespace: platformID, Subject: subject.UnionID},
		)
	}
	userID := ""
	for _, key := range keys {
		var found types.ExternalIdentity
		err := tx.Where("provider = ? AND namespace = ? AND subject = ?", key.Provider, key.Namespace, key.Subject).
			First(&found).
			Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		if userID != "" && userID != found.UserID {
			return nil, false, ErrWeChatAccount
		}
		userID = found.UserID
	}
	users := apprepo.NewUserRepository(tx)
	tokens := apprepo.NewAuthTokenRepository(tx)
	tenants := NewTenantService(apprepo.NewTenantRepository(tx), apprepo.NewStorageBackendRepository(tx))
	members := NewTenantMemberService(apprepo.NewTenantMemberRepository(tx), nil, users, tokens)
	us := &userService{
		userRepo:         users,
		tokenRepo:        tokens,
		tenantService:    tenants,
		memberService:    members,
		config:           s.cfg,
		systemSettingSvc: s.settings,
	}
	var user *types.User
	var err error
	isNew := userID == ""
	if isNew {
		if !allowRegistration {
			return nil, false, ErrWeChatRegistration
		}
		random, err := WeChatRandomVerifier()
		if err != nil {
			return nil, false, err
		}
		id := uuid.NewString()
		user, err = us.Register(
			ctx,
			&types.RegisterRequest{
				AuthSource:         "wechat",
				Username:           "wx_" + id,
				Email:              id + "@wechat.invalid",
				Password:           random,
				TenantProvisioning: provisioning,
			},
		)
		if err != nil {
			return nil, false, err
		}
		// Legacy UI flag also represents external-only accounts. The
		// generated password is discarded and never sent to the client.
		externalOnly := true
		user.Preferences.OidcOnlyLogin = &externalOnly
		if err := users.UpdateUser(ctx, user); err != nil {
			return nil, false, err
		}
	} else {
		user, err = users.GetUserByID(ctx, userID)
		if errors.Is(err, apprepo.ErrUserNotFound) {
			return nil, false, ErrWeChatAccount
		}
		if err != nil {
			return nil, false, err
		}
	}
	if !user.IsActive {
		return nil, false, ErrWeChatAccount
	}
	for _, key := range keys {
		key.UserID = user.ID
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&key).Error; err != nil {
			return nil, false, err
		}
		var stored types.ExternalIdentity
		if err := tx.Where("provider = ? AND namespace = ? AND subject = ?", key.Provider, key.Namespace, key.Subject).
			First(&stored).Error; err != nil {
			return nil, false, err
		}
		if stored.UserID != user.ID {
			return nil, false, errWeChatIdentityRace
		}
	}
	tenantID := us.resolveLoginTenantID(ctx, user)
	var tenant *types.Tenant
	if tenantID > 0 {
		tenant, err = tenants.GetTenantByID(ctx, tenantID)
		if err != nil {
			return nil, false, err
		}
	}
	access, refresh, err := us.generateTokensForTenant(ctx, user, tenantID)
	if err != nil {
		return nil, false, err
	}
	return &types.LoginResponse{
		Success:      true,
		User:         user,
		ActiveTenant: tenant,
		Memberships:  us.BuildLoginMemberships(ctx, user, tenant),
		Token:        access,
		RefreshToken: refresh,
	}, isNew, nil
}
