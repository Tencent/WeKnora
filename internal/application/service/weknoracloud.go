package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/infrastructure/crypto"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

type weKnoraCloudService struct {
	repo       interfaces.ModelRepository
	tenantRepo interfaces.TenantRepository
	cryptoSvc  *crypto.CryptoService
}

// NewWeKnoraCloudService 构造 WeKnoraCloudService
func NewWeKnoraCloudService(
	repo interfaces.ModelRepository,
	tenantRepo interfaces.TenantRepository,
	cryptoSvc *crypto.CryptoService,
) interfaces.WeKnoraCloudService {
	return &weKnoraCloudService{
		repo:       repo,
		tenantRepo: tenantRepo,
		cryptoSvc:  cryptoSvc,
	}
}

func IsWeKnoraCloudDocReaderAddr(addr string) bool {
	return strings.TrimSuffix(strings.TrimSpace(addr), "/") == strings.TrimRight(provider.WeKnoraCloudBaseURL, "/")+"/api/v1/doc/reader"
}

// weKnoraCloudModelDefs 三个内置模型的静态定义
var weKnoraCloudModelDefs = []struct {
	name            string
	modelType       types.ModelType
	remoteModelName string
}{
	{name: "weknoracloud-chat", modelType: types.ModelTypeKnowledgeQA, remoteModelName: "chat"},
	{name: "weknoracloud-embedding", modelType: types.ModelTypeEmbedding, remoteModelName: "embedding"},
	{name: "weknoracloud-rerank", modelType: types.ModelTypeRerank, remoteModelName: "rerank"},
}

func (s *weKnoraCloudService) Initialize(ctx context.Context, appID, appSecret string) (*types.InitializeResult, error) {
	if err := s.validateInitializeInput(appID, appSecret); err != nil {
		return nil, err
	}

	prepared, err := s.prepareInitialize(ctx, appID, appSecret)
	if err != nil {
		return nil, err
	}

	result := &types.InitializeResult{}

	actions, err := s.installBuiltinModels(ctx, prepared.tenantID, appID, prepared.encryptedSecret)
	if err != nil {
		return nil, s.failInitializeAndRollback(ctx, prepared.tenantID, prepared.snapshot, err)
	}
	result.Models = actions

	if err := s.updateTenantDocReaderAddr(ctx, prepared.tenantID, appID, prepared.encryptedSecret); err != nil {
		return nil, s.failInitializeAndRollback(ctx, prepared.tenantID, prepared.snapshot, err)
	}

	return result, nil
}

// validateInitializeInput validates that appID and appSecret are not empty.
func (s *weKnoraCloudService) validateInitializeInput(appID, appSecret string) error {
	if appID == "" {
		return fmt.Errorf("app_id is required")
	}
	if appSecret == "" {
		return fmt.Errorf("app_secret is required")
	}
	return nil
}

// weKnoraCloudInitPrepared holds the prepared state for initialize operation.
type weKnoraCloudInitPrepared struct {
	tenantID        uint64
	encryptedSecret string
	snapshot        *weKnoraCloudInitSnapshot
}

// prepareInitialize performs pre-initialization checks: ping WeKnoraCloud, encrypt secret, and snapshot current state.
func (s *weKnoraCloudService) prepareInitialize(ctx context.Context, appID, appSecret string) (*weKnoraCloudInitPrepared, error) {
	// Step 1: Check WeKnoraCloud service reachability
	if err := s.pingWeKnoraCloud(ctx, appID, appSecret); err != nil {
		return nil, fmt.Errorf("WeKnoraCloud service reachability check failed: %w", err)
	}

	// Step 2: Encrypt appSecret
	encryptedSecret, err := s.cryptoSvc.EncryptString(appSecret)
	if err != nil {
		return nil, fmt.Errorf("encrypt AppSecret failed: %w", err)
	}

	// Step 3: Get tenantID and take snapshot
	tenantID := types.MustTenantIDFromContext(ctx)
	snapshot, err := s.snapshotInitializeState(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("snapshot initialize state failed: %w", err)
	}

	return &weKnoraCloudInitPrepared{
		tenantID:        tenantID,
		encryptedSecret: encryptedSecret,
		snapshot:        snapshot,
	}, nil
}

