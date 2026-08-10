// Package service: sandbox backend config management.
//
// The load-bearing rule: some fields decide whether a config can still OPERATE
// the sandboxes it already created. Overwriting them in place while sandboxes
// are alive breaks those sandboxes in one of two ways, and both are bad enough
// to refuse the save.
//
// Losing the control plane (provider, API URL, API key) means the new
// credentials have no authority over the old sandboxes, so they can no longer be
// listed or deleted. Session sandboxes are created with onTimeout=pause, so the
// provider TTL never reclaims them either - the leak is permanent and it bills.
// A paused sandbox is also what a session expects to RESUME, so conversations
// break on top.
//
// Losing the data plane (E2B sandbox domain; Cube proxy URL and sandbox domain)
// keeps cleanup possible, but every envd request now goes to the wrong host.
// Every live session on this config fails at once - skill execution, attachment
// staging, artifact collection - while the sandboxes stay alive and keep billing
// until their sessions are deleted.
//
// Hence both groups are refused while sandboxes exist. The admin ends the owning
// sessions or creates a second config and re-points agents; both keep the old
// values intact, which is what keeps cleanup possible.
package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

const sandboxConfigCleanupTimeout = 20 * time.Second

// SandboxIdentityChanged reports whether sandboxes already created under oldCfg
// would stop being operable under newCfg. Names, templates, TTLs, HTTP timeouts
// and env vars deliberately do not count: they only shape FUTURE sandboxes.
//
// newCfg must already have been merged with the stored config (see
// types.MergeSandboxConfigForUpdate). The API masks secrets on read, so
// comparing a raw payload would report a key rotation on every single save and
// leave the config permanently uneditable.
//
// The comparison needs no deployment baseline: named configs inherit nothing, so
// the stored fields are the whole identity. It is also deliberately free of
// validation — see sandbox.IdentityOf for why an unreachable old endpoint must
// still be judgeable.
func SandboxIdentityChanged(oldCfg, newCfg *types.TenantSandboxConfig) bool {
	if oldCfg == nil {
		// Nothing exists yet, so nothing can be stranded.
		return false
	}
	if newCfg == nil {
		return true
	}
	return sandbox.IdentityOf(oldCfg) != sandbox.IdentityOf(newCfg)
}

// ErrSandboxesStillLive is returned when an identity change or deletion is
// refused because the current credentials still own provider resources.
var ErrSandboxesStillLive = stderrors.New("sandbox config still owns live sandboxes")

// SandboxesStillLiveError carries the provider inventory the handler must show
// in its 409 response; recomputing it could race and disagree with the refusal.
type SandboxesStillLiveError struct {
	Inventory SandboxInventory
}

func (e *SandboxesStillLiveError) Error() string {
	return fmt.Sprintf("%s: %d", ErrSandboxesStillLive, e.Inventory.SandboxCount)
}

func (e *SandboxesStillLiveError) Unwrap() error {
	return ErrSandboxesStillLive
}

// ErrSandboxInventoryUnverifiable is returned when the provider cannot be
// reached to answer "does this config still own sandboxes?".
//
// It is deliberately distinct from ErrSandboxesStillLive: one means "there ARE
// sandboxes, deal with them", the other means "we cannot tell". The second is
// the only case a force delete may override, because an endpoint whose DNS
// record is gone would otherwise make its config permanently undeletable.
var ErrSandboxInventoryUnverifiable = stderrors.New(
	"cannot verify whether the sandbox config still owns sandboxes")

// ErrSandboxConfigNameRequired is a sentinel so transports can classify this as
// bad input without matching on the message text.
var ErrSandboxConfigNameRequired = stderrors.New("sandbox config name is required")

// ErrNamedSandboxBackendUnsupported marks a sandbox type that cannot be stored
// as a user-facing named backend config.
var ErrNamedSandboxBackendUnsupported = stderrors.New(
	"named sandbox configs only support cube and e2b backends",
)

// SandboxInventory describes what a config holds and who a change disturbs.
type SandboxInventory struct {
	SandboxCount int      `json:"sandbox_count"`
	SessionIDs   []string `json:"session_ids,omitempty"`
	AgentNames   []string `json:"agent_names,omitempty"`

	// Unverifiable reports that SandboxCount is unknown rather than zero
	// because the provider could not be reached. The management page must say
	// so instead of rendering a reassuring "0 sandboxes".
	Unverifiable bool `json:"unverifiable,omitempty"`
}

