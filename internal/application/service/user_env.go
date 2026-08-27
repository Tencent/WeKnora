package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// Where the value a skill would actually receive comes from. The three states
// are distinct on purpose: "the workspace filled this in for everybody" and "I
// filled this in for myself" lead to different actions, and neither is "nobody
// has filled this in".
const (
	EnvSourceUnset     = "unset"
	EnvSourceWorkspace = "workspace"
	EnvSourceUser      = "user"
)

// EnvVarView is one variable as its owner may see it. There is deliberately no
// value field: this view exists so a member can tell what is still missing, and
// a stored value is never read back out.
type EnvVarView struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Source      string `json:"source"`
	// UpdatedAt is set only for the caller's own value; a workspace value has
	// no per-variable timestamp to report.
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// SkillEnvGroup is one skill's declared credentials. It carries the skill's
// identity and nothing else about it — not the instructions, not the bundle,
// not the install state, all of which are Admin+ disclosure.
type SkillEnvGroup struct {
	SkillID   string       `json:"skill_id"`
	SkillName string       `json:"skill_name"`
	Vars      []EnvVarView `json:"vars"`
}

// ConfigEnvGroup is one sandbox config: the caller's own config-wide variables
// plus the declared credentials of the skills installed on it.
type ConfigEnvGroup struct {
	SandboxConfigID   string          `json:"sandbox_config_id"`
	SandboxConfigName string          `json:"sandbox_config_name"`
	Vars              []EnvVarView    `json:"vars"`
	Skills            []SkillEnvGroup `json:"skills"`
}

// UserEnvService serves the values one identity keeps for itself, in both
// scopes: config-wide variables and the credentials a skill declared.
//
// It is separate from TenantSkillService because its authority is different in
// kind. Every method derives the workspace and the identity from the context
// alone and touches only rows keyed by that identity, which is what makes the
// endpoints safe for any logged-in member while install and listing stay Admin+.
type UserEnvService struct {
	skills  repository.TenantSkillRepository
	configs repository.TenantSandboxConfigRepository
}

func NewUserEnvService(
	skills repository.TenantSkillRepository,
	configs repository.TenantSandboxConfigRepository,
) *UserEnvService {
	return &UserEnvService{skills: skills, configs: configs}
}

// caller resolves whose values this call may touch. A missing principal is an
// error rather than a default: falling back to any other identity would let one
// member read or overwrite another's values.
func (s *UserEnvService) caller(ctx context.Context) (uint64, types.Principal, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return 0, types.Principal{}, apperrors.NewUnauthorizedError(
			"workspace context is required to access environment variables")
	}
	principal, ok := types.PrincipalFromContext(ctx)
	if !ok {
		return 0, types.Principal{}, apperrors.NewUnauthorizedError(
			"principal context is required to access environment variables")
	}
	return tenantID, principal, nil
}