// installBuiltinModels installs or updates the three builtin WeKnoraCloud models (chat, embedding, rerank).
// Returns a slice of ModelAction describing what was done.
func (s *weKnoraCloudService) installBuiltinModels(
	ctx context.Context,
	tenantID uint64,
	appID, encryptedSecret string,
) ([]types.ModelAction, error) {
	result := make([]types.ModelAction, 0, len(weKnoraCloudModelDefs))

	for _, def := range weKnoraCloudModelDefs {
		action, err := s.upsertModel(ctx, tenantID, def.name, def.modelType, def.remoteModelName, appID, encryptedSecret)
		if err != nil {
			return nil, fmt.Errorf("upsert model %s failed: %w", def.name, err)
		}
		result = append(result, types.ModelAction{
			Name:   def.name,
			Type:   string(def.modelType),
			Action: action,
		})
	}

	return result, nil
}

// weKnoraCloudInitSnapshot holds a snapshot of the state before initialization.
type weKnoraCloudInitSnapshot struct {
	modelsByType map[types.ModelType][]*types.Model
	tenant       *types.Tenant
}

func (s *weKnoraCloudService) snapshotInitializeState(ctx context.Context, tenantID uint64) (*weKnoraCloudInitSnapshot, error) {
	snapshot := &weKnoraCloudInitSnapshot{modelsByType: make(map[types.ModelType][]*types.Model, len(weKnoraCloudModelDefs))}
	for _, def := range weKnoraCloudModelDefs {
		models, err := s.repo.List(ctx, tenantID, def.modelType, types.ModelSourceRemote)
		if err != nil {
			return nil, err
		}
		copies := make([]*types.Model, 0, len(models))
		for _, model := range models {
			copies = append(copies, cloneWeKnoraCloudModel(model))
		}
		snapshot.modelsByType[def.modelType] = copies
	}
	if s.tenantRepo != nil {
		tenant, err := s.tenantRepo.GetTenantByID(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		snapshot.tenant = cloneWeKnoraCloudTenant(tenant)
	}
	return snapshot, nil
}

func (s *weKnoraCloudService) failInitializeAndRollback(ctx context.Context, tenantID uint64, snapshot *weKnoraCloudInitSnapshot, err error) error {
	if rollbackErr := s.rollbackInitializeState(ctx, tenantID, snapshot); rollbackErr != nil {
		return fmt.Errorf("%w；回滚失败：%v", err, rollbackErr)
	}
	return err
}

func (s *weKnoraCloudService) rollbackInitializeState(ctx context.Context, tenantID uint64, snapshot *weKnoraCloudInitSnapshot) error {
	if snapshot == nil {
		return nil
	}
	var rollbackErrs []string
	for _, def := range weKnoraCloudModelDefs {
		currentModels, err := s.repo.List(ctx, tenantID, def.modelType, types.ModelSourceRemote)
		if err != nil {
			rollbackErrs = append(rollbackErrs, err.Error())
			continue
		}
		snapshotByID := map[string]*types.Model{}
		for _, model := range snapshot.modelsByType[def.modelType] {
			snapshotByID[model.ID] = cloneWeKnoraCloudModel(model)
		}
		for _, current := range currentModels {
			if current == nil {
				continue
			}
			if _, ok := snapshotByID[current.ID]; ok {
				continue
			}
			if current.Parameters.Provider == string(provider.ProviderWeKnoraCloud) && isWeKnoraCloudBuiltinModelName(current.Name) {
				if err := s.repo.Delete(ctx, tenantID, current.ID); err != nil {
					rollbackErrs = append(rollbackErrs, err.Error())
				}
			}
		}
		for _, model := range snapshot.modelsByType[def.modelType] {
			if err := s.repo.Update(ctx, cloneWeKnoraCloudModel(model)); err != nil {
				rollbackErrs = append(rollbackErrs, err.Error())
			}
		}
	}
	if snapshot.tenant != nil && s.tenantRepo != nil {
		if err := s.tenantRepo.UpdateTenant(ctx, cloneWeKnoraCloudTenant(snapshot.tenant)); err != nil {
			rollbackErrs = append(rollbackErrs, err.Error())
		}
	}
	if len(rollbackErrs) > 0 {
		return fmt.Errorf("%s", strings.Join(rollbackErrs, "; "))
	}
	return nil
}

func isWeKnoraCloudBuiltinModelName(name string) bool {
	for _, def := range weKnoraCloudModelDefs {
		if def.name == name {
			return true
		}
	}
	return false
}

func cloneWeKnoraCloudModel(model *types.Model) *types.Model {
	if model == nil {
		return nil
	}
	clone := *model
	if model.Parameters.ExtraConfig != nil {
		clone.Parameters.ExtraConfig = make(map[string]string, len(model.Parameters.ExtraConfig))
		for k, v := range model.Parameters.ExtraConfig {
			clone.Parameters.ExtraConfig[k] = v
		}
	}
	return &clone
}

func cloneWeKnoraCloudTenant(tenant *types.Tenant) *types.Tenant {
	if tenant == nil {
		return nil
	}
	clone := *tenant
	if tenant.ParserEngineConfig != nil {
		cfg := *tenant.ParserEngineConfig
		clone.ParserEngineConfig = &cfg
	}
	return &clone
}

// pingWeKnoraCloud 调用 WeKnoraCloud 服务的 GET /health 接口验证可达性。
// 注意：这里仅检查服务可达，不校验 APPID / API Key 是否有效。
func (s *weKnoraCloudService) pingWeKnoraCloud(ctx context.Context, appID, appSecret string) error {
	baseURL := strings.TrimRight(provider.WeKnoraCloudBaseURL, "/")
	healthURL := baseURL + "/api/v1/health"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return fmt.Errorf("创建健康检查请求失败: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("WeKnoraCloud 服务不可达 (%s): %w", healthURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("WeKnoraCloud 健康检查返回非 200 状态码: %d", resp.StatusCode)
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
			HasModels:   true,
			NeedsReinit: true,
			Reason:      "WeKnoraCloud 凭证为空，请重新填写 APPID 和 API Key",
		}, nil
	}

	// Try to decrypt the API key
	_, decErr := s.cryptoSvc.DecryptString(tenant.ParserEngineConfig.DocreaderAPIKey)
	if decErr != nil {
		return &types.WeKnoraCloudStatusResult{
			HasModels:   true,
			NeedsReinit: true,
			Reason:      "WeKnoraCloud 凭证解密失败（服务重启后加密密钥已变更），请重新填写 APPID 和 API Key",
		}, nil
	}

	return &types.WeKnoraCloudStatusResult{HasModels: true, NeedsReinit: false}, nil
}

