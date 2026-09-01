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
	"crypto/sha256"
	"encoding/hex"
	stderrors "errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
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

func configHasSkillSnapshot(cfg *types.TenantSandboxConfig) bool {
	return cfg != nil && cfg.SkillImage != nil && strings.TrimSpace(cfg.SkillImage.SnapshotID) != ""
}

// skillRetargetWouldChange reports edits that would retarget the environment
// a skill snapshot is built from: identity, spawn template, or Cube DNS.
func skillRetargetWouldChange(stored, merged *types.TenantSandboxConfig) bool {
	if SandboxIdentityChanged(stored, merged) {
		return true
	}
	if spawnTemplateID(stored) != spawnTemplateID(merged) {
		return true
	}
	return !sameStrings(cubeDNSServers(stored), cubeDNSServers(merged))
}

// spawnTemplateID is the template/image this config would boot without a
// skill snapshot. Cube and E2B store it as template_id; Docker as the image.
func spawnTemplateID(cfg *types.TenantSandboxConfig) string {
	if cfg == nil {
		return ""
	}
	switch sandbox.SandboxType(cfg.SandboxType) {
	case sandbox.SandboxTypeCube:
		if cfg.Cube != nil {
			return strings.TrimSpace(cfg.Cube.TemplateID)
		}
	case sandbox.SandboxTypeE2B:
		if cfg.E2B != nil {
			return strings.TrimSpace(cfg.E2B.TemplateID)
		}
	case sandbox.SandboxTypeDocker:
		if cfg.Docker != nil {
			return strings.TrimSpace(cfg.Docker.Image)
		}
	}
	return ""
}