// ListMine returns every sandbox config of the workspace with the caller's own
// config-wide variables and the credentials its skills declared.
//
// A config is listed even when it carries nothing, because the config-wide
// editor has to be reachable. A skill is listed only when it declared
// something: an empty declaration has nothing to ask for.
//
// Only enabled, ready skills are listed. A disabled or half-installed skill is
// never handed to the agent, so asking for its credential asks for something
// nothing reads.
func (s *UserEnvService) ListMine(ctx context.Context) ([]ConfigEnvGroup, error) {
	tenantID, principal, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	configs, err := s.configs.ListByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	skills, err := s.skills.ListSkillsByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	skillsByConfig := map[string][]*types.TenantSkillEntity{}
	for _, row := range skills {
		if skillAcceptsUserEnvs(row) && len(row.Envs) > 0 {
			skillsByConfig[row.SandboxConfigID] = append(skillsByConfig[row.SandboxConfigID], row)
		}
	}

	groups := make([]ConfigEnvGroup, 0, len(configs))
	for _, cfg := range configs {
		if cfg == nil {
			continue
		}
		owned, err := s.skills.ListUserEnvVarsByConfig(ctx, tenantID, principal, cfg.ID)
		if err != nil {
			return nil, err
		}
		mineBySkill := map[string]map[string]*types.TenantUserEnvVar{}
		for _, row := range owned {
			if row == nil {
				continue
			}
			if mineBySkill[row.SkillID] == nil {
				mineBySkill[row.SkillID] = map[string]*types.TenantUserEnvVar{}
			}
			mineBySkill[row.SkillID][row.Name] = row
		}

		// Both lists are initialised so they marshal as [] rather than null:
		// the settings page reads .length off them.
		group := ConfigEnvGroup{
			SandboxConfigID:   cfg.ID,
			SandboxConfigName: cfg.Name,
			Vars:              configWideViews(mineBySkill[""]),
			Skills:            make([]SkillEnvGroup, 0, len(skillsByConfig[cfg.ID])),
		}
		for _, row := range skillsByConfig[cfg.ID] {
			group.Skills = append(group.Skills, SkillEnvGroup{
				SkillID:   row.ID,
				SkillName: row.Name,
				Vars:      declaredViews(row.Envs, mineBySkill[row.ID]),
			})
		}
		// The repository orders skills by creation; sorting by name is what
		// keeps the page stable when two were installed in one batch.
		sort.SliceStable(group.Skills, func(i, j int) bool {
			if group.Skills[i].SkillName != group.Skills[j].SkillName {
				return group.Skills[i].SkillName < group.Skills[j].SkillName
			}
			return group.Skills[i].SkillID < group.Skills[j].SkillID
		})
		groups = append(groups, group)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].SandboxConfigName != groups[j].SandboxConfigName {
			return groups[i].SandboxConfigName < groups[j].SandboxConfigName
		}
		return groups[i].SandboxConfigID < groups[j].SandboxConfigID
	})
	return groups, nil
}