// updateTenantDocReaderAddr updates tenant parser config with WeKnoraCloud credentials and DocReaderAddr
func (s *weKnoraCloudService) updateTenantDocReaderAddr(ctx context.Context, tenantID uint64, appID, encryptedSecret string) error {
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
	tenant.ParserEngineConfig.DocReaderAddr = strings.TrimRight(provider.WeKnoraCloudBaseURL, "/") + "/api/v1/doc/reader"
	tenant.ParserEngineConfig.DocreaderAppID = appID
	tenant.ParserEngineConfig.DocreaderAPIKey = encryptedSecret
	return s.tenantRepo.UpdateTenant(ctx, tenant)
}

func (s *weKnoraCloudService) upsertModel(
	ctx context.Context,
	tenantID uint64,
	name string,
	modelType types.ModelType,
	remoteModelName string,
	appID, encryptedSecret string,
) (string, error) {
	// 列出该类型的远程模型，过滤出 weknoracloud provider
	models, err := s.repo.List(ctx, tenantID, modelType, types.ModelSourceRemote)
	if err != nil {
		return "", err
	}

	var existing *types.Model
	for _, m := range models {
		if m.Name == name && m.Parameters.Provider == string(provider.ProviderWeKnoraCloud) {
			existing = m
			break
		}
	}

	// 先清除同类型其他模型的默认标记
	excludeID := ""
	if existing != nil {
		excludeID = existing.ID
	}
	if err := s.repo.ClearDefaultByType(ctx, uint(tenantID), modelType, excludeID); err != nil {
		return "", err
	}

	if existing != nil {
		// 更新凭证和默认标记
		existing.Parameters.BaseURL = provider.WeKnoraCloudBaseURL
		existing.Parameters.AppID = appID
		existing.Parameters.AppSecret = encryptedSecret
		if existing.Parameters.ExtraConfig == nil {
			existing.Parameters.ExtraConfig = map[string]string{}
		}
		existing.Parameters.ExtraConfig["remote_model_name"] = remoteModelName
		existing.IsDefault = true
		if err := s.repo.Update(ctx, existing); err != nil {
			return "", err
		}
		return "updated", nil
	}

	// 创建新模型
	newModel := &types.Model{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		Name:      name,
		Type:      modelType,
		Source:    types.ModelSourceRemote,
		IsBuiltin: true,
		IsDefault: true,
		Status:    types.ModelStatusActive,
		Parameters: types.ModelParameters{
			Provider:  string(provider.ProviderWeKnoraCloud),
			BaseURL:   provider.WeKnoraCloudBaseURL,
			AppID:     appID,
			AppSecret: encryptedSecret,
			ExtraConfig: map[string]string{
				"remote_model_name": remoteModelName,
			},
		},
	}
	if err := s.repo.Create(ctx, newModel); err != nil {
		return "", err
	}
	return "created", nil
}