// CreateSandboxConfigInput is the create payload.
type CreateSandboxConfigInput struct {
	Name        string
	Description string
	Config      *types.TenantSandboxConfig
}

// UpdateSandboxConfigInput is the update payload. Config may carry redacted
// placeholders that resolve against the stored row.
type UpdateSandboxConfigInput struct {
	Name        string
	Description string
	Config      *types.TenantSandboxConfig
}

// SandboxConfigAgentRepo is the slice of the agent repository this service
// needs. Agent references are warnings, never grounds for refusing a change.
type SandboxConfigAgentRepo interface {
	ListNamesBySandboxConfigID(ctx context.Context, tenantID uint64, configID string) ([]string, error)
}

// TenantSandboxConfigService owns the sandbox config lifecycle.
type TenantSandboxConfigService struct {
	repo      repository.TenantSandboxConfigRepository
	agents    SandboxConfigAgentRepo
	globalCfg *sandbox.Config
	now       func() time.Time

	// newClient is injectable so tests can supply a provider inventory.
	newClient func(*sandbox.Config) (sandbox.ConfigSandboxClient, error)
}

// NewTenantSandboxConfigService wires the config service.
func NewTenantSandboxConfigService(
	repo repository.TenantSandboxConfigRepository,
	agents SandboxConfigAgentRepo,
	globalCfg *sandbox.Config,
) *TenantSandboxConfigService {
	return &TenantSandboxConfigService{
		repo:      repo,
		agents:    agents,
		globalCfg: globalCfg,
		now:       time.Now,
		newClient: func(cfg *sandbox.Config) (sandbox.ConfigSandboxClient, error) {
			return sandbox.NewRemoteClientForCheck(cfg)
		},
	}
}

// SanitizeSandboxConfig resolves redacted secrets and validates the payload
// before it can be persisted.
func SanitizeSandboxConfig(
	incoming, existing *types.TenantSandboxConfig,
) (*types.TenantSandboxConfig, error) {
	if incoming == nil {
		return nil, nil
	}
	merged := types.MergeSandboxConfigForUpdate(incoming, existing)

	if merged.SandboxType != "" {
		if _, err := sandbox.ParseSandboxType(merged.SandboxType); err != nil {
			return nil, err
		}
	}
	for _, endpoint := range sandboxConfigEndpoints(merged) {
		if err := sandbox.ValidateOutboundURL(endpoint); err != nil {
			return nil, err
		}
	}
	// Without an AES key the Value() hook would persist these secrets in
	// plaintext. Refuse instead of silently downgrading storage security.
	if sandboxConfigHasSecrets(merged) && utils.GetAESKey() == nil {
		return nil, apperrors.NewBadRequestError(
			"SYSTEM_AES_KEY is not configured; refusing to store sandbox credentials in plaintext",
		)
	}
	// Reject an incomplete config here rather than at first sandbox allocation.
	// Resolving is what the runtime does, so both paths agree by construction.
	// The baseline passed in is irrelevant to the outcome: named configs inherit
	// no provider field, so only merged decides what is missing.
	// Returned unwrapped so respondSandboxConfigServiceError can classify the
	// sentinel; wrapping it in an AppError here would hide the chain.
	if _, err := sandbox.ResolveEffectiveConfig(merged, sandbox.DefaultConfig()); err != nil {
		return nil, err
	}
	return merged, nil
}

func validateNamedSandboxBackend(cfg *types.TenantSandboxConfig) error {
	if cfg == nil || strings.TrimSpace(cfg.SandboxType) == "" {
		return apperrors.NewBadRequestError("sandbox backend type is required")
	}
	if !sandbox.IsNamedSandboxBackendType(cfg.SandboxType) {
		return fmt.Errorf("%w", ErrNamedSandboxBackendUnsupported)
	}
	return nil
}