func cubeDNSServers(cfg *types.TenantSandboxConfig) []string {
	if cfg == nil || cfg.Cube == nil {
		return nil
	}
	return cfg.Cube.DNSServers
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// skillSnapshotBlocksConnectionChange reports edits that would retarget the
// environment a skill snapshot was built from: identity, spawn template, or
// Cube DNS (which only applies if the template is rebuilt).
func skillSnapshotBlocksConnectionChange(stored, merged *types.TenantSandboxConfig) bool {
	return configHasSkillSnapshot(stored) && skillRetargetWouldChange(stored, merged)
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
	"named sandbox configs only support cube, e2b and docker backends",
)

// ErrSkillSnapshotBlocksTemplateChange is returned when this config already
// has a skill snapshot and the caller tries to retarget its connection, DNS,
// or spawn template, or to rebuild the cluster's WeKnora template. Sessions
// boot the snapshot, so those edits would not reach them. The way out is a
// second config.
var ErrSkillSnapshotBlocksTemplateChange = stderrors.New(
	"sandbox connection cannot change while this config has a skill snapshot")

// ErrSkillSnapshotReleaseFailed is returned when deleting a sandbox config
// cannot destroy every provider snapshot on that config's ledger. Leaving the
// row is recoverable; leaking a billable snapshot is not.
var ErrSkillSnapshotReleaseFailed = stderrors.New("failed to release skill snapshots")

// SkillSnapshotReleaseFailedError names the snapshots that are still on the
// provider so the handler can show them and a retry can skip already-deleted
// ones.
type SkillSnapshotReleaseFailedError struct {
	Remaining []string
}

func (e *SkillSnapshotReleaseFailedError) Error() string {
	if e == nil || len(e.Remaining) == 0 {
		return ErrSkillSnapshotReleaseFailed.Error()
	}
	return fmt.Sprintf("%s: %s", ErrSkillSnapshotReleaseFailed.Error(), strings.Join(e.Remaining, ", "))
}

func (e *SkillSnapshotReleaseFailedError) Unwrap() error {
	return ErrSkillSnapshotReleaseFailed
}

// sandboxSnapshotReleaser is the provider capability Delete needs. It is a
// named slice of RemoteSnapshotManager so tests do not have to fake Create or
// List, and so the type assertion is not anonymous.
type sandboxSnapshotReleaser interface {
	DeleteSnapshot(ctx context.Context, snapshotID string) error
}

// sandboxConfigSkillStore is the skill/ledger slice Delete needs to destroy
// snapshots and drop rows that would dangle after SoftDelete.
type sandboxConfigSkillStore interface {
	ListSnapshotsByConfig(
		ctx context.Context, tenantID uint64, configID string,
	) ([]*types.TenantSkillSnapshotEntity, error)
	MarkSnapshotState(ctx context.Context, tenantID uint64, id, state, snapshotID string) error
	ListSkillsByConfig(ctx context.Context, tenantID uint64, configID string) ([]*types.TenantSkillEntity, error)
	DeleteSkill(ctx context.Context, tenantID uint64, configID, skillID string) error
	DeleteSnapshotRowsByConfig(ctx context.Context, tenantID uint64, configID string) error
	DeleteUserEnvVarsByConfig(ctx context.Context, tenantID uint64, configID string) error
}

// sandboxConfigBundleResolver locates the tenant file service so config
// deletion can drop skill archives after the ledger is released.
type sandboxConfigBundleResolver interface {
	ResolveFileService(
		ctx context.Context, tenant *types.Tenant, backendID, provider, localBaseDir string,
	) (interfaces.FileService, string, error)
}

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

// SandboxTemplateQueryInput describes an unsaved connection from the settings
// drawer. ConfigID is optional and lets masked credentials resolve against the
// stored row while editing.
type SandboxTemplateQueryInput struct {
	Config          *types.TenantSandboxConfig
	ConfigID        string
	EnsureStandard  bool
	ReplaceStandard bool
}

type SandboxTemplateCatalog struct {
	Templates          []sandbox.RemoteTemplate `json:"templates"`
	StandardTemplateID string                   `json:"standard_template_id,omitempty"`
	Provisioned        bool                     `json:"provisioned"`
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

	// skills and files are used only when deleting a config, to destroy the
	// snapshot ledger and drop skill archives. Either may be nil in tests
	// that never delete a config that owns skills.
	skills sandboxConfigSkillStore
	files  sandboxConfigBundleResolver

	// newClient is injectable so tests can supply a provider inventory.
	newClient func(*sandbox.Config) (sandbox.ConfigSandboxClient, error)

	// ensureTemplate collapses concurrent "make sure this cluster has our
	// template" requests per cluster. Provisioning is idempotent only once the
	// build shows up in the provider's catalog, and a double-click on refresh
	// is fast enough to slip in before that.
	ensureTemplate singleflight.Group
}

// NewTenantSandboxConfigService wires the config service.
func NewTenantSandboxConfigService(
	repo repository.TenantSandboxConfigRepository,
	agents SandboxConfigAgentRepo,
	globalCfg *sandbox.Config,
	skills repository.TenantSkillRepository,
	files sandboxConfigBundleResolver,
) *TenantSandboxConfigService {
	return &TenantSandboxConfigService{
		repo:      repo,
		agents:    agents,
		globalCfg: globalCfg,
		now:       time.Now,
		skills:    skills,
		files:     files,
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
		parsed, err := sandbox.ParseSandboxType(merged.SandboxType)
		if err != nil {
			return nil, err
		}
		if err := sandbox.EnsureDockerBackendAllowed(parsed); err != nil {
			return nil, err
		}
	}
	if merged.Cube != nil {
		dns, err := sandbox.NormalizeCubeDNSServers(merged.Cube.DNSServers)
		if err != nil {
			return nil, apperrors.NewBadRequestError(err.Error())
		}
		merged.Cube.DNSServers = dns
	}
	for _, endpoint := range sandboxConfigEndpoints(merged) {
		if err := sandbox.ValidateOutboundURLWithPolicy(endpoint, sandbox.OutboundURLPolicy{
			AllowPrivate: merged.AllowPrivateEndpoints,
		}); err != nil {
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
	if err := validateSkillRollout(merged.SkillRollout); err != nil {
		return nil, err
	}
	return merged, nil
}

func validateSkillRollout(value string) error {
	switch strings.TrimSpace(value) {
	case "", types.SkillRolloutNextTurn, types.SkillRolloutNewSession:
		return nil
	default:
		return apperrors.NewBadRequestError("invalid skill_rollout")
	}
}

func validateNamedSandboxBackend(cfg *types.TenantSandboxConfig) error {
	if cfg == nil || strings.TrimSpace(cfg.SandboxType) == "" {
		return apperrors.NewBadRequestError("sandbox backend type is required")
	}
	if !sandbox.IsNamedSandboxBackendType(cfg.SandboxType) {
		return fmt.Errorf("%w", ErrNamedSandboxBackendUnsupported)
	}
	return sandbox.EnsureDockerBackendAllowed(sandbox.SandboxType(cfg.SandboxType))
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

// WorkspaceScriptsDisabled reports whether the workspace-wide kill switch is
// active, regardless of which named backend an agent selected.
func (s *TenantSandboxConfigService) WorkspaceScriptsDisabled(
	ctx context.Context, tenantID uint64,
) (bool, error) {
	list, err := s.repo.ListByTenant(ctx, tenantID)
	if err != nil {
		return false, err
	}
	return findWorkspacePolicyRow(list) != nil, nil
}

// SetWorkspaceScriptsDisabled toggles script execution for the entire
// workspace, across all named backend types.
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
	if err := validateNamedSandboxBackend(in.Config); err != nil {
		return nil, err
	}
	merged, err := SanitizeSandboxConfig(in.Config, nil)
	if err != nil {
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

// QueryTemplates reads the provider's template catalog and optionally installs
// the standard WeKnora image when it is absent. This is intentionally driven by
// workspace credentials instead of deployment environment variables.
func (s *TenantSandboxConfigService) QueryTemplates(
	ctx context.Context, tenantID uint64, in SandboxTemplateQueryInput,
) (*SandboxTemplateCatalog, error) {
	if in.ReplaceStandard && strings.TrimSpace(in.ConfigID) == "" {
		return nil, apperrors.NewBadRequestError(
			"config_id is required to rebuild the standard template")
	}
	var existing *types.TenantSandboxConfig
	if strings.TrimSpace(in.ConfigID) != "" {
		entity, err := s.repo.GetByID(ctx, tenantID, in.ConfigID)
		if err != nil {
			return nil, err
		}
		if entity == nil || types.IsSandboxWorkspacePolicyRow(entity) {
			return nil, apperrors.NewNotFoundError("sandbox config not found")
		}
		existing = entity.Config
	}
	merged := types.MergeSandboxConfigForUpdate(in.Config, existing)
	if merged == nil {
		merged = types.MergeSandboxConfigForUpdate(existing, nil)
	}
	if merged == nil {
		return nil, apperrors.NewBadRequestError("sandbox config is required")
	}
	if in.ReplaceStandard {
		if err := s.refuseClusterSkillTemplateReplace(ctx, tenantID, merged); err != nil {
			return nil, err
		}
	}

	// Catalog access needs the control-plane connection but does not need a
	// spawn template yet. A private placeholder lets the same effective-config
	// validation protect every other required field without weakening runtime
	// validation.
	switch merged.SandboxType {
	case string(sandbox.SandboxTypeCube):
		if merged.Cube == nil {
			merged.Cube = &types.CubeSandboxConfig{}
		}
		if strings.TrimSpace(merged.Cube.TemplateID) == "" {
			merged.Cube.TemplateID = "__catalog__"
		}
	case string(sandbox.SandboxTypeE2B):
		if merged.E2B == nil {
			merged.E2B = &types.E2BSandboxConfig{}
		}
		if strings.TrimSpace(merged.E2B.TemplateID) == "" {
			merged.E2B.TemplateID = "__catalog__"
		}
	case string(sandbox.SandboxTypeDocker):
		if err := sandbox.EnsureDockerBackendAllowed(sandbox.SandboxTypeDocker); err != nil {
			return nil, err
		}
		// Docker's template is an image, and the catalog step is where the
		// admin picks one. The standard image stands in until they do, so the
		// effective-config validation below has something to accept.
		if merged.Docker == nil {
			merged.Docker = &types.DockerSandboxConfig{}
		}
		if strings.TrimSpace(merged.Docker.Image) == "" {
			merged.Docker.Image = sandbox.DefaultDockerImage
		}
	default:
		return nil, apperrors.NewBadRequestError(
			"sandbox template catalog only supports cube, e2b and docker backends")
	}

	for _, endpoint := range sandboxConfigEndpoints(merged) {
		if err := sandbox.ValidateOutboundURLWithPolicy(endpoint, sandbox.OutboundURLPolicy{
			AllowPrivate: merged.AllowPrivateEndpoints,
		}); err != nil {
			return nil, err
		}
	}
	effective, err := sandbox.ResolveEffectiveConfig(merged, sandbox.DefaultConfig())
	if err != nil {
		return nil, err
	}
	client, err := s.newClient(effective)
	if err != nil {
		return nil, err
	}
	catalog, ok := any(client).(sandbox.RemoteTemplateCatalog)
	if !ok {
		return nil, fmt.Errorf("sandbox: provider %q does not expose templates", effective.Type)
	}
	templates, err := catalog.ListTemplates(ctx)
	if err != nil {
		return nil, err
	}
	result := &SandboxTemplateCatalog{Templates: deduplicateSandboxTemplates(templates)}
	usable := pickStandardTemplate(result.Templates)
	if usable != nil {
		result.StandardTemplateID = usable.ID
	}
	oldStandardIDs := standardTemplateIDs(result.Templates)
	// Listing is read-only. Creating or replacing the WeKnora template is an
	// explicit settings-page action: auto-ensure on every refresh made DNS
	// and image changes impossible to apply, and left admins unsure whether
	// they had asked for a build.
	wantStandard := in.ReplaceStandard || (in.EnsureStandard && usable == nil)
	if wantStandard {
		op := "ensure"
		if in.ReplaceStandard {
			op = "replace"
		}
		key := op + ":" + ensureTemplateKey(tenantID, sandbox.IdentityOf(merged))
		ensured, ensureErr, _ := s.ensureTemplate.Do(key, func() (any, error) {
			if in.ReplaceStandard {
				return catalog.ReplaceStandardTemplate(ctx)
			}
			return catalog.EnsureStandardTemplate(ctx)
		})
		if ensureErr != nil {
			return nil, ensureErr
		}
		standard, ok := ensured.(*sandbox.RemoteTemplate)
		if !ok || standard == nil {
			return nil, fmt.Errorf("sandbox: provider %q returned no standard template", effective.Type)
		}
		result.Templates = deduplicateSandboxTemplates(append(result.Templates, *standard))
		if sandbox.IsTemplateReady(standard.Status) {
			result.Provisioned = true
			if in.ReplaceStandard {
				persistErr := s.persistSpawnTemplateID(ctx, tenantID, merged, standard.ID, oldStandardIDs)
				if persistErr != nil {
					logger.Warnf(ctx, "[sandbox] persist rebuilt template id: %v; keeping previous templates",
						persistErr)
				} else {
					result.StandardTemplateID = standard.ID
					result.Templates = hideSupersededStandardTemplates(result.Templates, standard.ID)
					s.deleteSupersededStandardTemplates(ctx, catalog, standard.ID)
				}
			} else {
				result.StandardTemplateID = standard.ID
			}
		} else if !sandbox.IsTemplateBuildFailed(standard.Status) {
			result.Provisioned = true
			if result.StandardTemplateID == "" {
				result.StandardTemplateID = standard.ID
			}
		}
	}
	sort.SliceStable(result.Templates, func(i, j int) bool {
		if result.Templates[i].Standard != result.Templates[j].Standard {
			return result.Templates[i].Standard
		}
		return strings.ToLower(result.Templates[i].Name) < strings.ToLower(result.Templates[j].Name)
	})
	return result, nil
}

func hideSupersededStandardTemplates(items []sandbox.RemoteTemplate, keepID string) []sandbox.RemoteTemplate {
	keepID = strings.TrimSpace(keepID)
	kept := make([]sandbox.RemoteTemplate, 0, len(items))
	for _, item := range items {
		if item.Standard && strings.TrimSpace(item.ID) != keepID {
			continue
		}
		kept = append(kept, item)
	}
	return kept
}

func standardTemplateIDs(items []sandbox.RemoteTemplate) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item.Standard {
			if id := strings.TrimSpace(item.ID); id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}

func (s *TenantSandboxConfigService) refuseClusterSkillTemplateReplace(
	ctx context.Context, tenantID uint64, merged *types.TenantSandboxConfig,
) error {
	identity := sandbox.IdentityOf(merged)
	list, err := s.repo.ListByTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	for _, entity := range list {
		if entity == nil || types.IsSandboxWorkspacePolicyRow(entity) {
			continue
		}
		if sandbox.IdentityOf(entity.Config) != identity {
			continue
		}
		if configHasSkillSnapshot(entity.Config) ||
			s.configHasInFlightSkill(ctx, entity.TenantID, entity.ID) {
			return ErrSkillSnapshotBlocksTemplateChange
		}
	}
	return nil
}

func (s *TenantSandboxConfigService) configHasInFlightSkill(
	ctx context.Context, tenantID uint64, configID string,
) bool {
	if s == nil || s.skills == nil || strings.TrimSpace(configID) == "" {
		return false
	}
	rows, err := s.skills.ListSkillsByConfig(ctx, tenantID, configID)
	if err != nil {
		logger.Warnf(ctx, "[sandbox] cannot read skills of config %s while judging a retarget: %v",
			configID, err)
		return true
	}
	for _, row := range rows {
		if row == nil {
			continue
		}
		switch row.Status {
		case types.SkillStatusInstalling, types.SkillStatusRemoving:
			return true
		}
	}
	return false
}

func (s *TenantSandboxConfigService) persistSpawnTemplateID(
	ctx context.Context,
	tenantID uint64,
	merged *types.TenantSandboxConfig,
	newID string,
	oldIDs []string,
) error {
	newID = strings.TrimSpace(newID)
	if newID == "" || s == nil || s.repo == nil {
		return nil
	}
	old := make(map[string]struct{}, len(oldIDs))
	for _, id := range oldIDs {
		if id = strings.TrimSpace(id); id != "" {
			old[id] = struct{}{}
		}
	}
	identity := sandbox.IdentityOf(merged)
	list, err := s.repo.ListByTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("list configs: %w", err)
	}
	var persistErr error
	for _, entity := range list {
		if entity == nil || entity.Config == nil || types.IsSandboxWorkspacePolicyRow(entity) {
			continue
		}
		if sandbox.IdentityOf(entity.Config) != identity {
			continue
		}
		current := spawnTemplateID(entity.Config)
		if current == newID {
			continue
		}
		if current != "" {
			if _, pointedAtOld := old[current]; !pointedAtOld {
				continue
			}
		}
		previous := current
		setSpawnTemplateID(entity.Config, newID)
		if err := s.repo.Update(ctx, entity); err != nil {
			setSpawnTemplateID(entity.Config, previous)
			logger.Warnf(ctx, "[sandbox] persist rebuilt template id on config %s: %v", entity.ID, err)
			persistErr = err
			continue
		}
		logger.Infof(ctx, "[sandbox] config %s spawn template is now %s after rebuild", entity.ID, newID)
	}
	return persistErr
}

func (s *TenantSandboxConfigService) deleteSupersededStandardTemplates(
	ctx context.Context, catalog sandbox.RemoteTemplateCatalog, keepID string,
) {
	if catalog == nil || strings.TrimSpace(keepID) == "" {
		return
	}
	if err := catalog.DeleteSupersededStandardTemplates(ctx, keepID); err != nil {
		logger.Warnf(ctx, "[sandbox] delete superseded standard templates failed: %v", err)
	}
}

func setSpawnTemplateID(cfg *types.TenantSandboxConfig, id string) {
	if cfg == nil {
		return
	}
	switch sandbox.SandboxType(cfg.SandboxType) {
	case sandbox.SandboxTypeCube:
		if cfg.Cube != nil {
			cfg.Cube.TemplateID = id
		}
	case sandbox.SandboxTypeE2B:
		if cfg.E2B != nil {
			cfg.E2B.TemplateID = id
		}
	}
}

// pickStandardTemplate returns the WeKnora template the UI should preselect, or
// nil when the cluster has none that could ever spawn a sandbox. A failed build
// is skipped so the caller can reprovision instead of offering it.
func pickStandardTemplate(items []sandbox.RemoteTemplate) *sandbox.RemoteTemplate {
	var best *sandbox.RemoteTemplate
	for i := range items {
		if !items[i].Standard || sandbox.IsTemplateBuildFailed(items[i].Status) {
			continue
		}
		if best == nil || templateStatusRank(items[i].Status) > templateStatusRank(best.Status) {
			best = &items[i]
		}
	}
	return best
}

// ensureTemplateKey names one cluster as seen by one tenant. The identity
// carries an API key, so it is hashed rather than formatted: this string is
// only ever compared, and it should not be able to surface a credential in a
// panic trace or a heap dump.
func ensureTemplateKey(tenantID uint64, identity sandbox.SandboxIdentity) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%#v", tenantID, identity)))
	return hex.EncodeToString(sum[:])
}

func deduplicateSandboxTemplates(items []sandbox.RemoteTemplate) []sandbox.RemoteTemplate {
	if len(items) < 2 {
		return items
	}
	result := make([]sandbox.RemoteTemplate, 0, len(items))
	indexByID := make(map[string]int, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			result = append(result, item)
			continue
		}
		idx, exists := indexByID[id]
		if !exists {
			indexByID[id] = len(result)
			result = append(result, item)
			continue
		}
		current := &result[idx]
		current.Standard = current.Standard || item.Standard
		if templateStatusRank(item.Status) > templateStatusRank(current.Status) {
			current.Status = item.Status
			current.Version = item.Version
			current.UpdatedAt = item.UpdatedAt
			current.Error = item.Error
		}
		if strings.TrimSpace(current.Name) == "" ||
			(strings.EqualFold(current.Name, sandbox.StandardTemplateName) && strings.Contains(item.Name, "/")) {
			current.Name = item.Name
		}
		if current.Image == "" {
			current.Image = item.Image
		}
	}
	return result
}

func templateStatusRank(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ready", "available", "complete", "completed", "success", "succeeded":
		return 3
	case "building", "waiting", "pending", "queued", "processing", "running":
		return 2
	case "failed", "error", "cancelled", "canceled":
		return 1
	default:
		return 0
	}
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
	if in.Config != nil && strings.TrimSpace(in.Config.SandboxType) != "" {
		if err := validateNamedSandboxBackend(in.Config); err != nil {
			return nil, err
		}
	}
	merged, err := SanitizeSandboxConfig(in.Config, entity.Config)
	if err != nil {
		return nil, err
	}
	if err := validateNamedSandboxBackend(merged); err != nil {
		return nil, err
	}
	if skillSnapshotBlocksConnectionChange(entity.Config, merged) ||
		(skillRetargetWouldChange(entity.Config, merged) && s.configHasInFlightSkill(ctx, tenantID, id)) {
		return nil, ErrSkillSnapshotBlocksTemplateChange
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

// Delete refuses while the config still owns sandboxes, and while skill
// snapshots on its ledger cannot be destroyed. Agent references are permanent
// state and are surfaced as warnings by callers, not blockers.
//
// force covers two cases we cannot complete from here: the provider could not
// be reached to list sandboxes, and a skill snapshot could not be destroyed.
// Without it a config whose endpoint's DNS record disappeared, or whose
// snapshot API is down, could never be removed — unlike an edit, deletion has
// no "create a second config" way out. It does NOT override sandboxes we can
// actually see; those still have to be dealt with through their sessions,
// otherwise the forced deletion would be precisely the permanent leak this
// whole flow prevents.
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
	if err := s.releaseSkillSnapshots(ctx, entity, tenantID, id, force); err != nil {
		return err
	}
	return s.repo.SoftDelete(ctx, tenantID, id)
}

func snapshotReleaserFrom(client sandbox.ConfigSandboxClient) (sandboxSnapshotReleaser, bool) {
	if client == nil {
		return nil, false
	}
	releaser, ok := client.(sandboxSnapshotReleaser)
	return releaser, ok
}

// pendingNeedProvider reports whether releasing these rows has to talk to the
// provider at all. A planned name counts: it is how an abandoned build — one
// whose commit outlived the process that started it — is still addressable.
func pendingNeedProvider(rows []*types.TenantSkillSnapshotEntity) bool {
	for _, row := range rows {
		if row == nil {
			continue
		}
		if strings.TrimSpace(row.SnapshotID) != "" ||
			strings.TrimSpace(row.PlannedName) != "" {
			return true
		}
	}
	return false
}

// resolveAbandonedBuildIDs fills in the provider ID of a build that died
// between its commit and the ledger write.
//
// This is the last chance to reclaim one: PruneSupersededSnapshots walks live
// configs, so once this config's row is gone the snapshot is unreachable by
// anything. A name that resolves to nothing is left alone — the usual reason
// is that the commit never happened, and there is no resource to release.
func resolveAbandonedBuildIDs(
	ctx context.Context,
	client sandbox.ConfigSandboxClient,
	pending []*types.TenantSkillSnapshotEntity,
) {
	unresolved := make([]*types.TenantSkillSnapshotEntity, 0, len(pending))
	for _, row := range pending {
		if row != nil && strings.TrimSpace(row.SnapshotID) == "" &&
			strings.TrimSpace(row.PlannedName) != "" {
			unresolved = append(unresolved, row)
		}
	}
	if len(unresolved) == 0 {
		return
	}
	lister, ok := client.(skillSnapshotLister)
	if !ok {
		return
	}
	listed, err := lister.ListSnapshots(ctx, "")
	if err != nil {
		logger.Warnf(ctx, "[sandbox] list snapshots to release abandoned builds failed: %v", err)
		return
	}
	prefix := skillSnapshotNamePrefix(unresolved[0].TenantID, unresolved[0].SandboxConfigID)
	listed = snapshotsNotFromOtherConfig(listed, prefix)
	for _, row := range unresolved {
		id := matchSnapshotByName(listed, row.PlannedName)
		if id == "" {
			continue
		}
		logger.Infof(ctx, "[sandbox] abandoned build %s resolved to snapshot %s for release",
			row.PlannedName, id)
		row.SnapshotID = id
	}
}

func pendingSkillSnapshots(rows []*types.TenantSkillSnapshotEntity) []*types.TenantSkillSnapshotEntity {
	var out []*types.TenantSkillSnapshotEntity
	for _, row := range rows {
		if row == nil || row.State == types.SkillSnapshotStateDeleted {
			continue
		}
		out = append(out, row)
	}
	return out
}

func skillSnapshotName(row *types.TenantSkillSnapshotEntity) string {
	if row == nil {
		return ""
	}
	if name := strings.TrimSpace(row.SnapshotID); name != "" {
		return name
	}
	return row.ID
}

func (s *TenantSandboxConfigService) releaseSkillSnapshots(
	ctx context.Context,
	entity *types.TenantSandboxConfigEntity,
	tenantID uint64,
	configID string,
	force bool,
) error {
	if s.skills == nil {
		return nil
	}

	rows, err := s.skills.ListSnapshotsByConfig(ctx, tenantID, configID)
	if err != nil {
		if !force {
			return fmt.Errorf("%w: list snapshots: %v", ErrSkillSnapshotReleaseFailed, err)
		}
		logger.Warnf(ctx,
			"[sandbox] force-deleting config %s without listing skill snapshots: %v",
			configID, err)
	}

	pending := pendingSkillSnapshots(rows)
	remaining := s.destroyPendingSnapshots(ctx, entity, pending)
	if len(remaining) > 0 && !force {
		return &SkillSnapshotReleaseFailedError{Remaining: remaining}
	}
	if len(remaining) > 0 {
		logger.Warnf(ctx,
			"[sandbox] force-deleting config %s with unreleased snapshots %s",
			configID, strings.Join(remaining, ", "))
	}

	s.cleanupSkillMetadata(ctx, tenantID, configID)
	return nil
}

func (s *TenantSandboxConfigService) destroyPendingSnapshots(
	ctx context.Context,
	entity *types.TenantSandboxConfigEntity,
	pending []*types.TenantSkillSnapshotEntity,
) []string {
	if len(pending) == 0 {
		return nil
	}

	releaser := sandboxSnapshotReleaser(nil)
	if pendingNeedProvider(pending) {
		client := s.snapshotClientFor(ctx, entity)
		resolveAbandonedBuildIDs(ctx, client, pending)
		if found, ok := snapshotReleaserFrom(client); ok {
			releaser = found
		} else if client != nil {
			logger.Warnf(ctx, "[sandbox] config %s provider does not support snapshot delete",
				entityID(entity))
		}
	}
	var remaining []string
	for _, row := range pending {
		if err := deleteProviderSnapshot(ctx, releaser, row); err != nil {
			remaining = append(remaining, skillSnapshotName(row))
			continue
		}
		if err := s.skills.MarkSnapshotState(
			ctx, row.TenantID, row.ID, types.SkillSnapshotStateDeleted, row.SnapshotID,
		); err != nil {
			logger.Warnf(ctx, "[sandbox] mark snapshot %s deleted failed: %v", row.ID, err)
			remaining = append(remaining, skillSnapshotName(row))
		}
	}
	return remaining
}

// snapshotClientFor builds the provider client config deletion needs to
// release this config's snapshots. It returns the client rather than just the
// delete capability because an abandoned build has to be looked up by name
// first, which is a different capability on the same client.
func (s *TenantSandboxConfigService) snapshotClientFor(
	ctx context.Context, entity *types.TenantSandboxConfigEntity,
) sandbox.ConfigSandboxClient {
	cfg := (*types.TenantSandboxConfig)(nil)
	if entity != nil {
		cfg = entity.Config
	}
	client, err := s.clientFor(cfg)
	if err != nil {
		logger.Warnf(ctx, "[sandbox] cannot build snapshot client for config %s: %v",
			entityID(entity), err)
		return nil
	}
	return client
}

func entityID(entity *types.TenantSandboxConfigEntity) string {
	if entity == nil {
		return ""
	}
	return entity.ID
}

func deleteProviderSnapshot(
	ctx context.Context, releaser sandboxSnapshotReleaser, row *types.TenantSkillSnapshotEntity,
) error {
	if row == nil {
		return nil
	}
	snapshotID := strings.TrimSpace(row.SnapshotID)
	if snapshotID == "" {
		return nil
	}
	if releaser == nil {
		return fmt.Errorf("snapshot delete is unavailable")
	}
	err := releaser.DeleteSnapshot(ctx, snapshotID)
	if err == nil || sandbox.IsRemoteNotFound(err) {
		return nil
	}
	return err
}

func (s *TenantSandboxConfigService) cleanupSkillMetadata(
	ctx context.Context, tenantID uint64, configID string,
) {
	skills, err := s.skills.ListSkillsByConfig(ctx, tenantID, configID)
	if err != nil {
		logger.Warnf(ctx, "[sandbox] list skills for config %s cleanup failed: %v", configID, err)
	}
	for _, skill := range skills {
		if skill == nil {
			continue
		}
		s.deleteSkillBundleBestEffort(ctx, tenantID, skill.BundleRef)
		if err := s.skills.DeleteSkill(ctx, tenantID, configID, skill.ID); err != nil {
			logger.Warnf(ctx, "[sandbox] delete skill %s on config %s failed: %v",
				skill.ID, configID, err)
		}
	}
	if err := s.skills.DeleteSnapshotRowsByConfig(ctx, tenantID, configID); err != nil {
		logger.Warnf(ctx, "[sandbox] delete snapshot ledger for config %s failed: %v",
			configID, err)
	}
	// DeleteSkill only takes the values filed under a skill; the config-wide
	// ones have no skill to hang off and would outlive the config.
	if err := s.skills.DeleteUserEnvVarsByConfig(ctx, tenantID, configID); err != nil {
		logger.Warnf(ctx, "[sandbox] delete member env vars for config %s failed: %v",
			configID, err)
	}
}

func (s *TenantSandboxConfigService) deleteSkillBundleBestEffort(
	ctx context.Context, tenantID uint64, bundleRef string,
) {
	if s.files == nil || strings.TrimSpace(bundleRef) == "" {
		return
	}
	fs, _, err := s.files.ResolveFileService(ctx, &types.Tenant{ID: tenantID}, "", "", "")
	if err != nil || fs == nil {
		logger.Warnf(ctx, "[sandbox] resolve file service to delete bundle %s failed: %v",
			bundleRef, err)
		return
	}
	if err := fs.DeleteFile(ctx, bundleRef); err != nil {
		logger.Warnf(ctx, "[sandbox] delete bundle %s failed: %v", bundleRef, err)
	}
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
	case sandbox.SandboxTypeCube, sandbox.SandboxTypeE2B, sandbox.SandboxTypeDocker:
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
