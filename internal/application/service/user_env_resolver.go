package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// userEnvReader is the slice of the repository this resolver needs. Narrowing it
// keeps the resolver testable without a database and makes the queries it
// issues per execution visible in the type.
type userEnvReader interface {
	ListUserEnvVars(
		ctx context.Context, tenantID uint64, p types.Principal, configID, skillID string,
	) ([]*types.TenantUserEnvVar, error)
}

// userEnvResolver answers skills.SkillEnvResolver from the skill rows this turn
// already loaded plus the caller's own values.
type userEnvResolver struct {
	// byName indexes the rows by the name the model addresses skills with.
	byName   map[string]*types.TenantSkillEntity
	userEnvs userEnvReader
	// tenantID and configID are the scope the rows came from, not whatever the
	// context happens to hold, so a lookup cannot resolve into another
	// workspace or another config.
	tenantID uint64
	configID string
}

// NewUserEnvResolver builds the resolver for one agent run. rows are this run's
// installed skills, already carrying their declarations and admin values; they
// may be empty, in which case only config-wide variables are resolved.
func NewUserEnvResolver(
	rows []*types.TenantSkillEntity, userEnvs userEnvReader, tenantID uint64, configID string,
) *userEnvResolver {
	byName := make(map[string]*types.TenantSkillEntity, len(rows))
	for _, row := range rows {
		if row != nil {
			byName[row.Name] = row
		}
	}
	return &userEnvResolver{
		byName: byName, userEnvs: userEnvs, tenantID: tenantID, configID: configID,
	}
}

// ResolveEnv layers the sources in a fixed order, each overwriting the last:
// the admin's workspace-wide skill values, then this caller's config-wide
// variables, then this caller's values for that skill. Mine beat the
// workspace's; among mine, the skill-specific one beats the config-wide one.
//
// An empty skillName resolves only the config-wide variables, which is what
// shell_exec gets when the model names no skill. An unknown name behaves the
// same way: host-staged skills carry no declaration.
func (r *userEnvResolver) ResolveEnv(
	ctx context.Context, skillName string,
) (map[string]string, []string, error) {
	// The identity is the Principal from the context and nothing else. A user
	// id would give every IM caller in a workspace the same synthetic account
	// and therefore the same values.
	principal, hasPrincipal := types.PrincipalFromContext(ctx)
	canReadMine := hasPrincipal && r.userEnvs != nil

	env := map[string]string{}
	row := r.byName[skillName]
	if row != nil {
		for _, declared := range row.Envs {
			// An empty admin value means unset; injecting it would make an
			// unconfigured variable indistinguishable from one set to "".
			if declared.Value != "" {
				env[declared.Name] = declared.Value
			}
		}
	}

	if canReadMine {
		if err := r.overlayMine(ctx, principal, "", env); err != nil {
			return nil, nil, err
		}
		if row != nil {
			if err := r.overlayMine(ctx, principal, row.ID, env); err != nil {
				return nil, nil, err
			}
		}
	}

	var missing []string
	if row != nil {
		for _, declared := range row.Envs {
			if declared.Required && env[declared.Name] == "" {
				missing = append(missing, declared.Name)
			}
		}
	}
	return env, missing, nil
}

// overlayMine writes the caller's own values for one scope over env. A failure
// is never degraded to the admin value: running a skill with somebody else's
// key is worse than not running it.
func (r *userEnvResolver) overlayMine(
	ctx context.Context, principal types.Principal, skillID string, env map[string]string,
) error {
	owned, err := r.userEnvs.ListUserEnvVars(ctx, r.tenantID, principal, r.configID, skillID)
	if err != nil {
		return err
	}
	for _, value := range owned {
		if value == nil || value.Value == "" {
			continue
		}
		env[value.Name] = value.Value
	}
	return nil
}