func filterPublicSandboxConfigs(
	list []*types.TenantSandboxConfigEntity,
) []*types.TenantSandboxConfigEntity {
	if len(list) == 0 {
		return list
	}
	out := make([]*types.TenantSandboxConfigEntity, 0, len(list))
	for _, e := range list {
		if types.IsSandboxWorkspacePolicyRow(e) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func findWorkspacePolicyRow(
	list []*types.TenantSandboxConfigEntity,
) *types.TenantSandboxConfigEntity {
	for _, e := range list {
		if types.IsSandboxWorkspacePolicyRow(e) {
			return e
		}
	}
	return nil
}

// WorkspaceScriptsDisabled reports whether agents that rely on the deployment
// default (empty sandbox_config_id) must not execute skill scripts.
func (s *TenantSandboxConfigService) WorkspaceScriptsDisabled(
	ctx context.Context, tenantID uint64,
) (bool, error) {
	list, err := s.repo.ListByTenant(ctx, tenantID)
	if err != nil {
		return false, err
	}
	return findWorkspacePolicyRow(list) != nil, nil
}

// SetWorkspaceScriptsDisabled toggles workspace-level script execution for
// agents that do not point at a named cube/e2b config.
func (s *TenantSandboxConfigService) SetWorkspaceScriptsDisabled(
	ctx context.Context, tenantID uint64, disabled bool,
) error {
	list, err := s.repo.ListByTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	existing := findWorkspacePolicyRow(list)
	if disabled {
		if existing != nil {
			return nil
		}
		entity := &types.TenantSandboxConfigEntity{
			ID:          uuid.New().String(),
			TenantID:    tenantID,
			Name:        types.SandboxWorkspacePolicyConfigName,
			Description: "",
			SandboxType: string(sandbox.SandboxTypeDisabled),
			Config:      &types.TenantSandboxConfig{SandboxType: string(sandbox.SandboxTypeDisabled)},
		}
		return s.repo.Create(ctx, entity)
	}
	if existing == nil {
		return nil
	}
	return s.repo.SoftDelete(ctx, tenantID, existing.ID)
}

// Create stores a new config.
func (s *TenantSandboxConfigService) Create(
	ctx context.Context, tenantID uint64, in CreateSandboxConfigInput,
) (*types.TenantSandboxConfigEntity, error) {
	merged, err := SanitizeSandboxConfig(in.Config, nil)
	if err != nil {
		return nil, err
	}
	if err := validateNamedSandboxBackend(merged); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, ErrSandboxConfigNameRequired
	}
	entity := &types.TenantSandboxConfigEntity{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		Name:        name,
		Description: in.Description,
		Config:      merged,
	}
	if merged != nil {
		entity.SandboxType = merged.SandboxType
	}
	if err := s.repo.Create(ctx, entity); err != nil {
		return nil, err
	}
	return entity, nil
}

// List returns the workspace's user-facing configs (policy row excluded).
func (s *TenantSandboxConfigService) List(
	ctx context.Context, tenantID uint64,
) ([]*types.TenantSandboxConfigEntity, error) {
	list, err := s.repo.ListByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return filterPublicSandboxConfigs(list), nil
}

// Get returns one config, or nil when absent.
func (s *TenantSandboxConfigService) Get(
	ctx context.Context, tenantID uint64, id string,
) (*types.TenantSandboxConfigEntity, error) {
	entity, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil || entity == nil {
		return nil, err
	}
	if types.IsSandboxWorkspacePolicyRow(entity) {
		return nil, nil
	}
	return entity, nil
}

// Inventory answers what changing or deleting this config would disturb.
//
// An unreachable provider is reported through Unverifiable rather than as an
// error: the management page still has to render the card, and the agent names
// it warns about come from our own database.
func (s *TenantSandboxConfigService) Inventory(
	ctx context.Context, tenantID uint64, id string,
) (SandboxInventory, error) {
	entity, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return SandboxInventory{}, err
	}
	if entity == nil {
		return SandboxInventory{}, apperrors.NewNotFoundError("sandbox config not found")
	}
	summaries, err := s.listSandboxes(ctx, entity.Config, tenantID, id)
	inv := s.inventoryFromSummaries(ctx, tenantID, id, summaries)
	if err != nil {
		logger.Warnf(ctx, "[sandbox] inventory of config %s is unverifiable: %v", id, err)
		inv.Unverifiable = true
	}
	return inv, nil
}

// listSandboxes enumerates what the config currently owns. A nil client means
// the backend holds no remote resources at all, which is a verified empty.
func (s *TenantSandboxConfigService) listSandboxes(
	ctx context.Context,
	cfg *types.TenantSandboxConfig,
	tenantID uint64,
	id string,
) ([]sandbox.RemoteSandboxSummary, error) {
	client, err := s.clientFor(cfg)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, nil
	}
	return sandbox.ListConfigSandboxes(ctx, client, tenantID, id)
}