// configWideViews lists the caller's own config-wide variables. They have no
// declaration behind them, so every one of them is theirs by definition.
func configWideViews(mine map[string]*types.TenantUserEnvVar) []EnvVarView {
	views := make([]EnvVarView, 0, len(mine))
	for _, row := range mine {
		view := EnvVarView{Name: row.Name, Source: EnvSourceUnset}
		applyOwnValue(&view, row)
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return views
}

// declaredViews layers the caller's own values onto the declaration in the same
// order the resolver injects them, so what a member sees is what a run gets.
func declaredViews(
	declared types.SkillEnvVars, mine map[string]*types.TenantUserEnvVar,
) []EnvVarView {
	views := make([]EnvVarView, 0, len(declared))
	for _, entry := range declared {
		view := EnvVarView{
			Name:        entry.Name,
			Description: entry.Description,
			Required:    entry.Required,
			Source:      EnvSourceUnset,
		}
		if entry.Value != "" {
			view.Source = EnvSourceWorkspace
		}
		applyOwnValue(&view, mine[entry.Name])
		views = append(views, view)
	}
	return views
}

// applyOwnValue promotes a view to the user source. A row whose value came back
// empty stays at its previous source: that only happens when the stored secret
// could not be decrypted, and reporting it as set would hide the one thing the
// member needs to do about it.
func applyOwnValue(view *EnvVarView, own *types.TenantUserEnvVar) {
	if own == nil || own.Value == "" {
		return
	}
	view.Source = EnvSourceUser
	updated := own.UpdatedAt
	view.UpdatedAt = &updated
}

// CaptureSkillEnv stores values a successful skill run already used, for the
// current principal only. It writes only names that skill declared; reserved
// names and a value that already matches the stored user row or the workspace
// admin value are skipped. Unknown or unusable skills are a no-op so a
// mis-inferred name cannot create a row.
func (s *UserEnvService) CaptureSkillEnv(
	ctx context.Context, configID, skillName string, pairs map[string]string,
) error {
	if len(pairs) == 0 || strings.TrimSpace(skillName) == "" {
		return nil
	}
	tenantID, principal, err := s.caller(ctx)
	if err != nil {
		return err
	}
	configID, err = s.visibleConfigID(ctx, tenantID, configID)
	if err != nil {
		return err
	}
	skill, err := s.skills.GetSkillByName(ctx, tenantID, configID, strings.TrimSpace(skillName))
	if err != nil {
		return err
	}
	if !skillAcceptsUserEnvs(skill) {
		return nil
	}

	owned, err := s.skills.ListUserEnvVars(ctx, tenantID, principal, configID, skill.ID)
	if err != nil {
		return err
	}
	mine := map[string]*types.TenantUserEnvVar{}
	for _, row := range owned {
		if row != nil {
			mine[row.Name] = row
		}
	}

	for name, value := range pairs {
		name = strings.TrimSpace(name)
		if value == "" || validateUserEnvName(name) != nil {
			continue
		}
		declared, ok := skill.Envs.Get(name)
		if !ok {
			continue
		}
		if declared.Value != "" && declared.Value == value {
			continue
		}
		if existing := mine[name]; existing != nil && existing.Value == value {
			continue
		}
		if err := s.upsertMine(ctx, tenantID, principal, configID, skill.ID, name, value); err != nil {
			return err
		}
	}
	return nil
}

// SetMineSkill stores the caller's own value for one variable a skill declared.
// A name outside the declaration is refused: free-form names belong to the
// config-wide scope, which is not tied to any one skill.
func (s *UserEnvService) SetMineSkill(ctx context.Context, skillID, name, value string) error {
	tenantID, principal, err := s.caller(ctx)
	if err != nil {
		return err
	}
	skill, err := s.findVisibleSkill(ctx, tenantID, strings.TrimSpace(skillID))
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if _, declared := skill.Envs.Get(name); !declared {
		return apperrors.NewBadRequestError(
			fmt.Sprintf("skill %s does not declare %s", skill.Name, name))
	}
	return s.upsertMine(ctx, tenantID, principal, skill.SandboxConfigID, skill.ID, name, value)
}

// DeleteMineSkill removes one of the caller's own skill values.
//
// It deliberately does not require the skill to be visible: the delete is
// already scoped to rows this identity owns, and revoking a credential must keep
// working after an admin disables the skill it was entered for — which is
// precisely when somebody is most likely to want it gone.
func (s *UserEnvService) DeleteMineSkill(ctx context.Context, skillID, name string) error {
	tenantID, principal, err := s.caller(ctx)
	if err != nil {
		return err
	}
	skillID, name = strings.TrimSpace(skillID), strings.TrimSpace(name)
	if skillID == "" || name == "" {
		return apperrors.NewBadRequestError("skill_id and name are required")
	}
	// The config is read off the caller's own row rather than the request, so a
	// delete cannot be aimed at another config.
	rows, err := s.skills.ListSkillsByTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row != nil && row.ID == skillID {
			return s.skills.DeleteUserEnvVar(
				ctx, tenantID, principal, row.SandboxConfigID, skillID, name)
		}
	}
	return types.ErrEnvVarNotFound
}

// SetMineSandbox stores the caller's own value for one config-wide variable.
// The name is free-form: this is the member-side counterpart of the admin's
// sandbox env_vars, not something a skill declared.
func (s *UserEnvService) SetMineSandbox(ctx context.Context, configID, name, value string) error {
	tenantID, principal, err := s.caller(ctx)
	if err != nil {
		return err
	}
	configID, err = s.visibleConfigID(ctx, tenantID, configID)
	if err != nil {
		return err
	}
	return s.upsertMine(ctx, tenantID, principal, configID, "", strings.TrimSpace(name), value)
}

// DeleteMineSandbox removes one of the caller's own config-wide variables.
func (s *UserEnvService) DeleteMineSandbox(ctx context.Context, configID, name string) error {
	tenantID, principal, err := s.caller(ctx)
	if err != nil {
		return err
	}
	configID, name = strings.TrimSpace(configID), strings.TrimSpace(name)
	if configID == "" || name == "" {
		return apperrors.NewBadRequestError("sandbox_config_id and name are required")
	}
	return s.skills.DeleteUserEnvVar(ctx, tenantID, principal, configID, "", name)
}

