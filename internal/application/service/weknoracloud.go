package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	modelsutils "github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

type weKnoraCloudService struct {
	tenantRepo interfaces.TenantRepository
}

// NewWeKnoraCloudService 构造 WeKnoraCloudService
func NewWeKnoraCloudService(
	repo interfaces.ModelRepository,
	tenantRepo interfaces.TenantRepository,
) interfaces.WeKnoraCloudService {
	return &weKnoraCloudService{
		tenantRepo: tenantRepo,
	}
}

func IsWeKnoraCloudDocReaderAddr(addr string) bool {
	return strings.TrimSuffix(strings.TrimSpace(addr), "/") == strings.TrimRight(provider.WeKnoraCloudBaseURL, "/")+"/api/v1/doc/reader"
}

// SaveCredentials 仅保存 APPID/APPSECRET 凭证，不自动创建模型
func (s *weKnoraCloudService) SaveCredentials(ctx context.Context, appID, appSecret string) error {
	if appID == "" {
		return fmt.Errorf("app_id is required")
	}
	if appSecret == "" {
		return fmt.Errorf("app_secret is required")
	}

	if err := s.verifyCredentials(ctx, appID, appSecret); err != nil {
		return fmt.Errorf("credential verification failed: %w", err)
	}

	encryptedSecret := appSecret
	if key := utils.GetAESKey(); key != nil {
		if encrypted, err := utils.EncryptAESGCM(appSecret, key); err == nil {
			encryptedSecret = encrypted
		}
	}

	tenantID := types.MustTenantIDFromContext(ctx)
	return s.updateTenantCredentials(ctx, tenantID, appID, encryptedSecret)
}

// verifyCredentials 向 WeKnoraCloud /api/v1/health 发送带签名头的 GET。
//
// 注意：health 一般为探活接口，远端常不校验 APPID/SECRET 或签名；HTTP 200 通常只表示
// 「网关/服务可达」，不能严格证明凭证有效。若需强校验，应改为调用必须鉴权的业务接口。
func (s *weKnoraCloudService) verifyCredentials(ctx context.Context, appID, appSecret string) error {
	baseURL := strings.TrimRight(provider.WeKnoraCloudBaseURL, "/")
	healthURL := baseURL + "/api/v1/health"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return fmt.Errorf("create verification request failed: %w", err)
	}

	requestID := fmt.Sprintf("verify-%d", time.Now().UnixNano())
	signHeaders := modelsutils.Sign(appID, appSecret, requestID, "{}")
	for k, v := range signHeaders {
		req.Header.Set(k, v)
	}

	logger.Infof(ctx, "credential verification request: method=GET url=%s app_id=%s request_id=%s ",
		healthURL, appID, requestID)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		logger.Warnf(ctx, "credential verification HTTP failed: url=%s err=%v", healthURL, err)
		return fmt.Errorf("service unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("invalid APPID or APPSECRET (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("invalid response status code: %d", resp.StatusCode)
	}
	return nil
}

// CheckStatus 检查 WeKnoraCloud 凭证是否可正常解密
func (s *weKnoraCloudService) CheckStatus(ctx context.Context) (*types.WeKnoraCloudStatusResult, error) {
	tenantID := types.MustTenantIDFromContext(ctx)

	tenant, err := s.tenantRepo.GetTenantByID(ctx, tenantID)
	if err != nil || tenant == nil {
		return &types.WeKnoraCloudStatusResult{HasModels: false, NeedsReinit: false}, nil
	}

	// Check if tenant has WeKnoraCloud credentials in parser config
	if tenant.ParserEngineConfig == nil || tenant.ParserEngineConfig.DocreaderAppID == "" || tenant.ParserEngineConfig.DocreaderAPIKey == "" {
		return &types.WeKnoraCloudStatusResult{
			HasModels:   false,
			NeedsReinit: false,
		}, nil
	}

	// Try to decrypt the API key
	if key := utils.GetAESKey(); key != nil {
		if _, err := utils.DecryptAESGCM(tenant.ParserEngineConfig.DocreaderAPIKey, key); err != nil {
			return &types.WeKnoraCloudStatusResult{
				HasModels:   true,
				NeedsReinit: true,
				Reason:      "WeKnoraCloud 凭证解密失败（服务重启后加密密钥已变更），请重新填写 APPID 和 APPSECRET",
			}, nil
		}
	}

	return &types.WeKnoraCloudStatusResult{HasModels: true, NeedsReinit: false}, nil
}

// updateTenantCredentials 更新租户的 WeKnoraCloud 凭证和 DocReader 地址
func (s *weKnoraCloudService) updateTenantCredentials(ctx context.Context, tenantID uint64, appID, encryptedSecret string) error {
	if s.tenantRepo == nil {
		return fmt.Errorf("tenant repository is required")
	}

	tenant, err := s.tenantRepo.GetTenantByID(ctx, tenantID)
	if err != nil {
		return err
	}
	if tenant.ParserEngineConfig == nil {
		tenant.ParserEngineConfig = &types.ParserEngineConfig{}
	}
	tenant.ParserEngineConfig.DocreaderAppID = appID
	tenant.ParserEngineConfig.DocreaderAPIKey = encryptedSecret
	return s.tenantRepo.UpdateTenant(ctx, tenant)
}