// Update applies an edit. Identity edits are cordoned before inventory and
// swept afterwards using the old client so credentials are never overwritten
// while they still own provider resources.
func (s *TenantSandboxConfigService) Update(
	ctx context.Context, tenantID uint64, id string, in UpdateSandboxConfigInput,
) (*types.TenantSandboxConfigEntity, error) {
	entity, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil || entity == nil {
		return nil, err
	}
	if types.IsSandboxWorkspacePolicyRow(entity) {
		return nil, apperrors.NewBadRequestError("workspace policy cannot be edited here")
	}
	merged, err := SanitizeSandboxConfig(in.Config, entity.Config)
	if err != nil {
		return nil, err
	}
	if err := validateNamedSandboxBackend(merged); err != nil {
		return nil, err
	}
	if !SandboxIdentityChanged(entity.Config, merged) {
		return s.writeConfig(ctx, entity, in, merged)
	}

	if err := s.repo.SetCordon(ctx, tenantID, id, s.now()); err != nil {
		return nil, err
	}
	defer s.clearCordonAfterRequest(ctx, tenantID, id)

	// When old credentials no longer reach the provider we cannot enumerate
	// sandboxes to refuse the edit — but blocking the save traps the admin on
	// a key they are trying to fix. Proceed and skip the post-write sweep;
	// sandboxes we cannot see may become orphans and need provider-side cleanup.
	oldClient, err := s.clientFor(entity.Config)
	if err != nil {
		logger.Warnf(ctx,
			"[sandbox] config %s: old credentials unusable for inventory: %v; proceeding",
			id, err)
		oldClient = nil
	}
	if oldClient != nil {
		summaries, listErr := sandbox.ListConfigSandboxes(ctx, oldClient, tenantID, id)
		if listErr != nil {
			logger.Warnf(ctx,
				"[sandbox] config %s: cannot verify sandbox inventory with old credentials: %v; proceeding",
				id, listErr)
			oldClient = nil
		} else if len(summaries) > 0 {
			inv := s.inventoryFromSummaries(ctx, tenantID, id, summaries)
			return nil, &SandboxesStillLiveError{Inventory: inv}
		}
	}

	updated, err := s.writeConfig(ctx, entity, in, merged)
	if err != nil {
		return nil, err
	}

	if oldClient != nil {
		s.sweepAfterWrite(ctx, oldClient, tenantID, id)
	}
	return updated, nil
}

// Delete refuses only while the config still owns sandboxes. Agent references
// are permanent state and are surfaced as warnings by callers, not blockers.
//
// force covers exactly one case: the provider could not be reached, so we cannot
// tell whether anything is live. Without it a config whose endpoint's DNS record
// disappeared could never be removed — unlike an edit, deletion has no "create a
// second config" way out. It does NOT override sandboxes we can actually see;
// those still have to be dealt with through their sessions, otherwise the forced
// deletion would be precisely the permanent leak this whole flow prevents.
func (s *TenantSandboxConfigService) Delete(
	ctx context.Context, tenantID uint64, id string, force bool,
) error {
	entity, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	// Reporting success for a config that is not there would let the UI drop a
	// card the workspace still has, so absence is an explicit 404.
	if entity == nil {
		return apperrors.NewNotFoundError("sandbox config not found")
	}
	if types.IsSandboxWorkspacePolicyRow(entity) {
		return apperrors.NewBadRequestError("workspace policy cannot be deleted here")
	}
	summaries, listErr := s.listSandboxes(ctx, entity.Config, tenantID, id)
	if listErr != nil {
		if !force {
			return fmt.Errorf("%w: %v", ErrSandboxInventoryUnverifiable, listErr)
		}
		logger.Warnf(ctx,
			"[sandbox] force-deleting config %s without verifying its sandboxes: %v",
			id, listErr)
	}
	if len(summaries) > 0 {
		inv := s.inventoryFromSummaries(ctx, tenantID, id, summaries)
		return &SandboxesStillLiveError{Inventory: inv}
	}
	return s.repo.SoftDelete(ctx, tenantID, id)
}