// upsertMine is the one write path both scopes share: same validation, same
// quota, same row shape. Only the scope key differs.
func (s *UserEnvService) upsertMine(
	ctx context.Context,
	tenantID uint64,
	principal types.Principal,
	configID, skillID, name, value string,
) error {
	if err := validateUserEnvName(name); err != nil {
		return apperrors.NewBadRequestError(err.Error())
	}
	if value == "" {
		return apperrors.NewBadRequestError(
			fmt.Sprintf("a value is required; delete %s to clear it", name))
	}
	if len(value) > MaxEnvValueBytes {
		return apperrors.NewBadRequestError(
			fmt.Sprintf("value of %s cannot exceed %d bytes", name, MaxEnvValueBytes))
	}

	owned, err := s.skills.ListUserEnvVars(ctx, tenantID, principal, configID, skillID)
	if err != nil {
		return err
	}
	// The quota bounds how many names one identity keeps in one scope, so an
	// overwrite is exempt: a user already at the limit must still be able to
	// rotate a key they hold.
	overwrite := false
	for _, e := range owned {
		if e != nil && e.Name == name {
			overwrite = true
			break
		}
	}
	if !overwrite && len(owned) >= MaxUserEnvVarsPerScope {
		return apperrors.NewBadRequestError(fmt.Sprintf(
			"you can keep at most %d environment variables here", MaxUserEnvVarsPerScope))
	}

	return s.skills.UpsertUserEnvVar(ctx, &types.TenantUserEnvVar{
		TenantID:        tenantID,
		PrincipalType:   principal.Type,
		PrincipalID:     principal.ID,
		SandboxConfigID: configID,
		SkillID:         skillID,
		Name:            name,
		Value:           value,
	})
}

// visibleConfigID resolves a sandbox config against the caller's own workspace.
// Without it an ID from another workspace would be accepted and a row written
// against it, which that workspace's runs would then read.
func (s *UserEnvService) visibleConfigID(
	ctx context.Context, tenantID uint64, configID string,
) (string, error) {
	configID = strings.TrimSpace(configID)
	if configID == "" {
		return "", apperrors.NewBadRequestError("sandbox_config_id is required")
	}
	cfg, err := s.configs.GetByID(ctx, tenantID, configID)
	if err != nil {
		return "", err
	}
	if cfg == nil {
		return "", apperrors.NewBadRequestError(
			fmt.Sprintf("sandbox config %s is not available in this workspace", configID))
	}
	return cfg.ID, nil
}

// findVisibleSkill resolves a skill ID against the caller's own workspace.
//
// Without this an ID from another workspace would be accepted and a row written
// against it, which the resolver of that workspace's run would then read. The
// lookup spans configs because the member-facing surface is about the person,
// not about one sandbox config.
func (s *UserEnvService) findVisibleSkill(
	ctx context.Context, tenantID uint64, skillID string,
) (*types.TenantSkillEntity, error) {
	if skillID == "" {
		return nil, apperrors.NewBadRequestError("skill_id is required")
	}
	rows, err := s.skills.ListSkillsByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row != nil && row.ID == skillID && skillAcceptsUserEnvs(row) {
			return row, nil
		}
	}
	// One message for "not yours", "not installed" and "not usable": which of
	// the three it is would itself be a disclosure.
	return nil, apperrors.NewBadRequestError(
		fmt.Sprintf("skill %s is not available in this workspace", skillID))
}

// skillAcceptsUserEnvs reports whether a skill is one the agent can actually
// run, which is the only kind worth asking a member for a credential for.
func skillAcceptsUserEnvs(row *types.TenantSkillEntity) bool {
	return row != nil && row.Enabled && row.Status == types.SkillStatusReady
}