func (s *TenantSandboxConfigService) clientFor(
	cfg *types.TenantSandboxConfig,
) (sandbox.ConfigSandboxClient, error) {
	// The baseline only supplies the deployment's execution timeout; every
	// provider field comes from cfg, so a nil globalCfg cannot change which
	// backend this client talks to.
	base := s.globalCfg
	if base == nil {
		base = sandbox.DefaultConfig()
	}
	effective, err := sandbox.ResolveEffectiveConfig(cfg, base)
	if err != nil {
		return nil, err
	}
	switch effective.Type {
	case sandbox.SandboxTypeCube, sandbox.SandboxTypeE2B:
		return s.newClient(effective)
	default:
		return nil, nil
	}
}

func (s *TenantSandboxConfigService) writeConfig(
	ctx context.Context,
	entity *types.TenantSandboxConfigEntity,
	in UpdateSandboxConfigInput,
	merged *types.TenantSandboxConfig,
) (*types.TenantSandboxConfigEntity, error) {
	if name := strings.TrimSpace(in.Name); name != "" {
		entity.Name = name
	}
	entity.Description = in.Description
	entity.Config = merged
	if merged != nil {
		entity.SandboxType = merged.SandboxType
	}
	if err := s.repo.Update(ctx, entity); err != nil {
		return nil, err
	}
	return entity, nil
}

func (s *TenantSandboxConfigService) inventoryFromSummaries(
	ctx context.Context,
	tenantID uint64,
	id string,
	summaries []sandbox.RemoteSandboxSummary,
) SandboxInventory {
	inv := SandboxInventory{SandboxCount: len(summaries)}
	for _, summary := range summaries {
		if sessionID := summary.Metadata[sandbox.MetadataSessionIDKey()]; sessionID != "" {
			inv.SessionIDs = append(inv.SessionIDs, sessionID)
		}
	}
	if s.agents == nil {
		return inv
	}
	names, err := s.agents.ListNamesBySandboxConfigID(ctx, tenantID, id)
	if err != nil {
		logger.Warnf(ctx, "[sandbox] list agents for config %s: %v", id, err)
		return inv
	}
	inv.AgentNames = names
	return inv
}

func (s *TenantSandboxConfigService) clearCordonAfterRequest(
	ctx context.Context,
	tenantID uint64,
	id string,
) {
	cleanupCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), sandboxConfigCleanupTimeout)
	defer cancel()
	if err := s.repo.ClearCordon(cleanupCtx, tenantID, id); err != nil {
		logger.Warnf(ctx, "[sandbox] clear cordon on config %s: %v", id, err)
	}
}

func (s *TenantSandboxConfigService) sweepAfterWrite(
	ctx context.Context,
	oldClient sandbox.ConfigSandboxClient,
	tenantID uint64,
	id string,
) {
	cleanupCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), sandboxConfigCleanupTimeout)
	defer cancel()
	deleted, err := sandbox.ReleaseConfigSandboxes(cleanupCtx, oldClient, tenantID, id)
	if err != nil {
		logger.Warnf(ctx, "[sandbox] post-write sweep of config %s failed: %v", id, err)
		return
	}
	if deleted > 0 {
		logger.Infof(ctx,
			"[sandbox] swept %d sandbox(es) created during the cordon window on config %s",
			deleted, id)
	}
}

// sandboxConfigEndpoints returns every non-empty tenant-supplied URL.
func sandboxConfigEndpoints(cfg *types.TenantSandboxConfig) []string {
	if cfg == nil {
		return nil
	}
	var endpoints []string
	if cfg.Cube != nil {
		for _, raw := range []string{cfg.Cube.APIURL, cfg.Cube.ProxyURL} {
			if raw != "" {
				endpoints = append(endpoints, raw)
			}
		}
	}
	if cfg.E2B != nil && cfg.E2B.APIURL != "" {
		endpoints = append(endpoints, cfg.E2B.APIURL)
	}
	return endpoints
}

// sandboxConfigHasSecrets reports whether cfg carries any value that must be
// encrypted at rest.
func sandboxConfigHasSecrets(cfg *types.TenantSandboxConfig) bool {
	if cfg == nil {
		return false
	}
	if cfg.Cube != nil && cfg.Cube.APIKey != "" {
		return true
	}
	if cfg.E2B != nil && cfg.E2B.APIKey != "" {
		return true
	}
	for _, value := range cfg.EnvVars {
		if value != "" {
			return true
		}
	}
	return false
}
